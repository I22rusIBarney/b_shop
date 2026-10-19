package service_test

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"bshop/internal/service"
	"bshop/internal/store"

	"github.com/google/uuid"
)

func adminNotesOf(e *env, event string) []service.AdminNote {
	e.notes.mu.Lock()
	defer e.notes.mu.Unlock()
	var out []service.AdminNote
	for _, n := range e.notes.adminNotes {
		if n.Event == event {
			out = append(out, n)
		}
	}
	return out
}

var leftover = regexp.MustCompile(`\{[a-z_]+\}`)

// The purchase notification follows the old bot: user line with the Telegram profile link, plan, devices,
// amount, provider, expiry and event time; it is sent once per payment and offers the profile buttons.
func TestPurchaseNotificationLayout(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	plan := createPlan(t, e, store.Plan{Name: "Бобр AI", IsActive: true, DeviceLimit: 3, RWTag: "ai",
		RWInternalSquads: []uuid.UUID{uuid.New()}, Periods: []store.Period{{Unit: "month", Amount: 1, Price: 9900}}})
	c := login(t, e, 787975776, "")
	if _, err := e.svc.Buy(ctx, c, service.BuyRequest{PlanID: plan.ID, PeriodID: plan.Periods[0].ID, Method: "sbp"}); err != nil {
		t.Fatal(err)
	}
	e.gw.succeed("yk-1", "sbp")
	notify(t, e, "yk-1")
	notify(t, e, "yk-1") // duplicate webhook: no second notification

	ns := adminNotesOf(e, "purchase")
	if len(ns) != 1 {
		t.Fatalf("expected one purchase note, got %d", len(ns))
	}
	n := ns[0]
	for _, want := range []string{"🛒 <b>Покупка</b>", `<a href="tg://user?id=787975776">787975776</a> (Тест) @user787975776`,
		"📦 Тариф: Бобр AI (1 месяц)", "📱 Устройства: 3", "💰 Сумма: 99 ₽", "💎 Провайдер: ЮKassa", "🗓 Действует до: ", "🕐 Время события: "} {
		if !strings.Contains(n.Text, want) {
			t.Errorf("missing %q in:\n%s", want, n.Text)
		}
	}
	if leftover.MatchString(n.Text) {
		t.Errorf("unfilled variable in:\n%s", n.Text)
	}
	if n.CustomerID != c.ID || n.ButtonText != "Открыть профиль" || n.ButtonPath != "/admin/customers/"+strconv.FormatInt(c.ID, 10) {
		t.Errorf("recipient/button %+v", n)
	}
}

// Names come from users: they are escaped, so a name cannot inject markup or links into the admin chat.
func TestNotificationEscapesUserData(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c := login(t, e, 5005, "")
	evil := `<a href="https://evil">x</a>`
	if _, err := e.pool.Exec(ctx, `UPDATE customers SET first_name=$2 WHERE id=$1`, c.ID, evil); err != nil {
		t.Fatal(err)
	}
	c, _ = store.GetCustomer(ctx, e.pool, c.ID)
	if err := e.svc.TestAdminEvent(ctx, "promo", c); err != nil {
		t.Fatal(err)
	}
	n := adminNotesOf(e, "promo")
	if len(n) != 1 || strings.Contains(n[0].Text, `<a href="https://evil"`) || !strings.Contains(n[0].Text, "&lt;a href=") {
		t.Fatalf("name not escaped: %v", n)
	}
}

// Every catalog template renders without leftovers for its sample data.
func TestAllEventTemplatesRender(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	admin := login(t, e, 1, "")
	for _, ev := range service.AdminEvents {
		if err := e.svc.TestAdminEvent(ctx, ev.Key, admin); err != nil {
			t.Fatalf("%s: %v", ev.Key, err)
		}
		ns := adminNotesOf(e, ev.Key)
		if len(ns) == 0 {
			t.Fatalf("%s: nothing sent", ev.Key)
		}
		txt := ns[len(ns)-1].Text
		if leftover.MatchString(txt) {
			t.Errorf("%s: unfilled variable in %q", ev.Key, txt)
		}
		if bad := badTelegramHTML(txt); bad != "" {
			t.Errorf("%s: Telegram would reject the message (%s): %q", ev.Key, bad, txt)
		}
	}
}

var tagRe = regexp.MustCompile(`<(/?)([^\s>/]*)[^>]*>`)

// badTelegramHTML reports a tag Telegram's HTML parse mode does not know (it rejects the whole message)
// or a stray "<" that is not a tag.
func badTelegramHTML(s string) string {
	allowed := map[string]bool{"b": true, "i": true, "u": true, "s": true, "a": true, "code": true, "pre": true, "blockquote": true, "tg-spoiler": true}
	for _, m := range tagRe.FindAllStringSubmatch(s, -1) {
		if !allowed[m[2]] {
			return "tag " + m[0]
		}
	}
	if strings.Count(s, "<") != len(tagRe.FindAllString(s, -1)) {
		return "stray <"
	}
	return ""
}

// A disabled event is not sent.
func TestDisabledEventIsSilent(t *testing.T) {
	e := setup(t)
	mustSet(t, e, "notifications", `{"events":{"trial":{"disabled":true}}}`)
	admin := login(t, e, 1, "")
	if err := e.svc.TestAdminEvent(context.Background(), "trial", admin); err == nil {
		t.Fatal("test of a disabled event must say it is off")
	}
	if n := adminNotesOf(e, "trial"); len(n) != 0 {
		t.Fatalf("disabled event sent: %v", n)
	}
}

// An external API error that quotes a secret (here the ЮKassa key) is cleaned before it reaches the admin chat.
func TestEventStripsSecrets(t *testing.T) {
	e := setup(t) // yookassa secret_key is "live_secret"
	ctx := context.Background()
	admin := login(t, e, 1, "")
	if err := e.svc.Settings.Update(ctx, "yookassa", []byte(`{"enabled":true,"shop_id":"1","secret_key":"live_secret_value_123","methods":["sbp"]}`)); err != nil {
		t.Fatal(err)
	}
	service.EmitEventForTest(e.svc, ctx, "payment_error", admin, map[string]any{"error": "401 for key live_secret_value_123"})
	ns := adminNotesOf(e, "payment_error")
	if txt := ns[len(ns)-1].Text; strings.Contains(txt, "live_secret_value_123") || !strings.Contains(txt, "•••") {
		t.Fatalf("secret in admin chat: %s", txt)
	}
}
