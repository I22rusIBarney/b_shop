package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"bshop/internal/ai"
	"bshop/internal/settings"
	"bshop/internal/store"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var statusNames = map[string]string{"open": "открыто", "waiting": "ждём ответа клиента", "resolved": "решено", "closed": "закрыто"}

func (s *Service) supportOn() error {
	if !s.set().Support.Enabled {
		return userErr("обращения сейчас не принимаются, напишите нам по ссылке поддержки")
	}
	return nil
}

func cleanBody(body string) string {
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	if r := []rune(body); len(r) > 4000 {
		body = string(r[:4000])
	}
	return body
}

func (s *Service) checkAttachments(ctx context.Context, c *store.Customer, ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return []uuid.UUID{}, nil
	}
	if max := s.set().Support.AttachMaxFiles; len(ids) > max {
		return nil, userErr(fmt.Sprintf("не больше %d вложений в сообщении", max))
	}
	return store.OwnedFiles(ctx, s.Store.Pool, c.ID, ids)
}

// CreateTicket opens a new ticket with the first message.
func (s *Service) CreateTicket(ctx context.Context, c *store.Customer, subject, body string, files []uuid.UUID, source string) (*store.Ticket, error) {
	if err := s.supportOn(); err != nil {
		return nil, err
	}
	body = cleanBody(body)
	files, err := s.checkAttachments(ctx, c, files)
	if err != nil {
		return nil, err
	}
	if body == "" && len(files) == 0 {
		return nil, userErr("опишите, что случилось")
	}
	set := s.set().Support
	if set.MaxOpen > 0 {
		n, err := store.CountOpenTickets(ctx, s.Store.Pool, c.ID)
		if err != nil {
			return nil, err
		}
		if n >= set.MaxOpen {
			return nil, userErr(fmt.Sprintf("у вас уже %d открытых обращения — напишите в одно из них", n))
		}
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		subject = truncate(strings.Split(body, "\n")[0], 60)
	}
	if subject == "" {
		subject = "Обращение"
	}
	t := &store.Ticket{CustomerID: c.ID, Subject: truncate(subject, 120), Source: source, AIEnabled: true}
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := store.CreateTicket(ctx, tx, t); err != nil {
			return err
		}
		if err := store.InsertTicketMessage(ctx, tx, &store.TicketMessage{TicketID: t.ID, Author: "customer", AuthorID: &c.ID,
			Body: body, Attachments: files}); err != nil {
			return err
		}
		if txt := s.autoReplyText(); txt != "" {
			return store.InsertTicketMessage(ctx, tx, &store.TicketMessage{TicketID: t.ID, Author: "system", Body: txt})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.scheduleAI(ctx, t)
	_ = store.AddEvent(ctx, s.Store.Pool, c.ID, "ticket", map[string]any{"ticket": t.ID}, nil)
	s.eventLink(ctx, "ticket_new", c, map[string]any{"ticket": t.ID, "subject": t.Subject, "text": truncate(body, 600)},
		"Открыть обращение", fmt.Sprintf("/admin/tickets/%d", t.ID))
	return t, nil
}

func (s *Service) autoReplyText() string {
	set := s.set()
	if from, to, ok := settings.ParseWindow(set.Support.WorkHours); ok && set.Support.OffHoursText != "" {
		now := time.Now().In(set.General.Location())
		if !settings.InWindow(now.Hour()*60+now.Minute(), from, to) {
			return set.Support.OffHoursText
		}
	}
	return set.Support.NewTicketText
}

// scheduleAI arms the debounce timer when the assistant may answer this ticket.
func (s *Service) scheduleAI(ctx context.Context, t *store.Ticket) {
	a := s.set().AI
	if !a.Enabled || !t.AIEnabled {
		return
	}
	due := time.Now().Add(time.Duration(a.DebounceSeconds) * time.Second)
	_, _ = s.Store.Pool.Exec(ctx, `UPDATE tickets SET ai_due_at=$2 WHERE id=$1 AND ai_enabled`, t.ID, due)
}

func (s *Service) ownedTicket(ctx context.Context, c *store.Customer, id int64) (*store.Ticket, error) {
	t, err := store.GetTicket(ctx, s.Store.Pool, id)
	if err != nil || t.CustomerID != c.ID {
		return nil, store.ErrNotFound
	}
	return t, nil
}

// CustomerMessage adds a customer message to a ticket and reopens it.
func (s *Service) CustomerMessage(ctx context.Context, c *store.Customer, id int64, body string, files []uuid.UUID) (*store.TicketMessage, error) {
	if err := s.supportOn(); err != nil {
		return nil, err
	}
	body = cleanBody(body)
	files, err := s.checkAttachments(ctx, c, files)
	if err != nil {
		return nil, err
	}
	if body == "" && len(files) == 0 {
		return nil, userErr("пустое сообщение")
	}
	var t *store.Ticket
	m := &store.TicketMessage{TicketID: id, Author: "customer", AuthorID: &c.ID, Body: body, Attachments: files}
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		t, err = store.GetTicketForUpdate(ctx, tx, id)
		if err != nil || t.CustomerID != c.ID {
			return store.ErrNotFound
		}
		if t.Status == "closed" {
			return userErr("обращение закрыто — создайте новое")
		}
		if err := store.InsertTicketMessage(ctx, tx, m); err != nil {
			return err
		}
		t.Status, t.UnreadAdmin, t.UnreadCustomer, t.LastCustomerAt, t.ResolvedAt = "open", true, false, time.Now(), nil
		return store.UpdateTicket(ctx, tx, t)
	})
	if err != nil {
		return nil, err
	}
	s.scheduleAI(ctx, t)
	s.eventLink(ctx, "ticket_message", c, map[string]any{"ticket": t.ID, "text": truncate(body, 600)},
		"Открыть обращение", fmt.Sprintf("/admin/tickets/%d", t.ID))
	return m, nil
}

