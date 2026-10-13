package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"bshop/internal/service"
	"bshop/internal/store"

	"github.com/google/uuid"
)

// buyPaid buys a plan period (optionally renewing subID) and confirms the payment.
func buyPaid(t *testing.T, e *env, c *store.Customer, plan *store.Plan, periodIdx int, subID int64) *store.Payment {
	t.Helper()
	ctx := context.Background()
	res, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[periodIdx].ID, SubscriptionID: subID, Method: "sbp"})
	if err != nil {
		t.Fatal(err)
	}
	return confirm(t, e, res)
}

func confirm(t *testing.T, e *env, res *service.BuyResult) *store.Payment {
	t.Helper()
	ctx := context.Background()
	p, err := store.GetPayment(ctx, e.pool, res.PaymentID)
	if err != nil {
		t.Fatal(err)
	}
	if p.ProviderRef != nil {
		e.gw.succeed(*p.ProviderRef, "sbp")
		notify(t, e, *p.ProviderRef)
	}
	p, _ = store.GetPayment(ctx, e.pool, res.PaymentID)
	if p.AppliedAt == nil {
		t.Fatalf("payment %s not applied", p.ID)
	}
	return p
}

func days(d time.Duration) float64 { return d.Hours() / 24 }

func near(got, want float64) bool { return got > want-0.05 && got < want+0.05 }

func TestPromoCodes(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Бобрёнок", IsActive: true, DeviceLimit: 3,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 30000}}})
	other := createPlan(t, e, store.Plan{Name: "Другой", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 10000}}})
	c := login(t, e, 2001, "")

	save := func(p store.Promo) *store.Promo {
		t.Helper()
		if err := service.ValidatePromo(&p); err != nil {
			t.Fatal(err)
		}
		if err := store.SavePromo(ctx, e.pool, &p); err != nil {
			t.Fatal(err)
		}
		return &p
	}
	save(store.Promo{Code: "cart10", Kind: "discount", Value: 10, IsActive: true})
	save(store.Promo{Code: "COMEBACK", Kind: "discount", Value: 20, IsActive: true, PlanIDs: []int64{other.ID}})
	save(store.Promo{Code: "BONUS5", Kind: "days", Value: 5, IsActive: true, MaxUses: 1})
	save(store.Promo{Code: "OLD", Kind: "discount", Value: 50, IsActive: true, ValidUntil: ptr(time.Now().Add(-time.Hour))})
	stranger := login(t, e, 2999, "")
	save(store.Promo{Code: "MINE", Kind: "discount", Value: 30, IsActive: true, CustomerID: &stranger.ID})

	if _, err := e.svc.RedeemPromo(ctx, c, "nope", 0); err == nil {
		t.Fatal("unknown code accepted")
	}
	if _, err := e.svc.RedeemPromo(ctx, c, "old", 0); err == nil || !strings.Contains(err.Error(), "истёк") {
		t.Fatalf("expired code: %v", err)
	}
	if _, err := e.svc.RedeemPromo(ctx, c, "MINE", 0); err == nil {
		t.Fatal("someone else's personal code accepted")
	}

	// Days code without a subscription goes to the bonus balance.
	r, err := e.svc.RedeemPromo(ctx, c, " bonus5 ", 0)
	if err != nil || !r.Pending {
		t.Fatalf("days code: %+v %v", r, err)
	}
	if _, err := e.svc.RedeemPromo(ctx, stranger, "BONUS5", 0); err == nil {
		t.Fatal("max uses not enforced")
	}

	// Discount code: reserved, shows in the catalog only for matching plans.
	if _, err := e.svc.RedeemPromo(ctx, c, "CART10", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RedeemPromo(ctx, c, "CART10", 0); err == nil {
		t.Fatal("redeemed twice")
	}
	cat, _ := e.svc.Catalog(ctx, c)
	for _, p := range cat {
		if p.DiscountPercent != 10 || p.PromoCode != "CART10" {
			t.Fatalf("catalog %s: %d %q", p.Name, p.DiscountPercent, p.PromoCode)
		}
	}
	// COMEBACK (20%) only for the other plan: it wins there, CART10 stays for the main plan.
	if _, err := e.svc.RedeemPromo(ctx, c, "comeback", 0); err != nil {
		t.Fatal(err)
	}
	cat, _ = e.svc.Catalog(ctx, c)
	for _, p := range cat {
		want := map[int64]int{plan.ID: 10, other.ID: 20}[p.ID]
		if p.DiscountPercent != want {
			t.Fatalf("plan %s discount %d, want %d", p.Name, p.DiscountPercent, want)
		}
	}

	// Buying uses the reserved code and the bonus days from the balance.
	pay := buyPaid(t, e, c, plan, 0, 0)
	if pay.Amount != 27000 || pay.PromoCode != "CART10" {
		t.Fatalf("payment amount %d code %q", pay.Amount, pay.PromoCode)
	}
	red, _ := store.ListRedemptions(ctx, e.pool, 0, c.ID)
	used := map[string]string{}
	for _, x := range red {
		used[x.Code] = x.Status
	}
	if used["CART10"] != "used" || used["COMEBACK"] != "reserved" || used["BONUS5"] != "used" {
		t.Fatalf("redemptions %v", used)
	}
	sub, _ := store.GetSubscription(ctx, e.pool, *pay.SubscriptionID)
	if d := days(time.Until(sub.ExpiresAt)); !near(d, 36) {
		t.Fatalf("expiry %.2f days, want 31+5", d)
	}
	c, _ = store.GetCustomer(ctx, e.pool, c.ID)
	if c.BonusDays != 0 {
		t.Fatalf("bonus balance not consumed: %d", c.BonusDays)
	}

	// A days code with an active subscription extends it right away.
	save(store.Promo{Code: "WEEK", Kind: "days", Value: 7, IsActive: true})
	before := sub.ExpiresAt
	r, err = e.svc.RedeemPromo(ctx, c, "WEEK", 0)
	if err != nil || r.Pending || r.SubscriptionID == nil {
		t.Fatalf("week: %+v %v", r, err)
	}
	sub, _ = store.GetSubscription(ctx, e.pool, sub.ID)
	if got := days(sub.ExpiresAt.Sub(before)); !near(got, 7) {
		t.Fatalf("WEEK added %.2f days", got)
	}
	if pu := e.panel.users[*sub.RWUserID]; !pu.ExpireAt.Equal(sub.ExpiresAt) {
		t.Fatal("panel expiry not updated")
	}

	// New-customers-only code is refused after a paid purchase.
	save(store.Promo{Code: "FIRST", Kind: "discount", Value: 15, IsActive: true, NewCustomersOnly: true})
	if _, err := e.svc.RedeemPromo(ctx, c, "FIRST", 0); err == nil {
		t.Fatal("new-customers-only code accepted")
	}
}

