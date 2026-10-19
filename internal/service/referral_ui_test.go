package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"bshop/internal/service"
	"bshop/internal/store"
	"bshop/internal/yookassa"

	"github.com/google/uuid"
)

func TestReferralFriendsAndInvitation(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mustSet(t, e, "referral", `{"enabled":true,"base_enabled":true,"inviter_days":3,"invitee_days":3}`)
	plan := createPlan(t, e, store.Plan{Name: "Бобр", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 30000}}})
	inviter := login(t, e, 6001, "")
	friend := login(t, e, 6002, "ref_"+*inviter.RefCode)
	login(t, e, 6003, "ref_"+*inviter.RefCode) // joined, never paid
	last := "Константинопольская"
	if _, err := e.pool.Exec(ctx, `UPDATE customers SET last_name=$2 WHERE id=$1`, friend.ID, last); err != nil {
		t.Fatal(err)
	}
	friend, _ = store.GetCustomer(ctx, e.pool, friend.ID)

	// Before the first payment the friend sees who invited them and the days they will get.
	inv := e.svc.Invitation(ctx, friend)
	if inv == nil || inv.From != "Тест" || inv.Days != 3 {
		t.Fatalf("invitation = %+v", inv)
	}
	if e.svc.Invitation(ctx, inviter) != nil {
		t.Fatal("a customer without a referrer sees an invitation")
	}

	res, err := e.svc.Buy(ctx, friend, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"})
	if err != nil {
		t.Fatal(err)
	}
	confirm(t, e, res)
	if e.svc.Invitation(ctx, friend) != nil {
		t.Fatal("invitation still shown after the first payment")
	}

	info, err := e.svc.Referral(ctx, inviter, "")
	if err != nil {
		t.Fatal(err)
	}
	if info.Stats.Invited != 2 || info.Stats.Paid != 1 || info.Stats.Active != 1 || info.Stats.Days != 3 || len(info.Friends) != 2 {
		t.Fatalf("stats %+v, friends %+v", info.Stats, info.Friends)
	}
	byStatus := map[string]service.ReferralFriend{}
	for _, f := range info.Friends {
		byStatus[f.Status] = f
	}
	if f := byStatus["active"]; f.Name != "Тест К." || f.Days != 3 {
		t.Fatalf("active friend %+v", f)
	}
	if f := byStatus["joined"]; f.Name != "Тест" || f.Days != 0 {
		t.Fatalf("joined friend %+v", f)
	}
	// The history uses the same short name and carries no ids of other customers.
	if len(info.Bonuses) != 1 || info.Bonuses[0].ReferralName != "Тест К." || info.Bonuses[0].ReferralID != nil || info.Bonuses[0].PaymentID != nil {
		t.Fatalf("history %+v", info.Bonuses)
	}
}