// TicketView is a ticket with its messages.
type TicketView struct {
	store.Ticket
	Messages []store.TicketMessage `json:"messages"`
}

func (s *Service) CustomerTickets(ctx context.Context, c *store.Customer) ([]store.Ticket, error) {
	return store.CustomerTickets(ctx, s.Store.Pool, c.ID)
}

func (s *Service) CustomerTicket(ctx context.Context, c *store.Customer, id int64) (*TicketView, error) {
	t, err := s.ownedTicket(ctx, c, id)
	if err != nil {
		return nil, err
	}
	msgs, err := store.TicketMessages(ctx, s.Store.Pool, id, false)
	if err != nil {
		return nil, err
	}
	if t.UnreadCustomer {
		_, _ = s.Store.Pool.Exec(ctx, `UPDATE tickets SET unread_customer=false WHERE id=$1`, id)
	}
	return &TicketView{Ticket: *t, Messages: msgs}, nil
}

// CloseTicketByCustomer marks the problem as solved by the customer.
func (s *Service) CloseTicketByCustomer(ctx context.Context, c *store.Customer, id int64) error {
	t, err := s.ownedTicket(ctx, c, id)
	if err != nil {
		return err
	}
	if t.Status == "closed" {
		return nil
	}
	now := time.Now()
	t.Status, t.ClosedAt, t.AIDueAt = "closed", &now, nil
	if t.ResolvedAt == nil {
		t.ResolvedAt = &now
	}
	if err := store.UpdateTicket(ctx, s.Store.Pool, t); err != nil {
		return err
	}
	return store.InsertTicketMessage(ctx, s.Store.Pool, &store.TicketMessage{TicketID: id, Author: "system", Body: "Клиент закрыл обращение"})
}

func (s *Service) RateTicket(ctx context.Context, c *store.Customer, id int64, rating int, comment string) error {
	if rating < 1 || rating > 5 {
		return userErr("оценка от 1 до 5")
	}
	t, err := s.ownedTicket(ctx, c, id)
	if err != nil {
		return err
	}
	if t.Status != "closed" && t.Status != "resolved" {
		return userErr("оценить можно после решения вопроса")
	}
	t.Rating = &rating
	t.RatingComment = truncate(comment, 1000)
	if err := store.UpdateTicket(ctx, s.Store.Pool, t); err != nil {
		return err
	}
	extra := Raw("")
	if t.RatingComment != "" {
		extra = Raw("\n💬 " + escape(t.RatingComment))
	}
	s.eventLink(ctx, "ticket_rating", c, map[string]any{"ticket": t.ID, "rating": rating, "comment": extra},
		"Открыть обращение", fmt.Sprintf("/admin/tickets/%d", t.ID))
	return nil
}

