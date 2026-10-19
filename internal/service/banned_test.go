package service_test

import (
	"context"
	"testing"

	"bshop/internal/service"
)

// A Telegram ID from general.blocked_telegram_ids must not get a session in the web cabinet either,
// and an existing session stops working once the ID is added to the list.
func TestBlockedTelegramIDCannotLogin(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sess, err := e.svc.LoginTelegramWebApp(ctx, initData(t, `{"id":4242,"first_name":"b"}`, ""), service.LoginMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if e.svc.Banned(sess.Customer) {
		t.Fatal("not in the list yet")
	}
	mustSet(t, e, "general", `{"blocked_telegram_ids":[4242]}`)
	c, err := e.svc.AuthenticateToken(ctx, sess.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !e.svc.Banned(c) {
		t.Fatal("blocked ID must be banned for an existing session")
	}
	if _, err := e.svc.LoginTelegramWebApp(ctx, initData(t, `{"id":4242,"first_name":"b"}`, ""), service.LoginMeta{}); err == nil {
		t.Fatal("blocked ID logged in")
	}
}
