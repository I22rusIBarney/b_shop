package service_test

import (
	"context"
	"testing"

	"bshop/internal/service"
	"bshop/internal/store"
)

func TestPlanChangePenaltyAndLimit(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mid := createPlan(t, e, store.Plan{Name: "Бобрёнок", IsActive: true, DeviceLimit: 3,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 31000}}})
	top := createPlan(t, e, store.Plan{Name: "Максимум", IsActive: true, DeviceLimit: 12,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 62000}}})
	low := createPlan(t, e, store.Plan{Name: "AI", IsActive: true, DeviceLimit: 2,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 15500}}})
	c := login(t, e, 3101, "")
	subID := *buyPaid(t, e, c, mid, 0, 0).SubscriptionID

	// 10% penalty: 31 days of 310 ₽/mo are worth 279 ₽/mo.
	// Upgrade to 620: (620 − 279) ₽ for the month = 341 ₽; convert: 31·279/620 = 13 days. Downgrade to 155: 55 days.
	mustSet(t, e, "general", `{"timezone":"Asia/Barnaul","days_in_month":31,"plan_change_penalty":10,"plan_changes_per_day":1}`)
	info, err := e.svc.ChangeOptions(ctx, c, subID)
	if err != nil || info.Penalty != 10 || info.Blocked != "" {
		t.Fatalf("info = %+v, %v", info, err)
	}
	for _, x := range info.Targets {
		switch x.PlanID {
		case top.ID:
			if x.Quotes[0].Amount != 34100 || x.Quotes[1].DaysAfter != 13 {
				t.Fatalf("upgrade with penalty %+v", x.Quotes)
			}
		case low.ID:
			if x.Quotes[0].DaysAfter != 55 {
				t.Fatalf("downgrade with penalty %+v", x.Quotes)
			}
		}
	}

	// One change a day: the second is refused and the options say why.
	if _, err := e.svc.ChangePlan(ctx, c, subID, service.ChangeRequest{PlanID: low.ID, Mode: "downgrade"}); err != nil {
		t.Fatal(err)
	}
	info, _ = e.svc.ChangeOptions(ctx, c, subID)
	if info.Blocked == "" {
		t.Fatal("limit not reported")
	}
	if _, err := e.svc.ChangePlan(ctx, c, subID, service.ChangeRequest{PlanID: mid.ID, Mode: "upgrade", Method: "sbp"}); err == nil {
		t.Fatal("second change in a day allowed")
	}
}