// ---- operator side ----

type AdminTicketView struct {
	TicketView
	Customer      *store.Customer    `json:"customer"`
	Subscriptions []SubscriptionView `json:"subscriptions"`
}

func (s *Service) AdminTicket(ctx context.Context, id int64) (*AdminTicketView, error) {
	t, err := store.GetTicket(ctx, s.Store.Pool, id)
	if err != nil {
		return nil, err
	}
	msgs, err := store.TicketMessages(ctx, s.Store.Pool, id, true)
	if err != nil {
		return nil, err
	}
	c, err := store.GetCustomer(ctx, s.Store.Pool, t.CustomerID)
	if err != nil {
		return nil, err
	}
	subs, _ := s.Subscriptions(ctx, c, false)
	if t.UnreadAdmin {
		_, _ = s.Store.Pool.Exec(ctx, `UPDATE tickets SET unread_admin=false WHERE id=$1`, id)
	}
	return &AdminTicketView{TicketView: TicketView{Ticket: *t, Messages: msgs}, Customer: c, Subscriptions: subs}, nil
}

type OperatorReply struct {
	Body        string      `json:"body"`
	Attachments []uuid.UUID `json:"attachments"`
	Note        bool        `json:"note"`   // internal note, not shown to the customer
	Status      string      `json:"status"` // status after the reply: waiting (default) | resolved | open
	ReplyTo     *int64      `json:"reply_to"`
}

func (s *Service) noticeTime(c *store.Customer) time.Time {
	if c.TelegramID != nil && !c.BotBlocked {
		return time.Now()
	}
	return time.Now().Add(time.Duration(s.set().Support.EmailReplyDelayMinutes) * time.Minute)
}

// Reply posts an operator message (or internal note).
func (s *Service) Reply(ctx context.Context, admin *store.Customer, id int64, in OperatorReply) (*store.TicketMessage, error) {
	in.Body = cleanBody(in.Body)
	if in.Body == "" && len(in.Attachments) == 0 {
		return nil, userErr("пустое сообщение")
	}
	if in.Attachments == nil {
		in.Attachments = []uuid.UUID{}
	}
	var m *store.TicketMessage
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		t, err := store.GetTicketForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		c, err := store.GetCustomer(ctx, tx, t.CustomerID)
		if err != nil {
			return err
		}
		m = &store.TicketMessage{TicketID: id, Author: "operator", AuthorID: &admin.ID, Body: in.Body, Attachments: in.Attachments, ReplyTo: in.ReplyTo}
		if in.Note {
			m.Author = "note"
			return store.InsertTicketMessage(ctx, tx, m)
		}
		at := s.noticeTime(c)
		m.NotifyAt = &at
		if err := store.InsertTicketMessage(ctx, tx, m); err != nil {
			return err
		}
		s.applyReplyStatus(t, in.Status)
		t.AIEnabled, t.AIDueAt = false, nil // the operator took over
		if t.AssigneeID == nil {
			t.AssigneeID = &admin.ID
		}
		return store.UpdateTicket(ctx, tx, t)
	})
	return m, err
}

func (s *Service) applyReplyStatus(t *store.Ticket, status string) {
	now := time.Now()
	switch status {
	case "resolved":
		t.Status, t.ResolvedAt = "resolved", &now
	case "open":
		t.Status = "open"
	default:
		t.Status = "waiting"
	}
	if t.FirstResponseAt == nil {
		t.FirstResponseAt = &now
	}
	t.UnreadCustomer, t.UnreadAdmin = true, false
}

type TicketPatch struct {
	Status     *string `json:"status"`
	AssigneeID *int64  `json:"assignee_id"`
	Tag        *string `json:"tag"`
	AIEnabled  *bool   `json:"ai_enabled"`
	Subject    *string `json:"subject"`
}

