package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"bshop/internal/remnawave"
	"bshop/internal/service"
	"bshop/internal/store"
)

func TestKBProposalsAndAIContext(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	fa := &fakeAI{}
	srv := fa.server(t)
	mustSet(t, e, "ai", fmt.Sprintf(`{"enabled":true,"base_url":%q,"api_key":"k","model":"m1","confidence":0.75,"debounce_seconds":0,
		"max_calls_per_hour":3,"max_tokens":512,"temperature":0.1,"history_messages":12,"timeout_seconds":10,
		"use_customer_data":true,"use_live_data":true,"use_server_status":true}`, srv.URL+"/v1"))
	e.panel.nodes = []remnawave.Node{{Name: "Финляндия", IsConnected: true}, {Name: "Швеция", IsConnected: false}}
	admin := login(t, e, 1, "")
	c := login(t, e, 1101, "")

	// The assistant sees server status and live customer data.
	fa.set(`{"reply":"Попробуйте Финляндию","confidence":0.9,"escalate":false}`)
	tk, err := e.svc.CreateTicket(ctx, c, "Швеция", "Не работает Швеция", nil, "web")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.ProcessAI(ctx)
	req := fmt.Sprint(fa.lastReq["messages"])
	if !strings.Contains(req, "НЕ работают (1): Швеция") || !strings.Contains(req, "Подписок нет") {
		t.Fatalf("context missing server status or customer data: %s", req)
	}

	// Operator answers and the ticket is closed: it becomes material for the knowledge base.
	if _, err := e.svc.Reply(ctx, admin, tk.ID, service.OperatorReply{Body: "Швеция на обслуживании, выберите Финляндию в приложении.", Status: "resolved"}); err != nil {
		t.Fatal(err)
	}
	fa.set("```json\n{\"useful\":true,\"title\":\"Не работает одна из локаций\",\"body\":\"Выберите другую локацию в приложении.\"}\n```")
	n, err := e.svc.ProposeKBFromTickets(ctx, 10)
	if err != nil || n != 1 {
		t.Fatalf("proposals = %d, %v", n, err)
	}
	// Already scanned tickets are not read again.
	if n, _ := e.svc.ProposeKBFromTickets(ctx, 10); n != 0 {
		t.Fatalf("ticket scanned twice: %d", n)
	}
	props, _ := store.ListKBProposals(ctx, e.pool, "pending")
	if len(props) != 1 {
		t.Fatalf("pending = %d", len(props))
	}
	// Approval with an edited title creates an active article; deciding twice is refused.
	p, err := e.svc.DecideKBProposal(ctx, props[0].ID, true, "Локация не подключается", "")
	if err != nil || p.Status != "approved" || p.ArticleID == nil {
		t.Fatalf("approve = %+v, %v", p, err)
	}
	kb, _ := store.ListKB(ctx, e.pool, true)
	if len(kb) != 1 || kb[0].Title != "Локация не подключается" {
		t.Fatalf("kb = %+v", kb)
	}
	if _, err := e.svc.DecideKBProposal(ctx, props[0].ID, false, "", ""); err == nil {
		t.Fatal("decided twice")
	}
}
