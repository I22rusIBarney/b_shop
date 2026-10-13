package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"bshop/internal/service"
	"bshop/internal/store"
)

func TestAdminNotificationRouting(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mustSet(t, e, "notifications", `{"default_chat_id":-1001,"default_topic_id":0,"events":{
		"new_customer":{"chat_id":-1002,"topic_id":7,"template":"Привет {name} / {via}"},
		"trial":{"disabled":true}}}`)
	c := login(t, e, 501, "")
	var got *service.AdminNote
	for i := range e.notes.adminNotes {
		if e.notes.adminNotes[i].Event == "new_customer" {
			got = &e.notes.adminNotes[i]
		}
	}
	if got == nil || got.ChatID != -1002 || got.TopicID != 7 || !strings.HasPrefix(got.Text, "Привет Тест / ") {
		t.Fatalf("routing/template not applied: %+v", got)
	}
	if got.ButtonPath != fmt.Sprintf("/admin/customers/%d", c.ID) {
		t.Fatalf("no open-customer button: %+v", got)
	}
	// Disabled event: test send refuses, and nothing is delivered.
	if err := e.svc.TestAdminEvent(ctx, "trial", c); err == nil {
		t.Fatal("disabled event test should fail")
	}
	// Values are HTML-escaped.
	mustSet(t, e, "notifications", `{"events":{"promo":{"template":"<b>{code}</b>"}}}`)
	if err := e.svc.TestAdminEvent(ctx, "promo", c); err != nil {
		t.Fatal(err)
	}
	last := e.notes.adminNotes[len(e.notes.adminNotes)-1]
	if last.ChatID != 0 || last.Text != "<b>‹code›</b>" {
		t.Fatalf("default route/escaping: %+v", last)
	}
}

// fakeAI answers chat completions with a scripted reply.
type fakeAI struct {
	mu      sync.Mutex
	reply   string
	calls   int
	lastReq map[string]any
}

func (f *fakeAI) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		_ = json.NewDecoder(r.Body).Decode(&f.lastReq)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": f.reply}}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeAI) set(reply string) { f.mu.Lock(); f.reply = reply; f.mu.Unlock() }