func (s *Service) PatchTicket(ctx context.Context, admin *store.Customer, id int64, p TicketPatch) (*store.Ticket, error) {
	var t *store.Ticket
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		t, err = store.GetTicketForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		now := time.Now()
		if p.Status != nil && *p.Status != t.Status {
			if _, ok := statusNames[*p.Status]; !ok {
				return userErr("неизвестный статус")
			}
			t.Status = *p.Status
			switch t.Status {
			case "resolved":
				t.ResolvedAt = &now
			case "closed":
				t.ClosedAt, t.AIDueAt = &now, nil
				if t.ResolvedAt == nil {
					t.ResolvedAt = &now
				}
			case "open", "waiting":
				t.ClosedAt = nil
			}
			_ = store.InsertTicketMessage(ctx, tx, &store.TicketMessage{TicketID: id, Author: "system",
				Body: "Статус: " + statusNames[t.Status]})
		}
		if p.AssigneeID != nil {
			if *p.AssigneeID == 0 {
				t.AssigneeID = nil
			} else {
				t.AssigneeID = p.AssigneeID
			}
		}
		if p.Tag != nil {
			t.Tag = truncate(*p.Tag, 40)
		}
		if p.Subject != nil && strings.TrimSpace(*p.Subject) != "" {
			t.Subject = truncate(*p.Subject, 120)
		}
		if p.AIEnabled != nil {
			t.AIEnabled = *p.AIEnabled
			if !t.AIEnabled {
				t.AIDueAt = nil
			}
		}
		return store.UpdateTicket(ctx, tx, t)
	})
	return t, err
}

// DraftAction sends (optionally edited) or rejects an AI draft.
func (s *Service) DraftAction(ctx context.Context, admin *store.Customer, msgID int64, action, body string) error {
	d, err := store.GetTicketMessage(ctx, s.Store.Pool, msgID)
	if err != nil || d.Author != "draft" {
		return store.ErrNotFound
	}
	if d.DraftStatus != "pending" {
		return userErr("черновик уже обработан")
	}
	switch action {
	case "reject":
		return store.SetDraftStatus(ctx, s.Store.Pool, msgID, "rejected")
	case "send":
		if strings.TrimSpace(body) == "" {
			body = d.Body
		}
		if _, err := s.Reply(ctx, admin, d.TicketID, OperatorReply{Body: body}); err != nil {
			return err
		}
		return store.SetDraftStatus(ctx, s.Store.Pool, msgID, "sent")
	}
	return userErr("неизвестное действие")
}

// ---- AI assistant ----

type aiAnswer struct {
	Reply      string  `json:"reply"`
	Confidence float64 `json:"confidence"`
	Escalate   bool    `json:"escalate"`
	Reason     string  `json:"reason"`
}

const aiFormat = `Ответь СТРОГО одним JSON-объектом без пояснений:
{"reply": "текст ответа клиенту (простой текст, без markdown)", "confidence": число от 0 до 1 — насколько ты уверен, что ответ верный и полный,
 "escalate": true, если нужен живой оператор (деньги, возврат, ошибка оплаты, жалоба, просьба позвать человека, ты не знаешь ответа), "reason": "коротко зачем оператор"}`

// ProcessAI answers tickets whose debounce timer fired.
func (s *Service) ProcessAI(ctx context.Context) {
	a := s.set().AI
	if !a.Enabled {
		return
	}
	// The worker's own timeout is short; give each answer the configured model timeout.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(a.TimeoutSeconds+30)*time.Second*3)
	defer cancel()
	ids, err := store.TicketsAIDue(ctx, s.Store.Pool, 3)
	if err != nil {
		s.Log.Error("ai due", "err", err)
		return
	}
	for _, id := range ids {
		if err := s.answerTicket(ctx, id); err != nil {
			s.Log.Warn("ai answer", "ticket", id, "err", err)
		}
	}
}

