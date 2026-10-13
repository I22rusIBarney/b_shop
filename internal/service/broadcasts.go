package service

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"bshop/internal/store"
)

var segments = map[string]string{
	"all": "Все", "active": "С активной подпиской", "paid_active": "С платной подпиской", "trial": "На пробном периоде",
	"expired": "Подписка истекла", "no_sub": "Без подписки", "never_paid": "Ни разу не платили", "plan": "На тарифе",
}

var allowedTagRe = regexp.MustCompile(`(?i)</?([a-z\-]+)[^>]*>`)

// checkTelegramHTML allows only tags Telegram understands.
func checkTelegramHTML(s string) error {
	for _, m := range allowedTagRe.FindAllStringSubmatch(s, -1) {
		switch strings.ToLower(m[1]) {
		case "b", "strong", "i", "em", "u", "ins", "s", "strike", "del", "code", "pre", "a", "tg-spoiler", "blockquote", "span":
		default:
			return userErr("тег <" + m[1] + "> Telegram не поддерживает")
		}
	}
	return nil
}

func (s *Service) ValidateBroadcast(b *store.Broadcast) error {
	b.Title = strings.TrimSpace(b.Title)
	b.Body = strings.TrimSpace(b.Body)
	if b.Body == "" {
		return userErr("напишите текст рассылки")
	}
	limit := 4096
	if b.Image != "" {
		limit = 4096 // long captions are sent as photo + separate text
	}
	if utf8.RuneCountInString(b.Body) > limit {
		return userErr("текст длиннее 4096 символов")
	}
	if err := checkTelegramHTML(b.Body); err != nil {
		return err
	}
	if _, ok := segments[b.Audience.Segment]; !ok {
		return userErr("выберите получателей")
	}
	if b.Audience.Segment == "plan" && b.Audience.PlanID == 0 {
		return userErr("выберите тариф")
	}
	switch b.Channel {
	case "telegram", "email", "all":
	default:
		return userErr("выберите канал")
	}
	if b.Title == "" {
		b.Title = truncate(stripTags(strings.Split(b.Body, "\n")[0]), 80)
	}
	for _, btn := range b.Buttons {
		if strings.TrimSpace(btn.Text) == "" || !(strings.HasPrefix(btn.URL, "https://") || strings.HasPrefix(btn.URL, "/")) {
			return userErr("у кнопки нужен текст и ссылка https:// или путь кабинета /...")
		}
	}
	return nil
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func stripTags(s string) string { return tagRe.ReplaceAllString(s, "") }

func (s *Service) broadcastMessage(ctx context.Context, c *store.Customer, b *store.Broadcast) Message {
	m := Message{Text: s.renderCustomerText(ctx, c, b.Body, nil), Photo: s.absURL(b.Image), EmailSubject: b.Title}
	for _, btn := range b.Buttons {
		m.Buttons = append(m.Buttons, Button{Text: btn.Text, URL: s.absURL(btn.URL)})
	}
	return m
}

// TestBroadcast sends the broadcast to the admin only.
func (s *Service) TestBroadcast(ctx context.Context, admin *store.Customer, b *store.Broadcast) error {
	if err := s.ValidateBroadcast(b); err != nil {
		return err
	}
	ch := b.Channel
	if ch == "all" {
		ch = "auto"
	}
	if err := s.Notify.Deliver(ctx, admin, s.broadcastMessage(ctx, admin, b), ch); err != nil {
		return userErr("не удалось отправить тест: " + err.Error())
	}
	return nil
}

// SubmitBroadcast queues a broadcast now or at its scheduled time.
func (s *Service) SubmitBroadcast(ctx context.Context, admin *store.Customer, b *store.Broadcast) (*store.Broadcast, error) {
	if err := s.ValidateBroadcast(b); err != nil {
		return nil, err
	}
	if b.ScheduledAt != nil && b.ScheduledAt.Before(time.Now().Add(-time.Minute)) {
		return nil, userErr("время отправки уже прошло")
	}
	b.Status = "scheduled"
	b.CreatedBy = &admin.ID
	if err := store.SaveBroadcast(ctx, s.Store.Pool, b); err != nil {
		return nil, err
	}
	n, err := store.CountAudience(ctx, s.Store.Pool, b.Audience, b.Channel)
	if err == nil {
		_ = store.SetBroadcastTotal(ctx, s.Store.Pool, b.ID, n)
		b.Total = n
	}
	return b, nil
}

func (s *Service) SaveBroadcastDraft(ctx context.Context, admin *store.Customer, b *store.Broadcast) (*store.Broadcast, error) {
	b.Status = "draft"
	b.CreatedBy = &admin.ID
	if b.Audience.Segment == "" {
		b.Audience.Segment = "all"
	}
	if b.Channel == "" {
		b.Channel = "telegram"
	}
	if err := store.SaveBroadcast(ctx, s.Store.Pool, b); err != nil {
		return nil, err
	}
	return b, nil
}

// ProcessBroadcasts sends one batch of the current broadcast; the rate limit comes from settings.
func (s *Service) ProcessBroadcasts(ctx context.Context) {
	if err := store.StartDueBroadcasts(ctx, s.Store.Pool); err != nil {
		s.Log.Error("broadcasts start", "err", err)
		return
	}
	rate := s.set().Broadcasts.RatePerSecond
	if rate < 1 {
		rate = 20
	}
	deadline := time.Now().Add(50 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		b, err := store.SendingBroadcast(ctx, s.Store.Pool)
		if errors.Is(err, store.ErrNotFound) {
			return
		}
		if err != nil {
			s.Log.Error("broadcast", "err", err)
			return
		}
		batch, err := store.AudienceBatch(ctx, s.Store.Pool, b.Audience, b.Channel, b.CursorID, 100)
		if err != nil {
			s.Log.Error("broadcast audience", "err", err)
			return
		}
		if len(batch) == 0 {
			_ = store.FinishBroadcast(ctx, s.Store.Pool, b.ID)
			fresh, _ := store.GetBroadcast(ctx, s.Store.Pool, b.ID)
			if fresh != nil {
				b = fresh
			}
			s.event(ctx, "broadcast_done", nil, map[string]any{"title": b.Title, "sent": b.Sent, "total": b.Total, "failed": b.Failed})
			continue
		}
		tick := time.NewTicker(time.Second / time.Duration(rate))
		sent, failed := 0, 0
		cursor := b.CursorID
		ch := b.Channel
		if ch == "all" {
			ch = "auto"
		}
		for i := range batch {
			c := &batch[i]
			if err := s.Notify.Deliver(ctx, c, s.broadcastMessage(ctx, c, b), ch); err != nil {
				failed++
			} else {
				sent++
			}
			cursor = c.ID
			<-tick.C
			// Save progress every 20 messages so a restart resumes close to where it stopped.
			if (sent+failed)%20 == 0 {
				_ = store.BroadcastProgress(ctx, s.Store.Pool, b.ID, cursor, sent, failed)
				sent, failed = 0, 0
				if cur, err := store.GetBroadcast(ctx, s.Store.Pool, b.ID); err == nil && cur.Status != "sending" {
					tick.Stop()
					return // canceled
				}
			}
		}
		tick.Stop()
		_ = store.BroadcastProgress(ctx, s.Store.Pool, b.ID, cursor, sent, failed)
	}
}

// NewsItem is a broadcast as shown in the cabinet feed.
type NewsItem struct {
	ID      int64                   `json:"id"`
	Title   string                  `json:"title"`
	Body    string                  `json:"body"`
	Image   string                  `json:"image"`
	Buttons []store.BroadcastButton `json:"buttons"`
	Date    time.Time               `json:"date"`
	Unread  bool                    `json:"unread"`
}

func (s *Service) News(ctx context.Context, c *store.Customer) ([]NewsItem, error) {
	list, err := store.News(ctx, s.Store.Pool, 50)
	if err != nil {
		return nil, err
	}
	seen := c.CreatedAt
	if c.NewsSeenAt != nil {
		seen = *c.NewsSeenAt
	}
	out := make([]NewsItem, 0, len(list))
	for _, b := range list {
		item := NewsItem{ID: b.ID, Title: b.Title, Body: s.renderCustomerTextNoPromo(ctx, c, b.Body), Image: b.Image, Buttons: b.Buttons, Date: *b.FinishedAt}
		item.Unread = b.FinishedAt.After(seen)
		out = append(out, item)
	}
	_ = store.MarkNewsSeen(ctx, s.Store.Pool, c.ID)
	return out, nil
}

// renderCustomerTextNoPromo renders variables but never creates promo codes (the feed is re-rendered on every view).
func (s *Service) renderCustomerTextNoPromo(ctx context.Context, c *store.Customer, tpl string) string {
	tpl = promoVarRe.ReplaceAllString(tpl, "")
	return s.renderCustomerText(ctx, c, tpl, nil)
}

func (s *Service) BroadcastAudienceSize(ctx context.Context, a store.BroadcastAudience, channel string) (int, error) {
	return store.CountAudience(ctx, s.Store.Pool, a, channel)
}

// Segments for the admin UI.
func Segments() map[string]string { return segments }
