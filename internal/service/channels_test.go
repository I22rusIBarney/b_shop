package service_test

import (
	"context"
	"errors"
	"testing"
)

type fakeChannels struct {
	members map[string]bool // chat → is member
	fail    bool
	calls   int
}

func (f *fakeChannels) IsChannelMember(_ context.Context, chat string, _ int64) (bool, error) {
	f.calls++
	if f.fail {
		return false, errors.New("bot is not an admin")
	}
	return f.members[chat], nil
}

func TestRequiredChannels(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	fc := &fakeChannels{members: map[string]bool{}}
	e.svc.Channels = fc
	c := login(t, e, 1201, "")
	admin := login(t, e, 1, "")

	if m := e.svc.MissingChannels(ctx, c); len(m) != 0 {
		t.Fatal("gate active while disabled")
	}
	if err := e.svc.Settings.Update(ctx, "channels", []byte(`{"enabled":true,"items":[]}`)); err == nil {
		t.Fatal("enabled without channels")
	}
	mustSet(t, e, "channels", `{"enabled":true,"items":[{"chat_id":"@bobrogo","title":"BobrGO"}]}`)
	m := e.svc.MissingChannels(ctx, c)
	if len(m) != 1 || m[0].URL != "https://t.me/bobrogo" {
		t.Fatalf("missing = %+v", m)
	}
	if len(e.svc.MissingChannels(ctx, admin)) != 0 {
		t.Fatal("admin blocked")
	}
	// After subscribing the gate opens and the result is cached.
	fc.members["@bobrogo"] = true
	if len(e.svc.MissingChannels(ctx, c)) != 0 {
		t.Fatal("still blocked after subscribing")
	}
	calls := fc.calls
	e.svc.MissingChannels(ctx, c)
	if fc.calls != calls {
		t.Fatal("confirmed subscription not cached")
	}
	// Telegram errors (bot not admin) never lock customers out.
	other := login(t, e, 1202, "")
	fc.fail = true
	if len(e.svc.MissingChannels(ctx, other)) != 0 {
		t.Fatal("customer locked out by a channel error")
	}
}