func (s *Service) answerTicket(ctx context.Context, id int64) error {
	set := s.set().AI
	t, err := store.GetTicket(ctx, s.Store.Pool, id)
	if err != nil || !t.AIEnabled || t.Status == "closed" {
		return err
	}
	now := time.Now()
	if t.AIWindowStart == nil || now.Sub(*t.AIWindowStart) > time.Hour {
		t.AIWindowStart, t.AIWindowCalls = &now, 0
	}
	if set.MaxCallsPerHour > 0 && t.AIWindowCalls >= set.MaxCallsPerHour {
		return nil // over the limit: wait for an operator
	}
	msgs, err := store.TicketMessages(ctx, s.Store.Pool, id, false)
	if err != nil {
		return err
	}
	// Answer only if the last word is the customer's.
	last := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Author == "customer" || msgs[i].Author == "operator" || msgs[i].Author == "ai" {
			last = i
			break
		}
	}
	if last < 0 || msgs[last].Author != "customer" {
		return nil
	}
	c, err := store.GetCustomer(ctx, s.Store.Pool, t.CustomerID)
	if err != nil {
		return err
	}
	t.AIWindowCalls++
	_, _ = s.Store.Pool.Exec(ctx, `UPDATE tickets SET ai_window_start=$2, ai_window_calls=$3 WHERE id=$1`, id, t.AIWindowStart, t.AIWindowCalls)

	ans, err := s.askAssistant(ctx, t, c, msgs)
	if err != nil {
		s.event(ctx, "system_error", nil, map[string]any{"error": fmt.Sprintf("Ассистент не ответил на обращение #%d: %v", id, err)})
		return err
	}
	return s.applyAIAnswer(ctx, t, c, ans)
}

func (s *Service) applyAIAnswer(ctx context.Context, t *store.Ticket, c *store.Customer, ans *aiAnswer) error {
	set := s.set().AI
	ans.Reply = cleanBody(ans.Reply)
	conf := float32(ans.Confidence)
	path := fmt.Sprintf("/admin/tickets/%d", t.ID)
	// Re-check under lock: the customer or an operator may have written meanwhile.
	return s.Store.Tx(ctx, func(tx pgx.Tx) error {
		cur, err := store.GetTicketForUpdate(ctx, tx, t.ID)
		if err != nil || !cur.AIEnabled || cur.Status == "closed" {
			return err
		}
		switch {
		case ans.Escalate:
			cur.AIEnabled = false
			cur.Status = "open"
			reply := "Передал ваш вопрос оператору — он ответит здесь же."
			if ans.Reply != "" && ans.Confidence >= set.Confidence {
				reply = ans.Reply
			}
			at := s.noticeTime(c)
			if err := store.InsertTicketMessage(ctx, tx, &store.TicketMessage{TicketID: t.ID, Author: "ai", Body: reply, Confidence: &conf, NotifyAt: &at}); err != nil {
				return err
			}
			_ = store.InsertTicketMessage(ctx, tx, &store.TicketMessage{TicketID: t.ID, Author: "note", Body: "Ассистент передал оператору: " + ans.Reason})
			if err := store.UpdateTicket(ctx, tx, cur); err != nil {
				return err
			}
			s.eventLink(ctx, "ticket_escalated", c, map[string]any{"ticket": t.ID, "reason": ans.Reason}, "Открыть обращение", path)
		case ans.Reply != "" && ans.Confidence >= set.Confidence:
			at := s.noticeTime(c)
			if err := store.InsertTicketMessage(ctx, tx, &store.TicketMessage{TicketID: t.ID, Author: "ai", Body: ans.Reply, Confidence: &conf, NotifyAt: &at}); err != nil {
				return err
			}
			s.applyReplyStatus(cur, "waiting")
			cur.UnreadAdmin = false
			if err := store.UpdateTicket(ctx, tx, cur); err != nil {
				return err
			}
		case ans.Reply != "":
			if err := store.InsertTicketMessage(ctx, tx, &store.TicketMessage{TicketID: t.ID, Author: "draft", Body: ans.Reply, Confidence: &conf, DraftStatus: "pending"}); err != nil {
				return err
			}
			if set.NotifyDrafts {
				s.eventLink(ctx, "ai_draft", c, map[string]any{"ticket": t.ID, "confidence": fmt.Sprintf("%.2f", ans.Confidence),
					"text": truncate(ans.Reply, 800)}, "Открыть обращение", path)
			}
		}
		return nil
	})
}

func (s *Service) aiClient() (*ai.Client, []string) {
	a := s.set().AI
	models := append([]string{a.Model}, a.FallbackModels...)
	return ai.New(a.BaseURL, a.APIKey, time.Duration(a.TimeoutSeconds)*time.Second), models
}

