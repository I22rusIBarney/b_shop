package service_test

import (
	"context"
	"testing"
	"time"

	"bshop/internal/store"
)

func appliedReferralPayment(t *testing.T, e *env, c *store.Customer, operation, provider string, amount int64) *store.Payment {
	t.Helper()
	ctx := context.Background()
	p := &store.Payment{CustomerID: c.ID, Operation: operation, Title: "Бобр", Days: 31,
		Amount: amount, BaseAmount: amount, Provider: provider}
	if err := store.InsertPayment(ctx, e.pool, p); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkPaid(ctx, e.pool, p.ID, "sbp"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkApplied(ctx, e.pool, p.ID, nil); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMarkAppliedUsesExecutionTime(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c := login(t, e, 6449, "")
	p := &store.Payment{CustomerID: c.ID, Operation: "buy", Title: "Бобр", Amount: 31000, Provider: "yookassa"}
	if err := store.InsertPayment(ctx, e.pool, p); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkPaid(ctx, e.pool, p.ID, "sbp"); err != nil {
		t.Fatal(err)
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var started time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&started); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := store.MarkApplied(ctx, tx, p.ID, nil); err != nil {
		t.Fatal(err)
	}
	var applied time.Time
	if err := tx.QueryRow(ctx, `SELECT applied_at FROM payments WHERE id=$1`, p.ID).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied.Sub(started) < 10*time.Millisecond {
		t.Fatalf("applied_at %v is the start of the transaction, not the application time %v", applied, started)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestReferralFirstPaidAutopay(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mustSet(t, e, "referral", `{"enabled":true,"base_enabled":true,"inviter_days":5,"invitee_days":0}`)
	inviter := login(t, e, 6450, "")
	friend := login(t, e, 6454, "ref_"+*inviter.RefCode)
	p := appliedReferralPayment(t, e, friend, "autopay", "yookassa", 31000)
	if err := e.svc.ProcessReferral(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	bonuses, err := store.ListReferralBonuses(ctx, e.pool, inviter.ID, 10)
	if err != nil || len(bonuses) != 1 || bonuses[0].Kind != "first" || bonuses[0].Days != 5 {
		t.Fatalf("first paid autopay bonuses %+v, %v", bonuses, err)
	}
}

func TestReferralTierIgnoresGiftAndFreePayments(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mustSet(t, e, "referral", `{"enabled":true,"base_enabled":true,"inviter_days":3,"invitee_days":0,
		"tiers_enabled":true,"tiers":[{"min_invited":2,"days":7}]}`)
	inviter := login(t, e, 6451, "")
	giftFriend := login(t, e, 6452, "ref_"+*inviter.RefCode)
	payingFriend := login(t, e, 6453, "ref_"+*inviter.RefCode)
	for _, p := range []*store.Payment{
		appliedReferralPayment(t, e, giftFriend, "gift", "yookassa", 31000),
		appliedReferralPayment(t, e, giftFriend, "buy", "free", 0),
	} {
		if err := e.svc.ProcessReferral(ctx, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	paid := appliedReferralPayment(t, e, payingFriend, "buy", "yookassa", 31000)
	if err := e.svc.ProcessReferral(ctx, paid.ID); err != nil {
		t.Fatal(err)
	}
	bonuses, err := store.ListReferralBonuses(ctx, e.pool, inviter.ID, 10)
	if err != nil || len(bonuses) != 1 || bonuses[0].Kind != "first" || bonuses[0].Days != 3 {
		t.Fatalf("gift/free payment raised tier: %+v, %v", bonuses, err)
	}
}

func TestReferralTierOrderWhenProcessedBackwards(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mustSet(t, e, "referral", `{"enabled":true,"base_enabled":true,"inviter_days":3,"invitee_days":0,
		"tiers_enabled":true,"tiers":[{"min_invited":2,"days":7}]}`)
	inviter := login(t, e, 6461, "")
	first := login(t, e, 6462, "ref_"+*inviter.RefCode)
	second := login(t, e, 6463, "ref_"+*inviter.RefCode)
	p1 := appliedReferralPayment(t, e, first, "buy", "yookassa", 31000)
	p2 := appliedReferralPayment(t, e, second, "renew", "yookassa", 31000)
	base := time.Now().UTC()
	if _, err := e.pool.Exec(ctx, `UPDATE payments SET applied_at=$2 WHERE id=$1`, p1.ID, base.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE payments SET applied_at=$2 WHERE id=$1`, p2.ID, base); err != nil {
		t.Fatal(err)
	}
	for _, payment := range []*store.Payment{p2, p1} {
		if err := e.svc.ProcessReferral(ctx, payment.ID); err != nil {
			t.Fatal(err)
		}
	}
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
		t.Fatalf("tier bonuses after reverse processing: %v, want [3 7]", awarded)
	}
}