func ptr[T any](v T) *T { return &v }

func TestPlanChange(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mid := createPlan(t, e, store.Plan{Name: "Бобрёнок", IsActive: true, DeviceLimit: 3, ExtraDevicesAllowed: true, ExtraDevicePrice: 5000,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 31000}}})
	top := createPlan(t, e, store.Plan{Name: "Максимум", IsActive: true, DeviceLimit: 12,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 62000}}})
	low := createPlan(t, e, store.Plan{Name: "AI", IsActive: true, DeviceLimit: 2,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 15500}}})
	createPlan(t, e, store.Plan{Name: "Закрытый", IsActive: true, ChangeToDisabled: true,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 99900}}})
	c := login(t, e, 3001, "")
	pay := buyPaid(t, e, c, mid, 0, 0)
	subID := *pay.SubscriptionID

	info, err := e.svc.ChangeOptions(ctx, c, subID)
	if err != nil {
		t.Fatal(err)
	}
	if info.DaysLeft != 31 || len(info.Targets) != 2 {
		t.Fatalf("change info: days %d targets %+v", info.DaysLeft, info.Targets)
	}
	var up, down service.ChangeTarget
	for _, x := range info.Targets {
		if x.PlanID == top.ID {
			up = x
		}
		if x.PlanID == low.ID {
			down = x
		}
	}
	// Upgrade 310 → 620 ₽/mo with 31 days left: pay 310 ₽, or convert to 15 days.
	if len(up.Quotes) != 2 || up.Quotes[0].Mode != "upgrade" || up.Quotes[0].Amount != 31000 || up.Quotes[1].DaysAfter != 15 {
		t.Fatalf("upgrade quotes %+v", up.Quotes)
	}
	// Downgrade to 155 ₽/mo: days double.
	if len(down.Quotes) != 1 || down.Quotes[0].Mode != "downgrade" || down.Quotes[0].DaysAfter != 62 {
		t.Fatalf("downgrade quotes %+v", down.Quotes)
	}

	// Paid upgrade keeps the expiry and switches plan + device limit in the panel.
	sub0, _ := store.GetSubscription(ctx, e.pool, subID)
	res, err := e.svc.ChangePlan(ctx, c, subID, service.ChangeRequest{PlanID: top.ID, Mode: "upgrade", Method: "sbp"})
	if err != nil || res.Amount != 31000 {
		t.Fatalf("change: %+v %v", res, err)
	}
	p := confirm(t, e, res)
	if p.Operation != "change" || p.FromPlanID == nil || *p.FromPlanID != mid.ID {
		t.Fatalf("change payment %+v", p)
	}
	sub, _ := store.GetSubscription(ctx, e.pool, subID)
	if *sub.PlanID != top.ID || sub.Title != "Максимум" || sub.ExpiresAt.Sub(sub0.ExpiresAt).Abs() > time.Second {
		t.Fatalf("after upgrade %+v", sub)
	}
	if *e.panel.users[*sub.RWUserID].HwidDeviceLimit != 12 {
		t.Fatal("panel device limit not updated")
	}
	e.svc.ProcessReceipts(ctx)
	if n := len(e.receipts.issued); n != 2 || e.receipts.issued[1].name != "VPN Максимум на 1 мес." {
		t.Fatalf("receipts %+v", e.receipts.issued)
	}

	// Free downgrade: days recalculated immediately (620 → 155 ₽: ×4).
	res, err = e.svc.ChangePlan(ctx, c, subID, service.ChangeRequest{PlanID: low.ID, Mode: "downgrade"})
	if err != nil || !res.Applied {
		t.Fatalf("downgrade %+v %v", res, err)
	}
	sub, _ = store.GetSubscription(ctx, e.pool, subID)
	if d := days(time.Until(sub.ExpiresAt)); *sub.PlanID != low.ID || !near(d, 124) {
		t.Fatalf("after downgrade plan %d days %.2f", *sub.PlanID, d)
	}
	changes, _ := store.ListPlanChanges(ctx, e.pool, c.ID)
	if len(changes) != 2 || changes[0].Mode != "downgrade" || changes[1].Mode != "upgrade" {
		t.Fatalf("plan changes %+v", changes)
	}
	if _, err := e.svc.ChangePlan(ctx, c, subID, service.ChangeRequest{PlanID: low.ID, Mode: "downgrade"}); err == nil {
		t.Fatal("change to the same plan accepted")
	}

	// Trial subscriptions cannot use plan change; buying another plan converts them instead.
	mustSet(t, e, "trial", fmt.Sprintf(`{"enabled":true,"plan_id":%d,"days":3}`, low.ID))
	c2 := login(t, e, 3002, "")
	trial, err := e.svc.ActivateTrial(ctx, c2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ChangeOptions(ctx, c2, trial.ID); err == nil {
		t.Fatal("trial plan change allowed")
	}
	buyPaid(t, e, c2, mid, 0, trial.ID)
	trial, _ = store.GetSubscription(ctx, e.pool, trial.ID)
	if trial.Kind != "paid" || *trial.PlanID != mid.ID || !near(days(time.Until(trial.ExpiresAt)), 34) {
		t.Fatalf("trial → paid %+v (%.2f days)", trial, days(time.Until(trial.ExpiresAt)))
	}
}