func (s *Service) askAssistant(ctx context.Context, t *store.Ticket, c *store.Customer, msgs []store.TicketMessage) (*aiAnswer, error) {
	set := s.set().AI
	var sys strings.Builder
	sys.WriteString(strings.TrimSpace(set.SystemPrompt))
	sys.WriteString("\n\nСервис: " + s.set().General.ShopName + ". Сегодня " + time.Now().In(s.set().General.Location()).Format("02.01.2006 15:04") + ".")
	if set.UseCustomerData {
		sys.WriteString("\n\n## Данные клиента\n" + s.customerContext(ctx, c))
	}
	var question strings.Builder
	for _, m := range msgs {
		if m.Author == "customer" {
			question.WriteString(m.Body + " ")
		}
	}
	if set.UseKnowledge {
		if kb := s.knowledgeContext(ctx, question.String(), set.KnowledgeChars); kb != "" {
			sys.WriteString("\n\n## База знаний\n" + kb)
		}
	}
	sys.WriteString("\n\n" + aiFormat)

	history := msgs
	if len(history) > set.HistoryMessages {
		history = history[len(history)-set.HistoryMessages:]
	}
	conv := []ai.Message{{Role: "system", Content: sys.String()}}
	hasImages := false
	for _, m := range history {
		role := "user"
		switch m.Author {
		case "operator", "ai":
			role = "assistant"
		case "system":
			continue
		}
		if role == "user" && set.AnalyzeImages && len(m.Attachments) > 0 {
			parts := []ai.Part{{Type: "text", Text: m.Body}}
			for _, id := range m.Attachments {
				f, err := store.GetFile(ctx, s.Store.Pool, id)
				if err != nil || !strings.HasPrefix(f.Mime, "image/") {
					continue
				}
				parts = append(parts, ai.Part{Type: "image_url", ImageURL: &ai.ImageURL{URL: "data:" + f.Mime + ";base64," + base64.StdEncoding.EncodeToString(f.Data)}})
				hasImages = true
			}
			conv = append(conv, ai.Message{Role: role, Content: parts})
			continue
		}
		body := m.Body
		if len(m.Attachments) > 0 {
			body += fmt.Sprintf(" [вложений: %d]", len(m.Attachments))
		}
		conv = append(conv, ai.Message{Role: role, Content: body})
	}
	client, models := s.aiClient()
	if hasImages && set.VisionModel != "" {
		models = append([]string{set.VisionModel}, models...)
	}
	temp := set.Temperature
	ctx, cancel := context.WithTimeout(ctx, time.Duration(set.TimeoutSeconds)*time.Second)
	defer cancel()
	text, _, err := client.Complete(ctx, models, ai.Request{Messages: conv, MaxTokens: set.MaxTokens, Temperature: &temp, ReasoningEffort: set.ReasoningEffort})
	if err != nil {
		return nil, err
	}
	return parseAIAnswer(text), nil
}

// parseAIAnswer accepts JSON or, if the model ignored the format, treats the text as a low-confidence draft.
func parseAIAnswer(text string) *aiAnswer {
	var ans aiAnswer
	if j := ai.ExtractJSON(text); j != "" && json.Unmarshal([]byte(j), &ans) == nil && (ans.Reply != "" || ans.Escalate) {
		if ans.Confidence > 1 {
			ans.Confidence /= 100
		}
		return &ans
	}
	return &aiAnswer{Reply: strings.TrimSpace(text), Confidence: 0}
}

func (s *Service) customerContext(ctx context.Context, c *store.Customer) string {
	var b strings.Builder
	loc := s.set().General.Location()
	fmt.Fprintf(&b, "Имя: %s. Клиент с %s.", c.DisplayName(), c.CreatedAt.In(loc).Format("02.01.2006"))
	if c.TrialUsed {
		b.WriteString(" Пробный период уже использован.")
	}
	subs, _ := s.Subscriptions(ctx, c, false)
	if len(subs) == 0 {
		b.WriteString("\nПодписок нет.")
	}
	for _, sv := range subs {
		state := "активна"
		if !sv.Active() {
			state = "истекла"
		}
		dev := "без лимита устройств"
		if sv.TotalDevices() > 0 {
			dev = fmt.Sprintf("до %d устройств", sv.TotalDevices())
		}
		kind := map[string]string{"trial": "пробная", "paid": "платная", "free": "бесплатная"}[sv.Kind]
		fmt.Fprintf(&b, "\n- Подписка «%s» (%s): %s, до %s, %s, автопродление %s.", sv.Title, kind, state,
			sv.ExpiresAt.In(loc).Format("02.01.2006"), dev, map[bool]string{true: "включено", false: "выключено"}[sv.Autopay])
	}
	if plans, err := s.Catalog(ctx, c); err == nil && len(plans) > 0 {
		b.WriteString("\nТарифы для покупки:")
		for _, p := range plans {
			if len(p.Offers) == 0 {
				continue
			}
			var offers []string
			for _, o := range p.Offers {
				offers = append(offers, fmt.Sprintf("%s — %s", periodLabel(o.Days, 0), rub(o.Price)))
			}
			dev := "устройств без лимита"
			if p.DeviceLimit > 0 {
				dev = fmt.Sprintf("до %d устройств", p.DeviceLimit)
			}
			fmt.Fprintf(&b, "\n- %s (%s): %s", p.Name, dev, strings.Join(offers, "; "))
		}
	}
	return b.String()
}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		r := []rune(w)
		if len(r) < 4 {
			continue
		}
		if len(r) > 6 {
			r = r[:6] // crude stemming for Russian endings
		}
		out[string(r)] = true
	}
	return out
}

