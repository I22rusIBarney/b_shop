package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"bshop/internal/service"
	"bshop/internal/store"
	"bshop/internal/yookassa"
)

func TestReconcileRecoversLostPayment(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Бобр", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 29900}}})
	c := login(t, e, 4001, "")
	res, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := store.GetPayment(ctx, e.pool, res.PaymentID)
	// The customer paid, the webhook was lost and the shop already expired the order locally.
	e.gw.succeed(*p.ProviderRef, "sbp")
	if _, err := e.pool.Exec(ctx, `UPDATE payments SET status='canceled', cancel_reason='expired' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	// Another YooKassa payment the shop knows nothing about.
	e.gw.payments["yk-foreign"] = &yookassa.Payment{ID: "yk-foreign", Status: "succeeded", Amount: yookassa.Kopecks(5000), CreatedAt: time.Now()}

	if _, err := e.svc.ReconcileYooKassa(ctx, time.Now().Add(-40*24*time.Hour), time.Now()); err == nil {
		t.Fatal("period over 31 days accepted")
	}
	from, to := time.Now().Add(-time.Hour), time.Now().Add(time.Minute)
	rep, err := e.svc.ReconcileYooKassa(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checked != 2 || len(rep.Recovered) != 1 || len(rep.Unknown) != 1 || len(rep.Problems) != 0 {
		t.Fatalf("report: %+v", rep)
	}
	p, _ = store.GetPayment(ctx, e.pool, p.ID)
	if p.Status != "succeeded" || p.AppliedAt == nil || p.SubscriptionID == nil {
		t.Fatalf("payment after reconcile: %s applied=%v", p.Status, p.AppliedAt)
	}
	if !hasAdmin(e, "payment_recovered") {
		t.Fatalf("no admin event: %v", e.notes.admin)
	}

	// Running again, or a late webhook, grants nothing twice.
	rep, _ = e.svc.ReconcileYooKassa(ctx, from, to)
	notify(t, e, *p.ProviderRef)
	subs, _ := store.ListSubscriptions(ctx, e.pool, c.ID)
	if len(rep.Recovered) != 0 || len(subs) != 1 {
		t.Fatalf("second run recovered %d, subscriptions %d", len(rep.Recovered), len(subs))
	}
}

func TestReconcileRejectsShortPayment(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Бобр", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 29900}}})
	c := login(t, e, 4002, "")
	res, _ := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"})
	p, _ := store.GetPayment(ctx, e.pool, res.PaymentID)
	e.gw.succeed(*p.ProviderRef, "sbp")
	e.gw.payments[*p.ProviderRef].Amount = yookassa.Kopecks(100)

	rep, err := e.svc.ReconcileYooKassa(ctx, time.Now().Add(-time.Hour), time.Now().Add(time.Minute))
	if err != nil || len(rep.Problems) != 1 || len(rep.Recovered) != 0 {
		t.Fatalf("report %+v, %v", rep, err)
	}
	// A payment in another currency is not accepted either, even through the webhook.
	e.gw.payments[*p.ProviderRef].Amount = yookassa.Amount{Value: "299.00", Currency: "USD"}
	notify(t, e, *p.ProviderRef)
	p, _ = store.GetPayment(ctx, e.pool, p.ID)
	if p.Status != "pending" || p.AppliedAt != nil {
		t.Fatalf("short/foreign payment applied: %s", p.Status)
	}
}

func TestRefundsAreIdempotentAndPartialAware(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Бобр", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 30000}}})
	c := login(t, e, 4003, "")
	res, _ := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"})
	p := confirm(t, e, res)
	ref := *p.ProviderRef
	refund := func(id string, amount, total int64) {
		t.Helper()
		e.gw.refunds[id] = &yookassa.Refund{ID: id, PaymentID: ref, Status: "succeeded", Amount: yookassa.Kopecks(amount)}
		tot := yookassa.Kopecks(total)
		e.gw.payments[ref].RefundedAmount = &tot
		var n yookassa.Notification
		n.Event, n.Object.ID = "refund.succeeded", id
		if err := e.svc.HandleYooKassaNotification(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	// A partial refund keeps the payment and its receipt.
	refund("rf-a", 10000, 10000)
	p, _ = store.GetPayment(ctx, e.pool, p.ID)
	if p.Status != "succeeded" || p.ReceiptStatus == "cancel_pending" {
		t.Fatalf("partial refund: %s %s", p.Status, p.ReceiptStatus)
	}
	// The second refund completes the amount; repeated notifications change nothing.
	refund("rf-b", 20000, 30000)
	refund("rf-b", 20000, 30000)
	refund("rf-a", 10000, 30000)
	p, _ = store.GetPayment(ctx, e.pool, p.ID)
	if p.Status != "refunded" {
		t.Fatalf("full refund: %s", p.Status)
	}
	n := 0
	for _, a := range e.notes.admin {
		if strings.HasPrefix(a, "refund:") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("refund events = %d, want 2: %v", n, e.notes.admin)
	}
}

func hasAdmin(e *env, key string) bool {
	e.notes.mu.Lock()
	defer e.notes.mu.Unlock()
	for _, a := range e.notes.admin {
		if strings.HasPrefix(a, key+":") {
			return true
		}
	}
	return false
}

func TestRecheckCanceledPayment(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Бобр", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 29900}}})
	c := login(t, e, 4004, "")
	res, _ := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"})
	p, _ := store.GetPayment(ctx, e.pool, res.PaymentID)
	if _, err := e.pool.Exec(ctx, `UPDATE payments SET status='canceled', cancel_reason='expired' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	// Still unpaid at YooKassa: stays canceled.
	if p, _ = e.svc.RecheckPayment(ctx, p.ID); p.Status != "canceled" {
		t.Fatalf("unpaid recheck: %s", p.Status)
	}
	e.gw.succeed(*p.ProviderRef, "sbp")
	if p, _ = e.svc.RecheckPayment(ctx, p.ID); p.Status != "succeeded" || p.AppliedAt == nil {
		t.Fatalf("paid recheck: %s applied=%v", p.Status, p.AppliedAt)
	}
}
