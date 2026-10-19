package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"bshop/internal/service"
	"bshop/internal/store"
	"bshop/internal/yookassa"

	"github.com/google/uuid"
)

func TestReferralFirstPaidRenewal(t *testing.T) {
	for _, kind := range []string{"trial", "transferred"} {
		t.Run(kind, func(t *testing.T) {
			e := setup(t)
			ctx := context.Background()
			mustSet(t, e, "referral", `{"enabled":true,"base_enabled":true,"inviter_days":5,"invitee_days":0,"recurring_enabled":true,"recurring_percent":10}`)
			plan := createPlan(t, e, store.Plan{Name: "Бобр", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 31000}}})
			inviter := login(t, e, 6401, "")
			friend := login(t, e, 6402, "ref_"+*inviter.RefCode)

			var subID int64
			if kind == "trial" {
				trialPlan := createPlan(t, e, store.Plan{Name: "Пробный", IsActive: true, IsTrial: true})
				mustSet(t, e, "trial", fmt.Sprintf(`{"enabled":true,"plan_id":%d,"days":3}`, trialPlan.ID))
				sub, err := e.svc.ActivateTrial(ctx, friend)
				if err != nil {
					t.Fatal(err)
				}
				subID = sub.ID
			} else {
				admin := login(t, e, 1, "")
				sub, err := e.svc.AdminGrant(ctx, admin, friend.ID, plan.ID, 31, "paid")
				if err != nil {
					t.Fatal(err)
				}
				subID = sub.ID
			}
			p := buyPaid(t, e, friend, plan, 0, subID)
			if p.Operation != "renew" {
				t.Fatalf("first paid operation %s, want renew", p.Operation)
			}
			bonuses, err := store.ListReferralBonuses(ctx, e.pool, inviter.ID, 10)
			if err != nil || len(bonuses) != 1 || bonuses[0].Kind != "first" || bonuses[0].Days != 5 {
				t.Fatalf("first paid renewal bonuses %+v, %v", bonuses, err)
			}
		})
	}
}

func TestReferralGiftAndFreeDoNotConsumeFirst(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mustSet(t, e, "referral", `{"enabled":true,"base_enabled":true,"inviter_days":5,"invitee_days":0,"recurring_enabled":true,"recurring_percent":10}`)
	mustSet(t, e, "gifts", `{"enabled":true,"buy_for_other":true}`)
	plan := createPlan(t, e, store.Plan{Name: "Бобр", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 31000}}})
	inviter := login(t, e, 6411, "")
	friend := login(t, e, 6412, "ref_"+*inviter.RefCode)

	gift, err := e.svc.BuyGift(ctx, friend, service.GiftRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"})
	if err != nil {
		t.Fatal(err)
	}
	confirm(t, e, gift)
	discount := 100
	if err := store.PatchCustomer(ctx, e.pool, friend.ID, store.CustomerAdminPatch{PersonalDiscount: &discount}); err != nil {
		t.Fatal(err)
	}
	friend, _ = store.GetCustomer(ctx, e.pool, friend.ID)
	free, err := e.svc.Buy(ctx, friend, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID})
	if err != nil || !free.Applied {
		t.Fatalf("free purchase %+v, %v", free, err)
	}
	if n, err := store.PaidPurchasesBefore(ctx, e.pool, friend.ID, uuid.Nil); err != nil || n != 0 {
		t.Fatalf("qualifying purchases after gift and free payment: %d, %v", n, err)
	}
	discount = 0
	if err := store.PatchCustomer(ctx, e.pool, friend.ID, store.CustomerAdminPatch{PersonalDiscount: &discount}); err != nil {
		t.Fatal(err)
	}
	friend, _ = store.GetCustomer(ctx, e.pool, friend.ID)
	freePay, _ := store.GetPayment(ctx, e.pool, free.PaymentID)
	paid := buyPaid(t, e, friend, plan, 0, *freePay.SubscriptionID)
	if paid.Operation != "renew" {
		t.Fatalf("first paid operation %s, want renew", paid.Operation)
	}
	bonuses, err := store.ListReferralBonuses(ctx, e.pool, inviter.ID, 10)
	if err != nil || len(bonuses) != 1 || bonuses[0].Kind != "first" || bonuses[0].Days != 5 {
		t.Fatalf("bonuses after nonqualifying payments %+v, %v", bonuses, err)
	}
}