// knowledgeContext picks FAQ entries and knowledge articles that share the most words with the question, within budget.
func (s *Service) knowledgeContext(ctx context.Context, question string, budget int) string {
	type doc struct {
		text  string
		score int
	}
	var docs []doc
	q := words(question)
	add := func(text string) {
		score := 0
		for w := range words(text) {
			if q[w] {
				score++
			}
		}
		docs = append(docs, doc{text, score})
	}
	if faq, err := store.ListFAQ(ctx, s.Store.Pool, true); err == nil {
		for _, f := range faq {
			add("В: " + f.Question + "\nО: " + f.Answer)
		}
	}
	if kb, err := store.ListKB(ctx, s.Store.Pool, true); err == nil {
		for _, a := range kb {
			add("### " + a.Title + "\n" + a.Body)
		}
	}
	sort.SliceStable(docs, func(i, j int) bool { return docs[i].score > docs[j].score })
	var b strings.Builder
	for _, d := range docs {
		if budget > 0 && b.Len()+len(d.text) > budget {
			continue
		}
		b.WriteString(d.text + "\n\n")
	}
	return strings.TrimSpace(b.String())
}

// TestAI checks the assistant connection with a tiny request.
func (s *Service) TestAI(ctx context.Context) (string, error) {
	set := s.set().AI
	if set.BaseURL == "" || set.Model == "" {
		return "", userErr("укажите адрес API и модель")
	}
	client, models := s.aiClient()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(set.TimeoutSeconds)*time.Second)
	defer cancel()
	text, model, err := client.Complete(ctx, models, ai.Request{MaxTokens: 64, Messages: []ai.Message{{Role: "user", Content: "Ответь одним словом: работает?"}}})
	if err != nil {
		return "", userErr("нет ответа: " + err.Error())
	}
	return fmt.Sprintf("Модель %s ответила: %s", model, truncate(text, 80)), nil
}

// SuggestReply asks the assistant for a draft on demand (operator button), regardless of thresholds.
func (s *Service) SuggestReply(ctx context.Context, id int64) (*aiAnswer, error) {
	if s.set().AI.BaseURL == "" {
		return nil, userErr("ассистент не настроен")
	}
	t, err := store.GetTicket(ctx, s.Store.Pool, id)
	if err != nil {
		return nil, err
	}
	c, err := store.GetCustomer(ctx, s.Store.Pool, t.CustomerID)
	if err != nil {
		return nil, err
	}
	msgs, err := store.TicketMessages(ctx, s.Store.Pool, id, false)
	if err != nil {
		return nil, err
	}
	ans, err := s.askAssistant(ctx, t, c, msgs)
	if err != nil {
		return nil, userErr("ассистент не ответил: " + err.Error())
	}
	return ans, nil
}

// ---- timers ----

