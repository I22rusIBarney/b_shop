package service_test

import (
	"context"
	"testing"

	"bshop/internal/service"
	"bshop/internal/store"
)

func TestUnlockHiddenPlan(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	open := createPlan(t, e, store.Plan{Name: "Бобрёнок", IsActive: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 29900}}})
	hidden := createPlan(t, e, store.Plan{Name: "Для своих", IsActive: true, IsHidden: true, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 15000}}})
	c := login(t, e, 901, "")
	other := login(t, e, 902, "")

	visible := func(c *store.Customer) map[int64]bool {
		cat, err := e.svc.Catalog(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		m := map[int64]bool{}
		for _, p := range cat {
			m[p.ID] = true
		}
		return m
	}
	if v := visible(c); !v[open.ID] || v[hidden.ID] {
		t.Fatalf("hidden plan shown before unlock: %v", v)
	}
	if _, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: hidden.ID, PeriodID: hidden.Periods[0].ID, Method: "sbp"}); err == nil {
		t.Fatal("hidden plan bought without unlock")
	}

	p := &store.Promo{Code: "FRIENDS", Kind: "unlock", PlanIDs: []int64{hidden.ID}, IsActive: true}
	if err := service.ValidatePromo(p); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePromo(ctx, e.pool, p); err != nil {
		t.Fatal(err)
	}
	if bad := (&store.Promo{Code: "EMPTY", Kind: "unlock", IsActive: true}); service.ValidatePromo(bad) == nil {
		t.Fatal("unlock without plans accepted")
	}
	res, err := e.svc.RedeemPromo(ctx, c, "friends", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "unlock" || len(res.PlanIDs) != 1 {
		t.Fatalf("redeem: %+v", res)
	}
	if v := visible(c); !v[hidden.ID] {
		t.Fatal("unlocked plan not in catalog")
	}
	if v := visible(other); v[hidden.ID] {
		t.Fatal("plan unlocked for everyone")
	}
	buyPaid(t, e, c, hidden, 0, 0)
	if _, err := e.svc.RedeemPromo(ctx, c, "FRIENDS", 0); err == nil {
		t.Fatal("same code twice")
	}
}