type paidBarrier struct {
	*notes
	entered chan struct{}
	release chan struct{}
}

func (b *paidBarrier) Customer(ctx context.Context, c *store.Customer, msg service.Message) {
	if strings.HasPrefix(msg.Text, "✅ Оплата получена!") {
		b.entered <- struct{}{}
		<-b.release
	}
	b.notes.Customer(ctx, c, msg)
}

func paymentWebhook(ctx context.Context, svc *service.Service, ref string, done chan<- error) {
	var n yookassa.Notification
	n.Event, n.Object.ID = "payment.succeeded", ref
	done <- svc.HandleYooKassaNotification(ctx, n)
}

func awaitPaid(t *testing.T, barrier *paidBarrier, done <-chan error) {
	t.Helper()
	select {
	case <-barrier.entered:
	case err := <-done:
		close(barrier.release)
		t.Fatalf("payment failed before notification: %v", err)
	case <-time.After(15 * time.Second):
		close(barrier.release)
		t.Fatal("payment did not reach the post-commit notification")
	}
}

func paidResults(t *testing.T, done <-chan error, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("payment webhook did not finish")
		}
	}
}

func TestReferralClosePaymentsOfOneFriend(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mustSet(t, e, "referral", `{"enabled":true,"base_enabled":true,"inviter_days":5,"invitee_days":0,"recurring_enabled":true,"recurring_percent":10}`)
	plan := createPlan(t, e, store.Plan{Name: "Бобр", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 31000}}})
	inviter := login(t, e, 6421, "")
	friend := login(t, e, 6422, "ref_"+*inviter.RefCode)
	barrier := &paidBarrier{notes: e.notes, entered: make(chan struct{}, 2), release: make(chan struct{})}
	e.svc.Notify = barrier
	done := make(chan error, 2)

	first, err := e.svc.Buy(ctx, friend, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"})
	if err != nil {
		t.Fatal(err)
	}
	p1, _ := store.GetPayment(ctx, e.pool, first.PaymentID)
	e.gw.succeed(*p1.ProviderRef, "sbp")
	go paymentWebhook(ctx, e.svc, *p1.ProviderRef, done)
	awaitPaid(t, barrier, done)
	p1, _ = store.GetPayment(ctx, e.pool, p1.ID)
	second, err := e.svc.Buy(ctx, friend, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, SubscriptionID: *p1.SubscriptionID, Method: "sbp"})
	if err != nil {
		close(barrier.release)
		t.Fatal(err)
	}
	p2, _ := store.GetPayment(ctx, e.pool, second.PaymentID)
	e.gw.succeed(*p2.ProviderRef, "sbp")
	go paymentWebhook(ctx, e.svc, *p2.ProviderRef, done)
	awaitPaid(t, barrier, done)

	// Force the same timestamp: only the UUID can decide which payment is first.
	if _, err := e.pool.Exec(ctx, `UPDATE payments SET applied_at='2026-10-19 12:00:00+00' WHERE id IN ($1,$2)`, p1.ID, p2.ID); err != nil {
		close(barrier.release)
		t.Fatal(err)
	}
	close(barrier.release)
	paidResults(t, done, 2)

	kinds := map[uuid.UUID]string{}
	rows, err := e.pool.Query(ctx, `SELECT payment_id, kind FROM referral_bonuses WHERE recipient_id=$1`, inviter.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		var kind string
		if err := rows.Scan(&id, &kind); err != nil {
			t.Fatal(err)
		}
		kinds[id] = kind
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	firstID, laterID := p1.ID, p2.ID
	if p2.ID.String() < p1.ID.String() {
		firstID, laterID = p2.ID, p1.ID
	}
	if len(kinds) != 2 || kinds[firstID] != "first" || kinds[laterID] != "recurring" {
		t.Fatalf("bonus kinds for simultaneous payments: %v", kinds)
	}
	inviter, _ = store.GetCustomer(ctx, e.pool, inviter.ID)
	if inviter.BonusDays != 8 {
		t.Fatalf("bonus days %d, want 5+3", inviter.BonusDays)
	}
}

