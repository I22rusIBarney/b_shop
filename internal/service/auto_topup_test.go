package service_test

import (
	"context"
	"testing"

	"bshop/internal/remnawave"
	"bshop/internal/store"
)

func TestAutoTopup(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Promo", IsActive: true, DeviceLimit: 2, TrafficLimitGB: 50,
		TrafficTopupEnabled: true, TrafficPackages: []store.TrafficPackage{{GB: 20, Price: 5000}},
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 14900}}})
	c := login(t, e, 801, "")
	subID := *buyPaid(t, e, c, plan, 0, 0).SubscriptionID
	sub, _ := store.GetSubscription(ctx, e.pool, subID)

	// Needs a saved payment method.
	if _, err := e.svc.SetAutoTopup(ctx, c, subID, true, 0); err == nil {
		t.Fatal("auto top-up enabled without a saved card")
	}
	if _, err := store.SavePaymentMethod(ctx, e.pool, c.ID, "bank_card", "pm-1", "Карта *4444"); err != nil {
		t.Fatal(err)
	}
	o, err := e.svc.SetAutoTopup(ctx, c, subID, true, 0)
	if err != nil || !o.AutoTopup || o.AutoTopupAt != 90 {
		t.Fatalf("enable = %+v, %v", o, err)
	}

	setUsed := func(gb int64) {
		e.panel.mu.Lock()
		e.panel.users[*sub.RWUserID].UserTraffic = remnawave.UserTraffic{UsedTrafficBytes: gb << 30}
		e.panel.mu.Unlock()
	}
	limit := func() int64 { u, _ := e.panel.GetUser(ctx, *sub.RWUserID); return u.TrafficLimitBytes >> 30 }
	retry := func() { _, _ = e.pool.Exec(ctx, `UPDATE subscriptions SET auto_topup_last_try=NULL WHERE id=$1`, subID) }

	// 80% used: nothing happens.
	setUsed(40)
	e.svc.RunAutoTopups(ctx)
	if limit() != 50 {
		t.Fatalf("charged below 90%%: limit %d", limit())
	}
	// 90% used: the package is bought with the saved card, limit 70 GB.
	setUsed(45)
	e.svc.RunAutoTopups(ctx)
	if limit() != 70 {
		t.Fatalf("limit after auto top-up = %d, want 70", limit())
	}
	// Same usage again: 45 of 70 is below 90%, no second charge.
	retry()
	e.svc.RunAutoTopups(ctx)
	if limit() != 70 {
		t.Fatalf("charged twice: limit %d", limit())
	}

	// A declined card switches auto top-up off.
	setUsed(65)
	retry()
	e.gw.savedResult = "canceled"
	e.svc.RunAutoTopups(ctx)
	if got, _ := store.GetSubscription(ctx, e.pool, subID); got.AutoTopup || limit() != 70 {
		t.Fatalf("after decline: auto=%v limit=%d", got.AutoTopup, limit())
	}
}
