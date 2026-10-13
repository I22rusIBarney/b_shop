// Package bot runs the Telegram bot: /start, opening the web app, and delivering notifications.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"bshop/internal/auth"
	"bshop/internal/service"
	"bshop/internal/store"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type Bot struct {
	svc      *service.Service
	tg       *tg.Bot
	log      *slog.Logger
	Username string
}

func New(token string, svc *service.Service, log *slog.Logger) (*Bot, error) {
	b := &Bot{svc: svc, log: log}
	t, err := tg.New(token,
		tg.WithDefaultHandler(b.handle),
		tg.WithAllowedUpdates(tg.AllowedUpdates{models.AllowedUpdateMessage, models.AllowedUpdateCallbackQuery, models.AllowedUpdateMyChatMember}),
		tg.WithErrorsHandler(func(err error) { log.Warn("telegram", "err", err) }),
	)
	if err != nil {
		return nil, err
	}
	b.tg = t
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	me, err := t.GetMe(ctx)
	if err != nil {
		return nil, fmt.Errorf("telegram getMe: %w", err)
	}
	b.Username = me.Username
	return b, nil
}

// Run polls updates until ctx is done.
func (b *Bot) Run(ctx context.Context) {
	_, _ = b.tg.DeleteWebhook(ctx, &tg.DeleteWebhookParams{})
	b.tg.Start(ctx)
}

func (b *Bot) webAppURL(path string) string { return b.svc.Cfg.PublicURL + path }

func (b *Bot) handle(ctx context.Context, _ *tg.Bot, u *models.Update) {
	switch {
	case u.MyChatMember != nil:
		m := u.MyChatMember
		if m.Chat.Type != models.ChatTypePrivate {
			return
		}
		blocked := m.NewChatMember.Type == models.ChatMemberTypeBanned || m.NewChatMember.Type == models.ChatMemberTypeLeft
		_ = store.SetBotBlocked(ctx, b.svc.Store.Pool, m.From.ID, blocked)
	case u.Message != nil && u.Message.Chat.Type == models.ChatTypePrivate && u.Message.From != nil:
		b.onMessage(ctx, u.Message)
	case u.CallbackQuery != nil:
		_, _ = b.tg.AnswerCallbackQuery(ctx, &tg.AnswerCallbackQueryParams{CallbackQueryID: u.CallbackQuery.ID})
	}
}

func (b *Bot) onMessage(ctx context.Context, m *models.Message) {
	text := strings.TrimSpace(m.Text)
	start := ""
	if strings.HasPrefix(text, "/start") {
		start = strings.TrimSpace(strings.TrimPrefix(text, "/start"))
	}
	f := m.From
	p := &auth.Profile{Provider: "telegram", Subject: strconv.FormatInt(f.ID, 10), Username: f.Username,
		FirstName: f.FirstName, LastName: f.LastName, Language: f.LanguageCode, StartParam: start}
	p.Display = strings.TrimSpace(f.FirstName + " " + f.LastName)
	if f.Username != "" {
		p.Display = "@" + f.Username
	}
	c, _, err := b.svc.TelegramBotUser(ctx, p)
	if err != nil {
		b.log.Error("bot user", "err", err)
		return
	}
	for _, id := range b.svc.Settings.Get().General.BlockedTelegramIDs {
		if id == f.ID {
			return
		}
	}
	if c.IsBlocked {
		return
	}
	b.sendMenu(ctx, f.ID, c)
}