func TestExtraDevices(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Бобрёнок", IsActive: true, DeviceLimit: 3, ExtraDevicesAllowed: true,
		ExtraDevicePrice: 6200, MaxExtraDevices: 2, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 31000}}})
	c := login(t, e, 4001, "")
	pay := buyPaid(t, e, c, plan, 0, 0)
	subID := *pay.SubscriptionID

	o, err := e.svc.DevicesOffer(ctx, c, subID)
	if err != nil || !o.Allowed || o.MaxMore != 2 || o.PricePerDevice != 6200 {
		t.Fatalf("offer %+v %v", o, err)
	}
	if _, err := e.svc.BuyDevices(ctx, c, subID, service.DevicesRequest{Count: 3, Method: "sbp"}); err == nil {
		t.Fatal("over the max accepted")
	}
	res, err := e.svc.BuyDevices(ctx, c, subID, service.DevicesRequest{Count: 2, Method: "sbp"})
	if err != nil || res.Amount != 12400 {
		t.Fatalf("buy devices %+v %v", res, err)
	}
	confirm(t, e, res)
	sub, _ := store.GetSubscription(ctx, e.pool, subID)
	if sub.ExtraDevices != 2 || *e.panel.users[*sub.RWUserID].HwidDeviceLimit != 5 {
		t.Fatalf("extra devices %d panel %d", sub.ExtraDevices, *e.panel.users[*sub.RWUserID].HwidDeviceLimit)
	}
	if o, _ := e.svc.DevicesOffer(ctx, c, subID); o.Allowed {
		t.Fatal("offer still allowed at the max")
	}
	// Renewal includes the bought slots.
	res2, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, SubscriptionID: subID, Method: "sbp"})
	if err != nil || res2.Amount != 31000+12400 {
		t.Fatalf("renew with devices %+v %v", res2, err)
	}
	confirm(t, e, res2)
	sub, _ = store.GetSubscription(ctx, e.pool, subID)
	if sub.ExtraDevices != 2 {
		t.Fatalf("renewal changed extra devices: %d", sub.ExtraDevices)
	}
}

