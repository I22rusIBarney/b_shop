package service_test

import (
	"context"
	"testing"

	"bshop/internal/store"
)

// TestStats runs every report on a small shop: two customers, one with three paid months
// spread back in time (so MRR, churn and cohorts have history), one referred.
func TestStats(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Бобрёнок", IsActive: true, DeviceLimit: 3,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 15000}, {Unit: "month", Amount: 3, Price: 40000}}})
	a := login(t, e, 701, "")
	p1 := buyPaid(t, e, a, plan, 0, 0)
	p2 := buyPaid(t, e, a, plan, 0, *p1.SubscriptionID)
	b := login(t, e, 702, "ref_"+*a.RefCode)
	buyPaid(t, e, b, plan, 1, 0)
	// Move a's payments into the past: three and two months ago, so a churned last month.
	if _, err := e.pool.Exec(ctx, `UPDATE payments SET paid_at = now() - interval '95 days', created_at = now() - interval '95 days' WHERE id=$1`, p1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE payments SET paid_at = now() - interval '64 days', created_at = now() - interval '64 days' WHERE id=$1`, p2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE customers SET created_at = now() - interval '100 days' WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}

	for _, period := range []string{"7d", "1m", "3m", "12m", "all"} {
		ov, err := e.svc.StatsOverview(ctx, period, "")
		if err != nil {
			t.Fatalf("overview %s: %v", period, err)
		}
		if len(ov.NewUsers) == 0 || len(ov.Segments) != 6 {
			t.Fatalf("overview %s: empty series/segments", period)
		}
		if _, err := e.svc.StatsPayments(ctx, period, "week"); err != nil {
			t.Fatalf("payments %s: %v", period, err)
		}
		if _, err := e.svc.StatsReferrals(ctx, period, ""); err != nil {
			t.Fatalf("referrals %s: %v", period, err)
		}
		if _, err := e.svc.StatsSupport(ctx, period, ""); err != nil {
			t.Fatalf("support %s: %v", period, err)
		}
		if _, err := e.svc.StatsAnalytics(ctx, period, "month"); err != nil {
			t.Fatalf("analytics %s: %v", period, err)
		}
	}
	ov, _ := e.svc.StatsOverview(ctx, "all", "")
	if ov.Revenue.AllTime != 15000*2+40000 || ov.Referrals.Referred != 1 || ov.Referrals.Paid != 1 {
		t.Fatalf("overview totals: %+v %+v", ov.Revenue, ov.Referrals)
	}

	ec, err := e.svc.StatsEconomics(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	last := ec.Months[len(ec.Months)-1]
	// Current month: b pays 40000 for 3 months → 13333/month; a is gone.
	if last.MRR < 13300 || last.MRR > 13400 || last.Active != 1 {
		t.Fatalf("current MRR: %+v", last)
	}
	var newSum, churned int64
	for _, m := range ec.Months {
		newSum += m.New
		churned += m.Churned
	}
	if churned < 1 || newSum < 15000+13300 {
		t.Fatalf("components: new=%d churned=%d %+v", newSum, churned, ec.Months)
	}
	if len(ec.Cohorts) == 0 || ec.LTV.Paying != 2 {
		t.Fatalf("cohorts/LTV: %+v %+v", ec.Cohorts, ec.LTV)
	}

	if _, err := e.svc.RecomputeLeads(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StatsLeads(ctx, "", "", 50, 0); err != nil {
		t.Fatal(err)
	}
}
