package service_test

import (
	"context"
	"testing"

	"bshop/internal/service"
	"bshop/internal/store"
)

func TestPlanNoticeMustBeAcknowledged(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "AI", IsActive: true, DeviceLimit: 3, Notice: "Без обхода белых списков",
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 9900}}})
	c := login(t, e, 1301, "")
	req := service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"}
	if _, err := e.svc.Buy(ctx, c, req); err == nil {
		t.Fatal("bought without acknowledging the notice")
	}
	req.NoticeAck = true
	if _, err := e.svc.Buy(ctx, c, req); err != nil {
		t.Fatal(err)
	}
	mustSet(t, e, "gifts", `{"enabled":true}`)
	if _, err := e.svc.BuyGift(ctx, c, service.GiftRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"}); err == nil {
		t.Fatal("gift bought without acknowledging the notice")
	}
}