func TestReferralBonuses(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mustSet(t, e, "referral", `{"enabled":true,"base_enabled":true,"inviter_days":3,"invitee_days":3,
		"tiers_enabled":true,"tiers":[{"min_invited":2,"days":7}],"recurring_enabled":true,"recurring_percent":10}`)
	plan := createPlan(t, e, store.Plan{Name: "Бобрёнок", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 31000}, {Unit: "month", Amount: 12, Price: 300000}}})
	inviter := login(t, e, 5001, "")

	// Inviter has no subscription yet: the bonus waits on the balance.
	b1 := login(t, e, 5002, "ref_"+*inviter.RefCode)
	if b1.ReferrerID == nil || *b1.ReferrerID != inviter.ID {
		t.Fatal("referrer not linked")
	}
	p := buyPaid(t, e, b1, plan, 0, 0)
	inv, _ := store.GetCustomer(ctx, e.pool, inviter.ID)
	if inv.BonusDays != 3 {
		t.Fatalf("inviter balance %d", inv.BonusDays)
	}
	bsub, _ := store.GetSubscription(ctx, e.pool, *p.SubscriptionID)
	if d := days(time.Until(bsub.ExpiresAt)); !near(d, 34) {
		t.Fatalf("invitee got %.2f days, want 31+3", d)
	}
	// Re-processing is a no-op.
	if err := e.svc.ProcessReferral(ctx, p.ID); err != nil {
		t.Fatal(err)
	}

	// The inviter buys: the balance goes into the new subscription.
	ip := buyPaid(t, e, inv, plan, 0, 0)
	isub, _ := store.GetSubscription(ctx, e.pool, *ip.SubscriptionID)
	if d := days(time.Until(isub.ExpiresAt)); !near(d, 34) {
		t.Fatalf("inviter sub %.2f days", d)
	}

	// Recurring: 10% of a 12-month renewal (372 days) = 37 days.
	before := isub.ExpiresAt
	buyPaid(t, e, b1, plan, 1, bsub.ID)
	isub, _ = store.GetSubscription(ctx, e.pool, isub.ID)
	if got := days(isub.ExpiresAt.Sub(before)); !near(got, 37) {
		t.Fatalf("recurring bonus %.2f days", got)
	}

	// Second paying referral reaches the tier: 7 days instead of 3.
	b2 := login(t, e, 5003, "ref_"+*inviter.RefCode)
	before = isub.ExpiresAt
	buyPaid(t, e, b2, plan, 0, 0)
	isub, _ = store.GetSubscription(ctx, e.pool, isub.ID)
	if got := days(isub.ExpiresAt.Sub(before)); !near(got, 7) {
		t.Fatalf("tier bonus %.2f days", got)
	}
	bon, _ := store.ListReferralBonuses(ctx, e.pool, inviter.ID, 10)
	kinds := []string{}
	for _, b := range bon {
		kinds = append(kinds, fmt.Sprintf("%s:%d", b.Kind, b.Days))
	}
	if strings.Join(kinds, ",") != "first:7,recurring:37,first:3" {
		t.Fatalf("bonuses %v", kinds)
	}
	info, err := e.svc.Referral(ctx, inviter, "")
	if err != nil || info.Stats.Invited != 2 || info.Stats.Paid != 2 || info.Stats.Days != 47 || info.CurrentDays != 7 {
		t.Fatalf("referral info %+v %v", info, err)
	}
}

