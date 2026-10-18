package service_test

import (
	"context"
	"testing"

	"bshop/internal/remnawave"
	"bshop/internal/service"
	"bshop/internal/store"
)

func TestTrafficTopupAndReset(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Promo", IsActive: true, DeviceLimit: 2, TrafficLimitGB: 50,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 14900}}})
	c := login(t, e, 601, "")
	res, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"})
	if err != nil {
		t.Fatal(err)
	}
	e.gw.succeed("yk-1", "sbp")
	notify(t, e, "yk-1")
	_ = res
	subs, _ := store.ListSubscriptions(ctx, e.pool, c.ID)
	sub := subs[0]

	// Off by default: nothing to buy, no reset.
	o, err := e.svc.TrafficOptions(ctx, c, sub.ID)
	if err != nil || o.Topup || o.Reset {
		t.Fatalf("defaults = %+v, %v", o, err)
	}
	if _, err := e.svc.BuyTraffic(ctx, c, sub.ID, 0, "sbp", ""); err == nil {
		t.Fatal("bought traffic while disabled")
	}

	plan.TrafficTopupEnabled = true
	plan.TrafficPackages = []store.TrafficPackage{{GB: 100, Price: 9900}}
	plan.TrafficResetEnabled = true
	plan.TrafficResetPrice = 10000
	if err := service.ValidatePlan(plan); err != nil {
		t.Fatal(err)
	}
	createPlan(t, e, *plan)

	// Buy 100 GB: limit becomes 150 GB in the panel and in the subscription.
	if _, err := e.svc.BuyTraffic(ctx, c, sub.ID, 0, "sbp", ""); err != nil {
		t.Fatal(err)
	}
	e.gw.succeed("yk-2", "sbp")
	notify(t, e, "yk-2")
	notify(t, e, "yk-2")
	u, _ := e.panel.GetUser(ctx, *sub.RWUserID)
	if u.TrafficLimitBytes != 150<<30 {
		t.Fatalf("panel limit = %d GB, want 150", u.TrafficLimitBytes>>30)
	}

	// Reset is refused below the threshold (80% by default), allowed above it and paid.
	if _, err := e.svc.ResetTrafficPaid(ctx, c, sub.ID, "sbp", ""); err == nil {
		t.Fatal("reset allowed with no traffic used")
	}
	e.panel.mu.Lock()
	e.panel.users[*sub.RWUserID].UserTraffic = remnawave.UserTraffic{UsedTrafficBytes: 140 << 30}
	e.panel.mu.Unlock()
	r, err := e.svc.ResetTrafficPaid(ctx, c, sub.ID, "sbp", "")
	if err != nil || r.Amount != 10000 {
		t.Fatalf("reset = %+v, %v", r, err)
	}
	e.gw.succeed("yk-3", "sbp")
	notify(t, e, "yk-3")
	if e.panel.resets != 1 {
		t.Fatalf("panel resets = %d, want 1", e.panel.resets)
	}

	// Renewal clears the bought traffic: back to the plan's 50 GB.
	if _, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, SubscriptionID: sub.ID, Method: "sbp"}); err != nil {
		t.Fatal(err)
	}
	e.gw.succeed("yk-4", "sbp")
	notify(t, e, "yk-4")
	u, _ = e.panel.GetUser(ctx, *sub.RWUserID)
	if u.TrafficLimitBytes != 50<<30 {
		t.Fatalf("limit after renewal = %d GB, want 50", u.TrafficLimitBytes>>30)
	}
}

func TestLocations(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.panel.nodes = []remnawave.Node{
		{Name: "Финляндия", CountryCode: "fi", Address: "10.0.0.1", IsConnected: true},
		{Name: "Швеция", CountryCode: "se", IsConnected: false},
		{Name: "Тест", CountryCode: "de", IsConnected: true, IsDisabled: true},
	}
	if _, err := e.svc.Locations(ctx); err == nil {
		t.Fatal("locations shown while disabled")
	}
	mustSet(t, e, "locations", `{"enabled":true}`)
	l, err := e.svc.Locations(ctx)
	if err != nil || len(l) != 1 || l[0].Name != "Финляндия" || l[0].Country != "FI" {
		t.Fatalf("locations = %+v, %v", l, err)
	}
	mustSet(t, e, "locations", `{"enabled":true,"show_offline":true}`)
	if l, _ := e.svc.Locations(ctx); len(l) != 2 || l[1].Online {
		t.Fatalf("with offline = %+v", l)
	}
}