func (b *Bot) sendMenu(ctx context.Context, chatID int64, c *store.Customer) {
	st := b.svc.Settings.Get()
	text := fmt.Sprintf("👋 Привет! Это бот <b>%s</b>.\n\nОткройте личный кабинет, чтобы оформить подписку, подключить устройства и управлять тарифом.",
		esc(st.General.ShopName))
	rows := [][]models.InlineKeyboardButton{
		{{Text: "🦫 Личный кабинет", WebApp: &models.WebAppInfo{URL: b.webAppURL("/")}}},
	}
	if st.Trial.Enabled && !c.TrialUsed {
		rows = append(rows, []models.InlineKeyboardButton{{Text: fmt.Sprintf("🎁 Попробовать бесплатно (%d дн.)", st.Trial.Days),
			WebApp: &models.WebAppInfo{URL: b.webAppURL("/?trial=1")}}})
	}
	_, err := b.tg.SendMessage(ctx, &tg.SendMessageParams{ChatID: chatID, Text: text, ParseMode: models.ParseModeHTML,
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: rows}})
	b.checkBlocked(ctx, chatID, err)
}

func (b *Bot) checkBlocked(ctx context.Context, chatID int64, err error) {
	if err != nil && errors.Is(err, tg.ErrorForbidden) {
		_ = store.SetBotBlocked(ctx, b.svc.Store.Pool, chatID, true)
	}
}

func esc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// ---- Notifier ----

// Customer delivers a message by Telegram, falling back to email.
func (b *Bot) Customer(ctx context.Context, c *store.Customer, msg service.Message) {
	if c.TelegramID != nil && !c.BotBlocked {
		params := &tg.SendMessageParams{ChatID: *c.TelegramID, Text: msg.Text, ParseMode: models.ParseModeHTML,
			LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tg.True()}}
		if msg.ButtonURL != "" {
			btn := models.InlineKeyboardButton{Text: msg.ButtonText}
			if strings.HasPrefix(msg.ButtonURL, b.svc.Cfg.PublicURL) {
				btn.WebApp = &models.WebAppInfo{URL: msg.ButtonURL}
			} else {
				btn.URL = msg.ButtonURL
			}
			params.ReplyMarkup = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{btn}}}
		}
		_, err := b.tg.SendMessage(ctx, params)
		if err == nil {
			return
		}
		b.checkBlocked(ctx, *c.TelegramID, err)
		b.log.Warn("notify telegram", "customer", c.ID, "err", err)
	}
	if c.Email != nil && b.svc.Mailer.Configured() {
		subject := msg.EmailSubject
		if subject == "" {
			subject = b.svc.Settings.Get().General.ShopName
		}
		html := "<div style=\"font-family:sans-serif;font-size:15px\">" + strings.ReplaceAll(msg.Text, "\n", "<br>")
		if msg.ButtonURL != "" {
			html += fmt.Sprintf(`<p><a href="%s">%s</a></p>`, msg.ButtonURL, esc(msg.ButtonText))
		}
		html += "</div>"
		if err := b.svc.Mailer.Send(ctx, *c.Email, subject, html); err != nil {
			b.log.Warn("notify email", "customer", c.ID, "err", err)
		}
	}
}

// Admin sends an event to admins. Per-event chats/topics come with the notifications settings section.
func (b *Bot) Admin(ctx context.Context, event, text string, customerID int64) {
	var markup models.ReplyMarkup
	if customerID > 0 {
		markup = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
			{Text: "Открыть клиента", WebApp: &models.WebAppInfo{URL: b.webAppURL(fmt.Sprintf("/admin/customers/%d", customerID))}},
		}}}
	}
	for _, id := range b.svc.Cfg.AdminTelegram {
		_, err := b.tg.SendMessage(ctx, &tg.SendMessageParams{ChatID: id, Text: text, ParseMode: models.ParseModeHTML,
			ReplyMarkup: markup, LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tg.True()}})
		if err != nil {
			b.log.Warn("notify admin", "event", event, "err", err)
		}
	}
}

// LogNotifier is used when no bot token is configured (development).
type LogNotifier struct{ Log *slog.Logger }

func (l LogNotifier) Customer(_ context.Context, c *store.Customer, m service.Message) {
	l.Log.Info("notify customer", "customer", c.ID, "text", m.Text)
}

func (l LogNotifier) Admin(_ context.Context, event, text string, customerID int64) {
	l.Log.Info("notify admin", "event", event, "customer", customerID, "text", text)
}
