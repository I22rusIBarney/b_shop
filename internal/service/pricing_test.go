package service_test

import (
	"context"
	"testing"

	"bshop/internal/service"
	"bshop/internal/store"
)

func setMarkup(t *testing.T, e *env, c *store.Customer, pct int) *store.Customer {
	t.Helper()
	ctx := context.Background()
	if err := store.PatchCustomer(ctx, e.pool, c.ID, store.CustomerAdminPatch{PersonalMarkup: &pct}); err != nil {
		t.Fatal(err)
	}
	c, _ = store.GetCustomer(ctx, e.pool, c.ID)
	return c
}

func TestPersonalMarkup(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Модуль", IsActive: true, DeviceLimit: 1,
		ExtraDevicesAllowed: true, ExtraDevicePrice: 5000, MaxExtraDevices: 5,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 20000}}})
	plain := login(t, e, 3001, "")
	c := setMarkup(t, e, login(t, e, 3002, ""), 20)

	// The catalog shows the customer's own price; others keep the list price.
	cat, _ := e.svc.Catalog(ctx, c)
	if got := cat[0].Offers[0].Price; got != 24000 {
		t.Fatalf("catalog with markup = %d, want 24000", got)
	}
	if got := cat[0].Plan.ExtraDevicePrice; got != 6000 {
		t.Fatalf("device price with markup = %d, want 6000", got)
	}
	if cat, _ := e.svc.Catalog(ctx, plain); cat[0].Offers[0].Price != 20000 {
		t.Fatalf("plain customer price = %d", cat[0].Offers[0].Price)
	}
	// The customer never sees the markup itself.
	if c.Public().PersonalMarkup != 0 {
		t.Fatal("markup leaks to the cabinet")
	}

	// New purchase with 2 extra devices: (200 + 2 × 50) × 1.2 = 360 ₽, plus 3% commission on top, last.
	mustSet(t, e, "yookassa", `{"enabled":true,"shop_id":"1","secret_key":"live_secret","methods":["sbp"],"commission_percent":3}`)
	res, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, ExtraDevices: 2, Method: "sbp"})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := store.GetPayment(ctx, e.pool, res.PaymentID)
	if p.BaseAmount != 36000 || p.MarkupPercent != 20 || p.CommissionAmount != 1080 || p.Amount != 37080 {
		t.Fatalf("payment base %d markup %d commission %d amount %d", p.BaseAmount, p.MarkupPercent, p.CommissionAmount, p.Amount)
	}

	// Changing the rule later does not touch the order already created: it is paid and applied as is.
	c = setMarkup(t, e, c, 50)
	p = confirm(t, e, res)
	if p.Amount != 37080 {
		t.Fatalf("existing order changed to %d", p.Amount)
	}
	mustSet(t, e, "yookassa", `{"enabled":true,"shop_id":"1","secret_key":"live_secret","methods":["sbp"]}`)

	// Renewal keeps the 2 slots and uses the current markup: (200 + 100) × 1.5 = 450 ₽.
	res, err = e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, SubscriptionID: *p.SubscriptionID, Method: "sbp"})
	if err != nil || res.Amount != 45000 {
		t.Fatalf("renewal = %+v, %v", res, err)
	}

	// A promo discount applies to the marked-up price, once: 450 × 0.9 = 405 ₽.
	promo := store.Promo{Code: "TEN", Kind: "discount", Value: 10, IsActive: true}
	if err := service.ValidatePromo(&promo); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePromo(ctx, e.pool, &promo); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RedeemPromo(ctx, c, "TEN", 0); err != nil {
		t.Fatal(err)
	}
	res, err = e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, SubscriptionID: *p.SubscriptionID, Method: "sbp"})
	if err != nil || res.Amount != 40500 {
		t.Fatalf("renewal with promo = %+v, %v", res, err)
	}

	// Extra devices for the rest of the period carry the markup too.
	o, err := e.svc.DevicesOffer(ctx, c, *p.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if o.PricePerMonth != 7500 {
		t.Fatalf("device offer per month = %d, want 7500", o.PricePerMonth)
	}
	plainOffer := applyDays(5000, o.DaysLeft)
	if o.PricePerDevice <= plainOffer {
		t.Fatalf("device offer %d is not above the list price %d", o.PricePerDevice, plainOffer)
	}

	// Autopay quotes the marked-up price as well.
	ap, err := e.svc.Autopay(ctx, c, *p.SubscriptionID)
	if err != nil || ap.Amount != 45000 {
		t.Fatalf("autopay amount = %+v, %v", ap, err)
	}
}

// applyDays is the list price of one device slot for the given days of a 31-day month.
func applyDays(monthly int64, days int) int64 { return monthly * int64(days) / 31 }

func TestMarkupOnPlanChangeAndGift(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	small := createPlan(t, e, store.Plan{Name: "S", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 31000}}})
	big := createPlan(t, e, store.Plan{Name: "L", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 62000}}})
	c := login(t, e, 3101, "")
	res, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: small.ID, PeriodID: small.Periods[0].ID, Method: "sbp"})
	if err != nil {
		t.Fatal(err)
	}
	p := confirm(t, e, res)
	info, err := e.svc.ChangeOptions(ctx, c, *p.SubscriptionID)
	if err != nil || len(info.Targets) == 0 {
		t.Fatalf("change options: %v", err)
	}
	plainUp := info.Targets[0].Quotes[0]
	c = setMarkup(t, e, c, 100)
	info, _ = e.svc.ChangeOptions(ctx, c, *p.SubscriptionID)
	up := info.Targets[0].Quotes[0]
	// Both plans are marked up, so the surcharge doubles and the converted days stay the same.
	if up.Mode != "upgrade" || up.BaseAmount != 2*plainUp.BaseAmount {
		t.Fatalf("upgrade base %d, plain %d", up.BaseAmount, plainUp.BaseAmount)
	}
	if info.Targets[0].Quotes[1].DaysAfter != func() int {
		c0, _ := store.GetCustomer(ctx, e.pool, c.ID)
		c0.PersonalMarkup = 0
		i, _ := e.svc.ChangeOptions(ctx, c0, *p.SubscriptionID)
		return i.Targets[0].Quotes[1].DaysAfter
	}() {
		t.Fatal("markup changed the converted days")
	}
	res, err = e.svc.ChangePlan(ctx, c, *p.SubscriptionID, service.ChangeRequest{PlanID: big.ID, Mode: "upgrade", Method: "sbp"})
	if err != nil {
		t.Fatal(err)
	}
	cp, _ := store.GetPayment(ctx, e.pool, res.PaymentID)
	if cp.MarkupPercent != 100 || cp.Amount != up.Amount {
		t.Fatalf("change payment markup %d amount %d, quote %d", cp.MarkupPercent, cp.Amount, up.Amount)
	}

	// A gift is paid at the payer's prices.
	mustSet(t, e, "gifts", `{"enabled":true,"buy_for_other":true}`)
	g, err := e.svc.BuyGift(ctx, c, service.GiftRequest{PlanID: small.ID, PeriodID: small.Periods[0].ID, Method: "sbp"})
	if err != nil || g.Amount != 62000 {
		t.Fatalf("gift = %+v, %v", g, err)
	}
}