func TestAutopay(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Бобрёнок", IsActive: true,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 31000}, {Unit: "month", Amount: 3, Price: 84000}}})
	c := login(t, e, 6001, "")
	res, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[1].ID, Method: "sbp", SaveCard: true})
	if err != nil {
		t.Fatal(err)
	}
	if !e.gw.created[0].SavePaymentMethod {
		t.Fatal("save_payment_method not requested")
	}
	e.gw.succeedSaved("yk-1", "sbp", "pm-abc")
	notify(t, e, "yk-1")
	subs, _ := store.ListSubscriptions(ctx, e.pool, c.ID)
	sub := subs[0]
	if !sub.Autopay || sub.AutopayMethodID == nil || sub.AutopayPeriodID == nil || *sub.AutopayPeriodID != plan.Periods[1].ID {
		t.Fatalf("autopay not enabled after saving: %+v", sub)
	}
	_ = res

	// Nothing is due yet.
	e.svc.RunAutopay(ctx)
	if len(e.gw.created) != 1 {
		t.Fatal("charged too early")
	}
	// 2 days before expiry: notice only (notify 3 days, charge 1 day before).
	setExpiry := func(d time.Duration) {
		t.Helper()
		if _, err := e.pool.Exec(ctx, `UPDATE subscriptions SET expires_at=$2 WHERE id=$1`, sub.ID, time.Now().Add(d)); err != nil {
			t.Fatal(err)
		}
	}
	setExpiry(48 * time.Hour)
	e.svc.RunAutopay(ctx)
	if len(e.gw.created) != 1 || !strings.Contains(strings.Join(e.notes.customer, "|"), "840 ₽") {
		t.Fatalf("expected a notice only; created=%d notes=%v", len(e.gw.created), e.notes.customer)
	}
	// 12 hours before expiry: charged with the saved method, subscription renewed for 3 months.
	setExpiry(12 * time.Hour)
	e.svc.RunAutopay(ctx)
	if len(e.gw.created) != 2 || e.gw.created[1].PaymentMethodID != "pm-abc" || e.gw.created[1].Confirmation != nil {
		t.Fatalf("autopay request %+v", e.gw.created)
	}
	sub2, _ := store.GetSubscription(ctx, e.pool, sub.ID)
	if d := days(time.Until(sub2.ExpiresAt)); !near(d, 93.5) {
		t.Fatalf("after autopay %.2f days", d)
	}
	// Second run the same day does not charge again.
	setExpiry(12 * time.Hour)
	e.svc.RunAutopay(ctx)
	if len(e.gw.created) != 2 {
		t.Fatal("charged twice within a day")
	}

	// A declined charge notifies the customer and admins, the payment is canceled.
	e.gw.savedResult = "canceled"
	if _, err := e.pool.Exec(ctx, `UPDATE subscriptions SET autopay_last_try=NULL WHERE id=$1`, sub.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ChargeAutopayNow(ctx, sub.ID); err != nil {
		t.Fatal(err)
	}
	last, _ := store.GetPaymentByProviderRef(ctx, e.pool, "yookassa", "yk-3")
	if last == nil || last.Status != "canceled" || !last.Autopay {
		t.Fatalf("declined autopay payment %+v", last)
	}
	if !strings.Contains(strings.Join(e.notes.admin, "|"), "autopay_error") ||
		!strings.Contains(strings.Join(e.notes.customer, "|"), "недостаточно средств") {
		t.Fatalf("no failure notices: %v / %v", e.notes.admin, e.notes.customer)
	}

	// Turning autopay off from the cabinet.
	info, err := e.svc.SetAutopay(ctx, c, sub.ID, false, 0)
	if err != nil || info.Enabled {
		t.Fatalf("autopay off %+v %v", info, err)
	}
	_ = uuid.Nil
}
