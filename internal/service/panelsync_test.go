package service_test

import (
	"context"
	"testing"
	"time"

	"bshop/internal/remnawave"
	"bshop/internal/service"
	"bshop/internal/store"
)

func TestPanelSync(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Бобрёнок", IsActive: true, RWTag: "BOBR", DeviceLimit: 3,
		Periods: []store.Period{{Unit: "month", Amount: 1, Price: 29900}}})
	known := login(t, e, 1001, "")
	p := buyPaid(t, e, known, plan, 0, 0)
	// Someone changed the expiry in the panel.
	sub, _ := store.GetSubscription(ctx, e.pool, *p.SubscriptionID)
	newExp := time.Now().Add(90 * 24 * time.Hour).Truncate(time.Second)
	e.panel.mu.Lock()
	e.panel.users[*sub.RWUserID].ExpireAt = newExp
	tag, tg2, tg3, mail := "BOBR", int64(1002), int64(1003), "Web@Mail.ru"
	exp := time.Now().Add(30 * 24 * time.Hour)
	e.panel.users[500] = &remnawave.User{ID: 500, Username: "old_1002", ShortUUID: "s500", TelegramID: &tg2, Tag: &tag, ExpireAt: exp}
	e.panel.users[501] = &remnawave.User{ID: 501, Username: "old_mail", ShortUUID: "s501", Email: &mail, ExpireAt: exp}
	e.panel.users[502] = &remnawave.User{ID: 502, Username: "nobody", ShortUUID: "s502", ExpireAt: exp}
	e.panel.users[503] = &remnawave.User{ID: 503, Username: "second_of_1001", ShortUUID: "s503", TelegramID: &tg3, ExpireAt: exp}
	e.panel.mu.Unlock()
	login(t, e, 1003, "") // an existing customer without a subscription

	st := service.PanelSyncState{}
	if err := e.svc.SyncPanelUsers(ctx, func(f func(*service.PanelSyncState)) { f(&st) }); err != nil {
		t.Fatal(err)
	}
	if st.Total != 5 || st.Updated != 1 || st.Created != 2 || st.Attached != 1 || st.Skipped != 1 || len(st.Errors) != 0 {
		t.Fatalf("state: %+v", st)
	}
	sub, _ = store.GetSubscription(ctx, e.pool, sub.ID)
	if !sub.ExpiresAt.Equal(newExp) {
		t.Fatalf("expiry not refreshed: %v", sub.ExpiresAt)
	}
	s500, err := store.GetSubscriptionByRWUser(ctx, e.pool, 500)
	if err != nil || s500.PlanID == nil || *s500.PlanID != plan.ID || s500.Title != "Бобрёнок" {
		t.Fatalf("tag → plan: %+v %v", s500, err)
	}
	c, err := store.GetCustomerByEmail(ctx, e.pool, "web@mail.ru")
	if err != nil || c.TelegramID != nil {
		t.Fatalf("email customer: %+v %v", c, err)
	}
	// Second run changes nothing new.
	st2 := service.PanelSyncState{}
	_ = e.svc.SyncPanelUsers(ctx, func(f func(*service.PanelSyncState)) { f(&st2) })
	if st2.Updated != 4 || st2.Created != 0 || st2.Attached != 0 {
		t.Fatalf("second run: %+v", st2)
	}
}
