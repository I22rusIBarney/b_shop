package service_test

import (
	"context"
	"testing"

	"bshop/internal/service"
	"bshop/internal/store"
)

func TestGiftsAndBuyForOther(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Бобрёнок", IsActive: true, DeviceLimit: 3,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 29900}}})
	payer := login(t, e, 501, "")
	friend := login(t, e, 502, "")
	req := service.GiftRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"}

	// Both features are off by default.
	if _, err := e.svc.BuyGift(ctx, payer, req); err == nil {
		t.Fatal("gift bought while disabled")
	}
	if _, err := e.svc.LookupRecipient(ctx, payer, "@user502"); err == nil {
		t.Fatal("lookup worked while disabled")
	}
	mustSet(t, e, "gifts", `{"enabled":true,"buy_for_other":true}`)

	// Buy for another: lookup by @username, by Telegram ID; self and unknown are rejected.
	r, err := e.svc.LookupRecipient(ctx, payer, "@user502")
	if err != nil || r.Name != "Тест" {
		t.Fatalf("lookup = %+v, %v", r, err)
	}
	if _, err := e.svc.LookupRecipient(ctx, payer, "501"); err == nil {
		t.Fatal("payer found themselves as recipient")
	}
	if _, err := e.svc.LookupRecipient(ctx, payer, "@nobody_here"); err == nil {
		t.Fatal("unknown recipient found")
	}
	req.Recipient = "502"
	res, err := e.svc.BuyForOther(ctx, payer, req)
	if err != nil || res.Amount != 29900 {
		t.Fatalf("buy for other = %+v, %v", res, err)
	}
	e.gw.succeed("yk-1", "sbp")
	notify(t, e, "yk-1")
	notify(t, e, "yk-1") // duplicate webhook must not give a second subscription
	if subs, _ := store.ListSubscriptions(ctx, e.pool, friend.ID); len(subs) != 1 {
		t.Fatalf("recipient subs = %d, want 1", len(subs))
	}
	if subs, _ := store.ListSubscriptions(ctx, e.pool, payer.ID); len(subs) != 0 {
		t.Fatalf("payer got a subscription too: %d", len(subs))
	}

	// Gift: paid → code appears; activation gives the subscription once.
	req.Recipient = ""
	if _, err := e.svc.BuyGift(ctx, payer, req); err != nil {
		t.Fatal(err)
	}
	if gifts, _ := e.svc.ListGifts(ctx, payer); len(gifts) != 0 {
		t.Fatalf("gift exists before payment: %d", len(gifts))
	}
	e.gw.succeed("yk-2", "sbp")
	notify(t, e, "yk-2")
	notify(t, e, "yk-2")
	gifts, _ := e.svc.ListGifts(ctx, payer)
	if len(gifts) != 1 || gifts[0].Status != "ready" || gifts[0].Days != 31 {
		t.Fatalf("gifts = %+v", gifts)
	}
	third := login(t, e, 503, "")
	sub, err := e.svc.RedeemGift(ctx, third, " "+gifts[0].Code+" ")
	if err != nil || sub.CustomerID != third.ID {
		t.Fatalf("redeem = %+v, %v", sub, err)
	}
	if _, err := e.svc.RedeemGift(ctx, friend, gifts[0].Code); err == nil {
		t.Fatal("gift redeemed twice")
	}
	if _, err := e.svc.RedeemGift(ctx, friend, "GIFT-XXXX-XXXX"); err == nil {
		t.Fatal("unknown code accepted")
	}
	gifts, _ = e.svc.ListGifts(ctx, payer)
	if gifts[0].Status != "redeemed" {
		t.Fatalf("gift status = %s", gifts[0].Status)
	}
}