func TestReferralConcurrentFriendsAtTier(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mustSet(t, e, "referral", `{"enabled":true,"base_enabled":true,"inviter_days":3,"invitee_days":0,
		"tiers_enabled":true,"tiers":[{"min_invited":2,"days":7}]}`)
	plan := createPlan(t, e, store.Plan{Name: "Бобр", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 31000}}})
	inviter := login(t, e, 6431, "")
	friends := []*store.Customer{login(t, e, 6432, "ref_"+*inviter.RefCode), login(t, e, 6433, "ref_"+*inviter.RefCode)}
	barrier := &paidBarrier{notes: e.notes, entered: make(chan struct{}, 2), release: make(chan struct{})}
	e.svc.Notify = barrier
	done := make(chan error, 2)
	start := make(chan struct{})
	for _, friend := range friends {
		res, err := e.svc.Buy(ctx, friend, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"})
		if err != nil {
			t.Fatal(err)
		}
		p, _ := store.GetPayment(ctx, e.pool, res.PaymentID)
		e.gw.succeed(*p.ProviderRef, "sbp")
		ref := *p.ProviderRef
		go func() {
			<-start
			paymentWebhook(ctx, e.svc, ref, done)
		}()
	}
	close(start)
	awaitPaid(t, barrier, done)
	awaitPaid(t, barrier, done)
	close(barrier.release)
	paidResults(t, done, 2)

	rows, err := e.pool.Query(ctx, `SELECT b.days FROM referral_bonuses b JOIN payments p ON p.id=b.payment_id
		WHERE b.recipient_id=$1 AND b.kind='first' ORDER BY p.applied_at, p.id`, inviter.ID)
	if err != nil {
		t.Fatal(err)
	}
	var awarded []int
	for rows.Next() {
		var days int
		if err := rows.Scan(&days); err != nil {
			t.Fatal(err)
		}
		awarded = append(awarded, days)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(awarded) != 2 || awarded[0] != 3 || awarded[1] != 7 {
		t.Fatalf("tier bonuses in payment order: %v, want [3 7]", awarded)
	}
}

func TestReferralConcurrentRetryOfSamePayment(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mustSet(t, e, "referral", `{"enabled":true,"base_enabled":true,"inviter_days":3,"invitee_days":0}`)
	inviter := login(t, e, 6441, "")
	friend := login(t, e, 6442, "ref_"+*inviter.RefCode)
	p := &store.Payment{CustomerID: friend.ID, Operation: "buy", Title: "Бобр", Days: 31,
		Amount: 31000, BaseAmount: 31000, Provider: "yookassa"}
	if err := store.InsertPayment(ctx, e.pool, p); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkPaid(ctx, e.pool, p.ID, "sbp"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkApplied(ctx, e.pool, p.ID, nil); err != nil {
		t.Fatal(err)
	}
	other := &service.Service{Cfg: e.svc.Cfg, Store: e.svc.Store, Settings: e.svc.Settings,
		Panel: e.panel, Notify: e.notes, Log: e.svc.Log}
	start := make(chan struct{})
	done := make(chan error, 2)
	for _, svc := range []*service.Service{e.svc, other} {
		go func(svc *service.Service) {
			<-start
			done <- svc.ProcessReferral(ctx, p.ID)
		}(svc)
	}
	close(start)
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("referral retry did not finish")
		}
	}
	inviter, _ = store.GetCustomer(ctx, e.pool, inviter.ID)
	bonuses, err := store.ListReferralBonuses(ctx, e.pool, inviter.ID, 10)
	if err != nil || inviter.BonusDays != 3 || len(bonuses) != 1 || bonuses[0].Kind != "first" {
		t.Fatalf("concurrent retry: balance %d, bonuses %+v, err %v", inviter.BonusDays, bonuses, err)
	}
}