func TestTicketsWithAssistant(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	ai := &fakeAI{}
	srv := ai.server(t)
	mustSet(t, e, "ai", fmt.Sprintf(`{"enabled":true,"base_url":%q,"api_key":"k","model":"m1","confidence":0.75,"debounce_seconds":0,
		"max_calls_per_hour":3,"max_tokens":512,"temperature":0.1,"history_messages":12,"timeout_seconds":10,"use_customer_data":true,"use_knowledge":true,"notify_drafts":true}`, srv.URL+"/v1"))
	if _, err := e.pool.Exec(ctx, `INSERT INTO kb_articles (title, body) VALUES ('Установка на iPhone', 'Скачайте INCY из App Store и нажмите «Добавить подписку».')`); err != nil {
		t.Fatal(err)
	}
	c := login(t, e, 601, "")

	// 1. Confident answer goes to the customer.
	ai.set("```json\n{\"reply\":\"Установите INCY и нажмите «Добавить подписку».\",\"confidence\":0.9,\"escalate\":false}\n```")
	tk, err := e.svc.CreateTicket(ctx, c, "", "Как подключить айфон?", nil, "web")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.ProcessAI(ctx)
	v, _ := e.svc.CustomerTicket(ctx, c, tk.ID)
	if v.Status != "waiting" || v.Messages[len(v.Messages)-1].Author != "ai" {
		t.Fatalf("expected AI answer, status %s, msgs %+v", v.Status, v.Messages)
	}
	if !strings.Contains(fmt.Sprint(ai.lastReq["messages"]), "INCY из App Store") {
		t.Fatal("knowledge base not passed to the model")
	}
	e.svc.ProcessReplyNotices(ctx)
	if !strings.Contains(strings.Join(e.notes.customer, "|"), "Ответ поддержки") {
		t.Fatalf("customer not notified: %v", e.notes.customer)
	}

	// 2. Low confidence → draft for an operator, nothing sent.
	ai.set(`{"reply":"Может быть, перезагрузите телефон","confidence":0.4}`)
	if _, err := e.svc.CustomerMessage(ctx, c, tk.ID, "Не работает", nil); err != nil {
		t.Fatal(err)
	}
	e.svc.ProcessAI(ctx)
	adm, _ := e.svc.AdminTicket(ctx, tk.ID)
	draft := adm.Messages[len(adm.Messages)-1]
	if draft.Author != "draft" || draft.DraftStatus != "pending" || adm.Status != "open" {
		t.Fatalf("expected pending draft, got %+v status %s", draft, adm.Status)
	}
	if !strings.Contains(strings.Join(e.notes.admin, "|"), "ai_draft") {
		t.Fatal("no draft notice")
	}
	// Operator sends the edited draft: becomes an operator reply, AI is switched off.
	admin := login(t, e, 1, "")
	if err := e.svc.DraftAction(ctx, admin, draft.ID, "send", "Перезагрузите телефон и обновите подписку"); err != nil {
		t.Fatal(err)
	}
	adm, _ = e.svc.AdminTicket(ctx, tk.ID)
	if last := adm.Messages[len(adm.Messages)-1]; last.Author != "operator" || adm.AIEnabled || adm.Status != "waiting" {
		t.Fatalf("draft send: %+v ai=%v status=%s", last, adm.AIEnabled, adm.Status)
	}

	// 3. Escalation.
	tk2, _ := e.svc.CreateTicket(ctx, c, "Деньги", "Верните деньги", nil, "web")
	ai.set(`{"reply":"","confidence":0.2,"escalate":true,"reason":"возврат"}`)
	e.svc.ProcessAI(ctx)
	adm, _ = e.svc.AdminTicket(ctx, tk2.ID)
	if adm.AIEnabled || adm.Status != "open" || !strings.Contains(strings.Join(e.notes.admin, "|"), "ticket_escalated") {
		t.Fatalf("escalation not handled: ai=%v status=%s", adm.AIEnabled, adm.Status)
	}

	// Max open tickets and rating after close.
	mustSet(t, e, "support", `{"enabled":true,"max_open":2,"attach_max_mb":10,"attach_max_files":5}`)
	if _, err := e.svc.CreateTicket(ctx, c, "", "третье", nil, "web"); err == nil {
		t.Fatal("limit of open tickets not enforced")
	}
	if err := e.svc.CloseTicketByCustomer(ctx, c, tk2.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RateTicket(ctx, c, tk2.ID, 5, "спасибо"); err != nil {
		t.Fatal(err)
	}
	// Bot chat: a message goes to the latest open ticket.
	id, created, err := e.svc.BotSupportMessage(ctx, c, "ещё вопрос", nil)
	if err != nil || created || id != tk.ID {
		t.Fatalf("bot message: id=%d created=%v err=%v", id, created, err)
	}
}

func TestAutomationsImportAndRun(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	raw, err := os.ReadFile("../../import/automations-2026-10-13.json")
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, e, "trial", `{"enabled":false,"days":3}`)
	res, err := e.svc.ImportAutomations(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 10 {
		t.Fatalf("created %d", res.Created)
	}
	w := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"COMEBACK", "пробный период — 3 дн."} {
		if !strings.Contains(w, want) {
			t.Fatalf("missing warning %q in:\n%s", want, w)
		}
	}
	// Re-import replaces by name instead of duplicating.
	res, err = e.svc.ImportAutomations(ctx, raw)
	if err != nil || res.Updated != 10 || res.Created != 0 {
		t.Fatalf("reimport: %+v %v", res, err)
	}
	exp, _ := e.svc.ExportAutomations(ctx)
	if len(exp.Sequences) != 10 || exp.Sequences[0].Steps[1].DelayDuration != "7 days" {
		t.Fatal("export does not round-trip")
	}

	// Only one automation with a short custom flow, to test the engine.
	if _, err := e.pool.Exec(ctx, `DELETE FROM automations`); err != nil {
		t.Fatal(err)
	}
	mustSet(t, e, "automations", `{"enabled":true,"quiet_hours":"","abandoned_cart_hours":48,"personal_promo_hours":48}`)
	steps := `[{"step_order":1,"delay_duration":"00:00:00","message_content":"{first_name}, оплата {plan_name} не прошла. Код {promo:15}","stop_on_purchase":true,"buttons":[{"text":"Тарифы","url":"/buy"}],"repeat_count":1,"slots":[]},
		{"step_order":2,"delay_duration":"1 day","message_content":"Напоминание","stop_on_purchase":true,"buttons":[],"repeat_count":1,"slots":[]}]`
	a := store.Automation{Name: "Корзина", TriggerType: "event", TriggerFactor: "abandoned_cart", IsActive: true, CooldownHours: 72, Steps: json.RawMessage(steps)}
	if err := service.ValidateAutomation(&a); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAutomation(ctx, e.pool, &a); err != nil {
		t.Fatal(err)
	}
	c := login(t, e, 701, "")
	if _, err := e.pool.Exec(ctx, `INSERT INTO payments (customer_id, operation, title, amount, provider, status, created_at)
		VALUES ($1,'buy','Бобр Про',29900,'yookassa','canceled', now() - interval '2 hours')`, c.ID); err != nil {
		t.Fatal(err)
	}
	e.svc.ScanAutomationTriggers(ctx)
	e.svc.ScanAutomationTriggers(ctx) // second scan must not start a duplicate
	e.svc.ProcessAutomationRuns(ctx)
	runs, _ := store.CustomerRuns(ctx, e.pool, c.ID)
	if len(runs) != 1 || runs[0].Sent != 1 || runs[0].Status != "active" {
		t.Fatalf("runs: %+v", runs)
	}
	text := e.notes.customer[len(e.notes.customer)-1]
	if !strings.Contains(text, "Тест, оплата Бобр Про не прошла. Код <code>BG") {
		t.Fatalf("rendered text: %s", text)
	}
	var promos int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM promo_codes WHERE customer_id=$1 AND folder='Автоматизации' AND value=15`, c.ID).Scan(&promos)
	if promos != 1 {
		t.Fatal("personal promo not created")
	}
	// A purchase stops the chain at the next stop_on_purchase step and counts as a conversion.
	if _, err := e.pool.Exec(ctx, `INSERT INTO payments (customer_id, operation, title, amount, provider, status, paid_at)
		VALUES ($1,'buy','Бобр Про',29900,'yookassa','succeeded', now())`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE automation_runs SET next_at=now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	e.svc.ProcessAutomationRuns(ctx)
	runs, _ = store.CustomerRuns(ctx, e.pool, c.ID)
	if runs[0].Status != "stopped" || runs[0].StopReason != "purchase" || !runs[0].Converted {
		t.Fatalf("not stopped by purchase: %+v", runs[0])
	}
}

func TestParseDelay(t *testing.T) {
	for in, want := range map[string]time.Duration{"00:00:00": 0, "7 days": 7 * 24 * time.Hour, "1 day 02:30:00": 26*time.Hour + 30*time.Minute,
		"3 hours": 3 * time.Hour, "48h": 48 * time.Hour, "2 недели": 14 * 24 * time.Hour} {
		got, err := service.ParseDelay(in)
		if err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
}

func TestBroadcastAndNews(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mustSet(t, e, "broadcasts", `{"rate_per_second":30}`)
	admin := login(t, e, 1, "")
	var ids []int64
	for i := 0; i < 3; i++ {
		ids = append(ids, login(t, e, int64(800+i), "").ID)
	}
	// One customer blocked the bot: excluded from a Telegram broadcast.
	if _, err := e.pool.Exec(ctx, `UPDATE customers SET bot_blocked=true WHERE id=$1`, ids[2]); err != nil {
		t.Fatal(err)
	}
	b := &store.Broadcast{Body: "<b>Новые серверы</b>, {first_name}!", Audience: store.BroadcastAudience{Segment: "all"}, Channel: "telegram", ShowInNews: true}
	if err := e.svc.TestBroadcast(ctx, admin, b); err != nil {
		t.Fatal(err)
	}
	bad := &store.Broadcast{Body: "<div>x</div>", Audience: store.BroadcastAudience{Segment: "all"}, Channel: "telegram"}
	if err := e.svc.ValidateBroadcast(bad); err == nil {
		t.Fatal("unsupported tag accepted")
	}
	before := len(e.notes.customer)
	sb, err := e.svc.SubmitBroadcast(ctx, admin, b)
	if err != nil {
		t.Fatal(err)
	}
	if sb.Total != 3 { // admin + 2 reachable customers
		t.Fatalf("audience %d", sb.Total)
	}
	e.svc.ProcessBroadcasts(ctx)
	got, _ := store.GetBroadcast(ctx, e.pool, sb.ID)
	if got.Status != "done" || got.Sent != 3 || len(e.notes.customer)-before != 3 {
		t.Fatalf("broadcast: %+v", got)
	}
	if !strings.Contains(strings.Join(e.notes.admin, "|"), "broadcast_done") {
		t.Fatal("no completion notice")
	}
	c, _ := store.GetCustomer(ctx, e.pool, ids[0])
	n, _ := store.UnreadNews(ctx, e.pool, c)
	if n != 1 {
		t.Fatalf("unread news %d", n)
	}
	news, _ := e.svc.News(ctx, c)
	if len(news) != 1 || !strings.Contains(news[0].Body, "Тест") || !news[0].Unread {
		t.Fatalf("news: %+v", news)
	}
	c, _ = store.GetCustomer(ctx, e.pool, ids[0])
	if n, _ := store.UnreadNews(ctx, e.pool, c); n != 0 {
		t.Fatal("news not marked as seen")
	}
}

func TestTriggerQueries(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c := login(t, e, 901, "")
	p := store.TriggerParams{RecentlyExpiredHours: 72, AbandonedCartHours: 48, ActiveTrialHours: 24, ExpiringSoonDays: 3, InactiveDays: 30}
	for _, f := range []string{"recently_expired", "abandoned_cart", "active_trial", "expiring_soon", "inactive_30d"} {
		if _, err := store.TriggerCandidates(ctx, e.pool, f, p, 10); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	lp := store.LeadParams{HotSeenDays: 3, WarmSeenDays: 7, CoolSeenDays: 30, LeadMinAgeHours: 0, StableMinPayments: 2, StableMinDays: 30}
	ch, err := store.RecomputeLeadTemperatures(ctx, e.pool, lp)
	if err != nil || len(ch) != 1 || !ch[0].First || ch[0].New != "cool" {
		t.Fatalf("first pass: %+v %v", ch, err)
	}
	// Tried to pay → hot, reported as a change.
	if _, err := e.pool.Exec(ctx, `INSERT INTO payments (customer_id, operation, title, amount, provider, status) VALUES ($1,'buy','X',100,'yookassa','canceled')`, c.ID); err != nil {
		t.Fatal(err)
	}
	ch, err = store.RecomputeLeadTemperatures(ctx, e.pool, lp)
	if err != nil || len(ch) != 1 || ch[0].First || ch[0].New != "hot" {
		t.Fatalf("second pass: %+v %v", ch, err)
	}
}
