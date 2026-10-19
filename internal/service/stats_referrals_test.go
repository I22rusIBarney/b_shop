package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"bshop/internal/service"
	"bshop/internal/store"
	"bshop/internal/yookassa"
)

func refundForStats(t *testing.T, e *env, p *store.Payment, id string, amount, total int64) {
	t.Helper()
	e.gw.mu.Lock()
	e.gw.refunds[id] = &yookassa.Refund{ID: id, PaymentID: *p.ProviderRef, Status: "succeeded", Amount: yookassa.Kopecks(amount)}
	refunded := yookassa.Kopecks(total)
	e.gw.payments[*p.ProviderRef].RefundedAmount = &refunded
	e.gw.mu.Unlock()
	var n yookassa.Notification
	n.Event, n.Object.ID = "refund.succeeded", id
	if err := e.svc.HandleYooKassaNotification(context.Background(), n); err != nil {
		t.Fatal(err)
	}
}

func pointSum(points []service.Point, column int) int64 {
	var total int64
	for _, p := range points {
		total += p.V[column]
	}
	return total
}

func TestReferralStatsRefundInSamePeriod(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mustSet(t, e, "referral", `{"enabled":true,"base_enabled":true,"inviter_days":3,"invitee_days":2}`)
	plan := createPlan(t, e, store.Plan{Name: "Бобр", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 31000}}})
	inviter := login(t, e, 6501, "")
	first := login(t, e, 6502, "ref_"+*inviter.RefCode)
	second := login(t, e, 6503, "ref_"+*inviter.RefCode)
	refunded := buyPaid(t, e, first, plan, 0, 0)
	buyPaid(t, e, second, plan, 0, 0)

	refundForStats(t, e, refunded, "rf-stats-partial", 10000, 10000)
	before, err := e.svc.StatsReferrals(ctx, "7d", "day")
	if err != nil || before.BonusDays != 10 || before.RevokedDays != 0 || before.UnrevokedDays != 10 {
		t.Fatalf("partial refund stats %+v, %v", before, err)
	}
	refundForStats(t, e, refunded, "rf-stats-full", 21000, 31000)
	r, err := e.svc.StatsReferrals(ctx, "7d", "day")
	if err != nil {
		t.Fatal(err)
	}
	if r.BonusDays != 10 || r.RevokedDays != 3 || r.UnrevokedDays != 7 || r.Revenue != 31000 {
		t.Fatalf("period totals: granted=%d revoked=%d unrevoked=%d revenue=%d", r.BonusDays, r.RevokedDays, r.UnrevokedDays, r.Revenue)
	}
	if r.DayPrice != 1000 || r.BonusCost != 10000 || r.UnrevokedCost != 7000 || r.ROI != 210 || r.UnrevokedROI != 342.9 {
		t.Fatalf("estimated costs/ROI: day=%d gross=%d unrevoked=%d roi=%.1f unrevoked_roi=%.1f",
			r.DayPrice, r.BonusCost, r.UnrevokedCost, r.ROI, r.UnrevokedROI)
	}
	if pointSum(r.Bonuses, 0) != 6 || pointSum(r.Bonuses, 2) != 4 ||
		pointSum(r.Unrevoked, 0) != 3 || pointSum(r.Unrevoked, 2) != 4 || pointSum(r.Revocations, 0) != 3 {
		t.Fatalf("bonus/unrevoked/revocation series: %+v / %+v / %+v", r.Bonuses, r.Unrevoked, r.Revocations)
	}
	gross, unrevoked := map[string]int64{}, map[string]int64{}
	for _, row := range r.ByKind {
		gross[row.Key] = row.Sum
	}
	for _, row := range r.ByKindUnrevoked {
		unrevoked[row.Key] = row.Sum
	}
	if gross["first"] != 6 || gross["invitee"] != 4 || unrevoked["first"] != 3 || unrevoked["invitee"] != 4 {
		t.Fatalf("by kind: gross=%v unrevoked=%v", gross, unrevoked)
	}
	if len(r.Top) != 1 || r.Top[0].BonusDays != 6 || r.Top[0].RevokedDays != 3 || r.Top[0].UnrevokedDays != 3 {
		t.Fatalf("top inviters: %+v", r.Top)
	}
	if len(r.Recent) != 4 || len(r.RecentRevocations) != 1 || r.RecentRevocations[0].Days != 3 {
		t.Fatalf("recent grants/revocations: %+v / %+v", r.Recent, r.RecentRevocations)
	}
	marked := 0
	for _, event := range r.Recent {
		if event.RevokedAt != nil {
			marked++
		}
	}
	if marked != 1 {
		t.Fatalf("marked grants: %d, want 1", marked)
	}
	ov, err := e.svc.StatsOverview(ctx, "7d", "")
	if err != nil || ov.Referrals.BonusDays != 10 || ov.Referrals.RevokedDays != 3 || ov.Referrals.UnrevokedDays != 7 {
		t.Fatalf("overview referrals: %+v, %v", ov.Referrals, err)
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"bonus_days", "bonus_cost", "roi", "bonuses", "by_kind", "recent",
		"revoked_days", "unrevoked_days", "unrevoked_cost", "unrevoked_roi", "unrevoked", "revocations", "by_kind_unrevoked", "recent_revocations"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("missing API field %s", key)
		}
	}
}

