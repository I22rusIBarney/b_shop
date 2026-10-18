package service_test

import (
	"context"
	"testing"

	"bshop/internal/service"
	"bshop/internal/store"
)

func TestBuyWithDeviceSlider(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Модуль", IsActive: true, DeviceLimit: 1,
		ExtraDevicesAllowed: true, ExtraDevicePrice: 5000, MaxExtraDevices: 14,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 20000}}})
	c := login(t, e, 901, "")
	req := service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"}

	req.ExtraDevices = 15
	if _, err := e.svc.Buy(ctx, c, req); err == nil {
		t.Fatal("more than the maximum accepted")
	}
	// 1 + 4 devices for a month: 200 ₽ + 4 × 50 ₽ = 400 ₽; the panel gets a limit of 5.
	req.ExtraDevices = 4
	res, err := e.svc.Buy(ctx, c, req)
	if err != nil || res.Amount != 40000 {
		t.Fatalf("buy = %+v, %v", res, err)
	}
	p := confirm(t, e, res)
	sub, _ := store.GetSubscription(ctx, e.pool, *p.SubscriptionID)
	u, _ := e.panel.GetUser(ctx, *sub.RWUserID)
	if sub.TotalDevices() != 5 || u.HwidDeviceLimit == nil || *u.HwidDeviceLimit != 5 {
		t.Fatalf("devices: sub %d, panel %v", sub.TotalDevices(), u.HwidDeviceLimit)
	}
	// Renewal keeps the 4 extra slots and charges for them; changing the count on renewal is refused.
	req.SubscriptionID, req.ExtraDevices = sub.ID, 2
	if _, err := e.svc.Buy(ctx, c, req); err == nil {
		t.Fatal("device count changed on renewal")
	}
	req.ExtraDevices = 0
	if res, err = e.svc.Buy(ctx, c, req); err != nil || res.Amount != 40000 {
		t.Fatalf("renewal = %+v, %v", res, err)
	}
}