// ProcessReplyNotices tells customers about new replies (Telegram right away, email after the delay).
func (s *Service) ProcessReplyNotices(ctx context.Context) {
	due, err := store.DueReplyNotices(ctx, s.Store.Pool, 50)
	if err != nil {
		s.Log.Error("reply notices", "err", err)
		return
	}
	for _, n := range due {
		if n.Stale {
			continue // the customer already wrote after this reply — they have seen it
		}
		c, err := store.GetCustomer(ctx, s.Store.Pool, n.CustomerID)
		if err != nil {
			continue
		}
		msg := Message{
			Text:         fmt.Sprintf("💬 <b>Ответ поддержки</b> · обращение #%d\n\n%s", n.TicketID, escape(truncate(n.Body, 3500))),
			ButtonText:   "Открыть обращение",
			ButtonURL:    fmt.Sprintf("%s/support/%d", s.Cfg.PublicURL, n.TicketID),
			EmailSubject: fmt.Sprintf("Ответ по обращению #%d", n.TicketID),
		}
		if err := s.Notify.Deliver(ctx, c, msg, "auto"); err != nil && err != ErrUnreachable {
			s.Log.Warn("reply notice", "ticket", n.TicketID, "err", err)
		}
	}
}

// ProcessTicketTimers closes idle tickets and asks for ratings.
func (s *Service) ProcessTicketTimers(ctx context.Context) {
	set := s.set().Support
	closed, err := store.AutocloseTickets(ctx, s.Store.Pool, time.Duration(set.AutocloseWaitingMinutes)*time.Minute,
		time.Duration(set.AutocloseResolvedMinutes)*time.Minute)
	if err != nil {
		s.Log.Error("autoclose", "err", err)
	}
	for _, t := range closed {
		_ = store.InsertTicketMessage(ctx, s.Store.Pool, &store.TicketMessage{TicketID: t.ID, Author: "system", Body: "Обращение закрыто автоматически"})
	}
	if !set.RatingEnabled {
		return
	}
	rate, err := store.TicketsToRate(ctx, s.Store.Pool, time.Duration(set.RatingDelayMinutes)*time.Minute)
	if err != nil {
		return
	}
	for _, t := range rate {
		c, err := store.GetCustomer(ctx, s.Store.Pool, t.CustomerID)
		if err != nil {
			continue
		}
		_ = s.Notify.Deliver(ctx, c, Message{
			Text:         fmt.Sprintf("Удалось решить вопрос по обращению #%d «%s»? Оцените, пожалуйста, работу поддержки — это займёт пару секунд.", t.ID, escape(t.Subject)),
			ButtonText:   "Оценить",
			ButtonURL:    fmt.Sprintf("%s/support/%d", s.Cfg.PublicURL, t.ID),
			EmailSubject: "Оцените работу поддержки",
		}, "auto")
	}
}

// BotSupportMessage turns a message written to the bot into a ticket message.
func (s *Service) BotSupportMessage(ctx context.Context, c *store.Customer, text string, files []uuid.UUID) (ticketID int64, created bool, err error) {
	if !s.set().Support.Enabled || !s.set().Support.BotChat {
		return 0, false, userErr("off")
	}
	t, err := store.LatestOpenTicket(ctx, s.Store.Pool, c.ID)
	if err == store.ErrNotFound {
		nt, err := s.CreateTicket(ctx, c, "", text, files, "telegram")
		if err != nil {
			return 0, false, err
		}
		return nt.ID, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	_, err = s.CustomerMessage(ctx, c, t.ID, text, files)
	return t.ID, false, err
}

// SaveCustomerFile stores a customer upload (ticket attachment) after checks.
func (s *Service) SaveCustomerFile(ctx context.Context, c *store.Customer, name, mime string, data []byte) (*store.File, error) {
	set := s.set().Support
	if set.AttachMaxMB == 0 || set.AttachMaxFiles == 0 {
		return nil, userErr("вложения отключены")
	}
	if len(data) > set.AttachMaxMB<<20 {
		return nil, userErr(fmt.Sprintf("файл больше %d МБ", set.AttachMaxMB))
	}
	switch mime {
	case "image/jpeg", "image/png", "image/webp", "image/gif", "application/pdf", "text/plain", "video/mp4":
	default:
		return nil, userErr("можно прикрепить картинку, PDF, текст или видео MP4")
	}
	var n int
	_ = s.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM files WHERE owner_id=$1 AND created_at > now() - interval '1 day'`, c.ID).Scan(&n)
	if n > 50 {
		return nil, userErr("слишком много файлов за сутки")
	}
	return store.PutOwnedFile(ctx, s.Store.Pool, c.ID, truncate(name, 120), mime, data)
}
