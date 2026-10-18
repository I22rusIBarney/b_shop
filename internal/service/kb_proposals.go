package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"bshop/internal/ai"
	"bshop/internal/store"
)

const kbPrompt = `Ты помогаешь вести базу знаний поддержки VPN-сервиса. Ниже — закрытое обращение клиента и ответы сотрудника.
Если из ответов сотрудника можно сделать полезную статью для похожих вопросов в будущем, напиши её.
Правила:
- Пиши обобщённо, без имён, ссылок на подписки, ID, email, телефонов и других персональных данных.
- Опирайся только на то, что ответил сотрудник; ничего не выдумывай.
- Если вопрос разовый (личная проблема оплаты, благодарность, спам) или ответа сотрудника нет — статья не нужна.
- Если такая статья уже есть в списке существующих — статья не нужна.
Ответь строго JSON: {"useful": true|false, "title": "Короткий заголовок-вопрос", "body": "Ответ: шаги и пояснения"}`

// ProposeKBFromTickets reads up to n closed tickets with operator answers and stores article proposals.
// Returns how many proposals were created.
func (s *Service) ProposeKBFromTickets(ctx context.Context, n int) (int, error) {
	set := s.set().AI
	if set.BaseURL == "" || set.Model == "" || set.APIKey == "" {
		return 0, userErr("ИИ не настроен: укажите адрес API, ключ и модель в «Поддержка и ИИ»")
	}
	ids, err := store.TicketsForKB(ctx, s.Store.Pool, n)
	if err != nil {
		return 0, err
	}
	made := 0
	for _, id := range ids {
		ok, err := s.proposeFromTicket(ctx, id)
		if err != nil {
			s.Log.Warn("kb proposal", "ticket", id, "err", err)
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				break
			}
			continue // the ticket stays unscanned and is retried next time
		}
		if ok {
			made++
		}
	}
	return made, nil
}

func (s *Service) proposeFromTicket(ctx context.Context, id int64) (bool, error) {
	t, err := store.GetTicket(ctx, s.Store.Pool, id)
	if err != nil {
		return false, err
	}
	msgs, err := store.TicketMessages(ctx, s.Store.Pool, id, false)
	if err != nil {
		return false, err
	}
	var conv strings.Builder
	fmt.Fprintf(&conv, "Тема: %s\n", t.Subject)
	for _, m := range msgs {
		who := map[string]string{"customer": "Клиент", "operator": "Сотрудник", "ai": "Ассистент"}[m.Author]
		if who == "" || strings.TrimSpace(m.Body) == "" {
			continue
		}
		fmt.Fprintf(&conv, "%s: %s\n", who, truncate(m.Body, 1500))
	}
	var existing strings.Builder
	if kb, err := store.ListKB(ctx, s.Store.Pool, false); err == nil {
		for _, a := range kb {
			existing.WriteString("- " + a.Title + "\n")
		}
	}
	if props, err := store.ListKBProposals(ctx, s.Store.Pool, "pending"); err == nil {
		for _, p := range props {
			existing.WriteString("- " + p.Title + "\n")
		}
	}
	set := s.set().AI
	client, models := s.aiClient()
	cctx, cancel := context.WithTimeout(ctx, time.Duration(set.TimeoutSeconds)*time.Second)
	defer cancel()
	temp := 0.2
	text, _, err := client.Complete(cctx, models, ai.Request{MaxTokens: 1200, Temperature: &temp, Messages: []ai.Message{
		{Role: "system", Content: kbPrompt + "\n\nСуществующие статьи:\n" + existing.String()},
		{Role: "user", Content: truncate(conv.String(), 12000)},
	}})
	if err != nil {
		return false, err
	}
	var out struct {
		Useful bool   `json:"useful"`
		Title  string `json:"title"`
		Body   string `json:"body"`
	}
	j := ai.ExtractJSON(text)
	if j == "" || json.Unmarshal([]byte(j), &out) != nil {
		return false, fmt.Errorf("unexpected answer: %s", truncate(text, 200))
	}
	made := false
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if out.Useful && strings.TrimSpace(out.Title) != "" && strings.TrimSpace(out.Body) != "" {
			tid := t.ID
			if err := store.InsertKBProposal(ctx, tx, &store.KBProposal{TicketID: &tid, Title: truncate(strings.TrimSpace(out.Title), 200),
				Body: strings.TrimSpace(out.Body)}); err != nil {
				return err
			}
			made = true
		}
		return store.MarkTicketKBScanned(ctx, tx, t.ID)
	})
	return made, err
}

// DecideKBProposal approves (optionally with edits) or rejects a proposal; approval creates an active article.
func (s *Service) DecideKBProposal(ctx context.Context, id int64, approve bool, title, body string) (*store.KBProposal, error) {
	var out *store.KBProposal
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		p, err := store.GetKBProposalForUpdate(ctx, tx, id)
		if err != nil {
			return userErr("предложение не найдено")
		}
		if p.Status != "pending" {
			return userErr("предложение уже рассмотрено")
		}
		if !approve {
			p.Status = "rejected"
			out = p
			return store.DecideKBProposal(ctx, tx, id, "rejected", nil)
		}
		if strings.TrimSpace(title) != "" {
			p.Title = strings.TrimSpace(title)
		}
		if strings.TrimSpace(body) != "" {
			p.Body = strings.TrimSpace(body)
		}
		a := &store.KBArticle{Title: p.Title, Body: p.Body, IsActive: true}
		if err := store.SaveKB(ctx, tx, a); err != nil {
			return err
		}
		p.Status, p.ArticleID = "approved", &a.ID
		out = p
		return store.DecideKBProposal(ctx, tx, id, "approved", &a.ID)
	})
	return out, err
}

// RunKBProposals is the background job: a few tickets per run when enabled.
func (s *Service) RunKBProposals(ctx context.Context) {
	set := s.set().AI
	if !set.Enabled || !set.KBFromTickets || set.APIKey == "" {
		return
	}
	if n, err := s.ProposeKBFromTickets(ctx, 5); err != nil {
		s.Log.Warn("kb proposals", "err", err)
	} else if n > 0 {
		s.event(ctx, "kb_proposals", nil, map[string]any{"count": n})
	}
}