func TestReferralBonusRevokedOnFullRefund(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mustSet(t, e, "referral", `{"enabled":true,"base_enabled":true,"inviter_days":3,"invitee_days":2,"recurring_enabled":true,"recurring_percent":10}`)
	plan := createPlan(t, e, store.Plan{Name: "Бобр", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 31000}}})
	buy := func(c *store.Customer, subID int64) *store.Payment {
		t.Helper()
		res, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, SubscriptionID: subID, Method: "sbp"})
		if err != nil {
			t.Fatal(err)
		}
		return confirm(t, e, res)
	}
	refund := func(p *store.Payment, id string) {
		t.Helper()
		amt := yookassa.Kopecks(p.Amount)
		e.gw.payments[*p.ProviderRef].RefundedAmount = &amt
		e.gw.refunds[id] = &yookassa.Refund{ID: id, PaymentID: *p.ProviderRef, Status: "succeeded", Amount: amt}
		var n yookassa.Notification
		n.Event, n.Object.ID = "refund.succeeded", id
		if err := e.svc.HandleYooKassaNotification(ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	// The inviter has no subscription: the first bonus waits on the balance.
	inviter := login(t, e, 6101, "")
	friend := login(t, e, 6102, "ref_"+*inviter.RefCode)
	first := buy(friend, 0)
	inviter, _ = store.GetCustomer(ctx, e.pool, inviter.ID)
	if inviter.BonusDays != 3 {
		t.Fatalf("balance %d, want 3", inviter.BonusDays)
	}
	// A full refund takes it back once, even if YooKassa repeats the notification.
	refund(first, "rf-1")
	refund(first, "rf-1")
	inviter, _ = store.GetCustomer(ctx, e.pool, inviter.ID)
	info, _ := e.svc.Referral(ctx, inviter, "")
	if inviter.BonusDays != 0 || info.Stats.Days != 0 || info.Bonuses[0].RevokedAt == nil {
		t.Fatalf("after refund: balance %d, stats %+v, history %+v", inviter.BonusDays, info.Stats, info.Bonuses)
	}
	// The friend's own bonus stays with their purchase.
	var invitee int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM referral_bonuses WHERE recipient_id=$1 AND revoked_at IS NULL`, friend.ID).Scan(&invitee)
	if invitee != 1 {
		t.Fatalf("invitee bonuses left %d, want 1", invitee)
	}

	// With a subscription the days come off it: a renewal bonus (10% of 31 = 3 days) is taken back from its expiry.
	own := buy(inviter, 0)
	sub, _ := store.GetSubscription(ctx, e.pool, *own.SubscriptionID)
	renewal := buy(friend, *first.SubscriptionID)
	withBonus, _ := store.GetSubscription(ctx, e.pool, sub.ID)
	if got := withBonus.ExpiresAt.Sub(sub.ExpiresAt); got < 71*time.Hour || got > 73*time.Hour {
		t.Fatalf("renewal bonus added %v", got)
	}
	refund(renewal, "rf-2")
	after, _ := store.GetSubscription(ctx, e.pool, sub.ID)
	if d := after.ExpiresAt.Sub(sub.ExpiresAt); d < -time.Minute || d > time.Minute {
		t.Fatalf("expiry after revoke differs by %v", d)
	}
	if !hasCustomerNote(e, "бонус за неё отменён") {
		t.Fatalf("no message to the inviter: %v", e.notes.customer)
	}
}

func hasCustomerNote(e *env, part string) bool {
	e.notes.mu.Lock()
	defer e.notes.mu.Unlock()
	for _, m := range e.notes.customer {
		if strings.Contains(m, part) {
			return true
		}
	}
	return false
}

// A first purchase whose bonus is retried after the friend already renewed still counts as the first one.
func TestReferralRetryKeepsPurchaseOrder(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mustSet(t, e, "referral", `{"enabled":true,"base_enabled":true,"inviter_days":3,"invitee_days":0,"recurring_enabled":true,"recurring_percent":10}`)
	plan := createPlan(t, e, store.Plan{Name: "Бобр", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 31000}}})
	inviter := login(t, e, 6201, "")
	friend := login(t, e, 6202, "ref_"+*inviter.RefCode)
	res, _ := e.svc.Buy(ctx, friend, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"})
	first := confirm(t, e, res)
	res, _ = e.svc.Buy(ctx, friend, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, SubscriptionID: *first.SubscriptionID, Method: "sbp"})
	second := confirm(t, e, res)
	// As if both bonuses had failed: drop them and process the renewal before the first purchase.
	if _, err := e.pool.Exec(ctx, `DELETE FROM referral_bonuses; UPDATE payments SET referral_done=false`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{second.ID, first.ID} {
		if err := e.svc.ProcessReferral(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	kinds := map[uuid.UUID]string{}
	rows, _ := e.pool.Query(ctx, `SELECT payment_id, kind FROM referral_bonuses WHERE recipient_id=$1`, inviter.ID)
	for rows.Next() {
		var id uuid.UUID
		var k string
		_ = rows.Scan(&id, &k)
		kinds[id] = k
	}
	if kinds[first.ID] != "first" || kinds[second.ID] != "recurring" {
		t.Fatalf("bonus kinds %v", kinds)
	}
}