func TestReferralStatsRefundFromEarlierPeriod(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mustSet(t, e, "referral", `{"enabled":true,"base_enabled":true,"inviter_days":3,"invitee_days":2}`)
	plan := createPlan(t, e, store.Plan{Name: "Бобр", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 31000}}})
	inviter := login(t, e, 6511, "")
	oldFriend := login(t, e, 6512, "ref_"+*inviter.RefCode)
	newFriend := login(t, e, 6513, "ref_"+*inviter.RefCode)
	old := buyPaid(t, e, oldFriend, plan, 0, 0)
	if _, err := e.pool.Exec(ctx, `UPDATE referral_bonuses SET created_at=now()-interval '20 days' WHERE payment_id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	buyPaid(t, e, newFriend, plan, 0, 0)
	refundForStats(t, e, old, "rf-stats-old", 31000, 31000)

	week, err := e.svc.StatsReferrals(ctx, "7d", "day")
	if err != nil {
		t.Fatal(err)
	}
	if week.BonusDays != 5 || week.RevokedDays != 3 || week.UnrevokedDays != 5 {
		t.Fatalf("this week: granted=%d revoked=%d unrevoked=%d", week.BonusDays, week.RevokedDays, week.UnrevokedDays)
	}
	if pointSum(week.Bonuses, 0) != 3 || pointSum(week.Bonuses, 2) != 2 ||
		pointSum(week.Unrevoked, 0) != 3 || pointSum(week.Unrevoked, 2) != 2 || pointSum(week.Revocations, 0) != 3 {
		t.Fatalf("this week's series: %+v / %+v / %+v", week.Bonuses, week.Unrevoked, week.Revocations)
	}
	if len(week.RecentRevocations) != 1 || week.RecentRevocations[0].Days != 3 {
		t.Fatalf("refund from earlier grant missing from recent events: %+v", week.RecentRevocations)
	}
	quarter, err := e.svc.StatsReferrals(ctx, "3m", "month")
	if err != nil {
		t.Fatal(err)
	}
	if quarter.BonusDays != 10 || quarter.RevokedDays != 3 || quarter.UnrevokedDays != 7 {
		t.Fatalf("three months: granted=%d revoked=%d unrevoked=%d", quarter.BonusDays, quarter.RevokedDays, quarter.UnrevokedDays)
	}
	if pointSum(quarter.Bonuses, 0) != 6 || pointSum(quarter.Unrevoked, 0) != 3 || pointSum(quarter.Revocations, 0) != 3 {
		t.Fatalf("three months series: %+v / %+v / %+v", quarter.Bonuses, quarter.Unrevoked, quarter.Revocations)
	}
}
