// Package bot runs the Telegram bot: /start, opening the web app, and delivering notifications.
package bot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bshop/internal/auth"
	"bshop/internal/service"
	"bshop/internal/store"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
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
	// The menu button opens the web app at PUBLIC_URL, so a domain change needs no trip to @BotFather.
	if strings.HasPrefix(b.svc.Cfg.PublicURL, "https://") {
		if _, err := b.tg.SetChatMenuButton(ctx, &tg.SetChatMenuButtonParams{MenuButton: models.MenuButtonWebApp{
			Type: models.MenuButtonTypeWebApp, Text: "Открыть", WebApp: models.WebAppInfo{URL: b.webAppURL("/")}}}); err != nil {
			b.log.Warn("set menu button", "err", err)
		}
	}
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
		q := u.CallbackQuery
		if q.Data == checkChannelsData {
			b.onCheckChannels(ctx, q)
			return
		}
		_, _ = b.tg.AnswerCallbackQuery(ctx, &tg.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
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
	isCommand := strings.HasPrefix(text, "/")
	hasContent := text != "" || m.Caption != "" || len(m.Photo) > 0 || m.Document != nil
	if !isCommand && hasContent && b.supportMessage(ctx, m, c) {
		return
	}
	if b.channelGate(ctx, f.ID, c) {
		return
	}
	b.sendMenu(ctx, f.ID, c)
}

// supportMessage files a message written to the bot into the customer's ticket. Returns false if bot chat is off.
func (b *Bot) supportMessage(ctx context.Context, m *models.Message, c *store.Customer) bool {
	sup := b.svc.Settings.Get().Support
	if !sup.Enabled || !sup.BotChat {
		return false
	}
	text := strings.TrimSpace(m.Text)
	if text == "" {
		text = strings.TrimSpace(m.Caption)
	}
	var files []uuid.UUID
	if id, ok := b.saveAttachment(ctx, m, c); ok {
		files = append(files, id)
	}
	ticketID, created, err := b.svc.BotSupportMessage(ctx, c, text, files)
	var reply string
	switch {
	case err != nil:
		var ue *service.UserError
		if errors.As(err, &ue) {
			reply = "⚠️ " + esc(ue.Msg)
		} else {
			b.log.Error("bot support", "err", err)
			reply = "⚠️ Не удалось передать сообщение, попробуйте ещё раз или напишите из кабинета."
		}
	case created:
		reply = fmt.Sprintf("✉️ Обращение <b>#%d</b> создано. Ответ придёт сюда — просто продолжайте писать в этот чат.", ticketID)
	default:
		return true
	}
	_, err = b.tg.SendMessage(ctx, &tg.SendMessageParams{ChatID: m.Chat.ID, Text: reply, ParseMode: models.ParseModeHTML,
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
			{Text: "Открыть обращение", WebApp: &models.WebAppInfo{URL: b.webAppURL(fmt.Sprintf("/support/%d", ticketID))}},
		}}}})
	b.checkBlocked(ctx, m.Chat.ID, err)
	return true
}

func (b *Bot) saveAttachment(ctx context.Context, m *models.Message, c *store.Customer) (uuid.UUID, bool) {
	var fileID, name, mime string
	switch {
	case len(m.Photo) > 0:
		p := m.Photo[len(m.Photo)-1]
		fileID, name, mime = p.FileID, "photo.jpg", "image/jpeg"
	case m.Document != nil:
		fileID, name, mime = m.Document.FileID, m.Document.FileName, m.Document.MimeType
	default:
		return uuid.Nil, false
	}
	f, err := b.tg.GetFile(ctx, &tg.GetFileParams{FileID: fileID})
	if err != nil {
		return uuid.Nil, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.tg.FileDownloadLink(f), nil)
	if err != nil {
		return uuid.Nil, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return uuid.Nil, false
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 21<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		return uuid.Nil, false
	}
	saved, err := b.svc.SaveCustomerFile(ctx, c, name, mime, data)
	if err != nil {
		b.log.Info("bot attachment rejected", "customer", c.ID, "err", err)
		return uuid.Nil, false
	}
	return saved.ID, true
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
	if err := b.Deliver(ctx, c, msg, "auto"); err != nil && !errors.Is(err, service.ErrUnreachable) {
		b.log.Warn("notify customer", "customer", c.ID, "err", err)
	}
}

func (b *Bot) button(text, url string, private bool) models.InlineKeyboardButton {
	btn := models.InlineKeyboardButton{Text: text}
	// Web-app buttons work only in private chats; groups get a plain link.
	if private && strings.HasPrefix(url, b.svc.Cfg.PublicURL) {
		btn.WebApp = &models.WebAppInfo{URL: url}
	} else {
		btn.URL = url
	}
	return btn
}

func (b *Bot) keyboard(msg service.Message) *models.InlineKeyboardMarkup {
	var rows [][]models.InlineKeyboardButton
	if msg.ButtonURL != "" {
		rows = append(rows, []models.InlineKeyboardButton{b.button(msg.ButtonText, msg.ButtonURL, true)})
	}
	for _, x := range msg.Buttons {
		rows = append(rows, []models.InlineKeyboardButton{b.button(x.Text, x.URL, true)})
	}
	if len(rows) == 0 {
		return nil
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// Deliver sends a message through Telegram, email, or Telegram with email fallback ("auto").
func (b *Bot) Deliver(ctx context.Context, c *store.Customer, msg service.Message, channel string) error {
	canTG := c.TelegramID != nil && !c.BotBlocked
	canMail := c.Email != nil && b.svc.Mailer.Configured()
	var tgErr error
	if (channel == "telegram" || channel == "auto") && canTG {
		tgErr = b.sendTelegram(ctx, *c.TelegramID, msg)
		if tgErr == nil {
			return nil
		}
		b.checkBlocked(ctx, *c.TelegramID, tgErr)
		if channel == "telegram" {
			return tgErr
		}
	}
	if (channel == "email" || channel == "auto") && canMail {
		return b.sendEmail(ctx, *c.Email, msg)
	}
	if tgErr != nil {
		return tgErr
	}
	return service.ErrUnreachable
}

func (b *Bot) sendTelegram(ctx context.Context, chatID int64, msg service.Message) error {
	kb := b.keyboard(msg)
	var markup models.ReplyMarkup
	if kb != nil {
		markup = kb
	}
	if msg.Photo != "" {
		caption := msg.Text
		long := len([]rune(caption)) > 1024
		if long {
			caption = ""
		}
		p := &tg.SendPhotoParams{ChatID: chatID, Photo: &models.InputFileString{Data: msg.Photo}, Caption: caption, ParseMode: models.ParseModeHTML}
		if !long {
			p.ReplyMarkup = markup
		}
		if _, err := b.tg.SendPhoto(ctx, p); err != nil {
			return err
		}
		if !long {
			return nil
		}
	}
	_, err := b.tg.SendMessage(ctx, &tg.SendMessageParams{ChatID: chatID, Text: msg.Text, ParseMode: models.ParseModeHTML,
		ReplyMarkup: markup, LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tg.True()}})
	return err
}

func (b *Bot) sendEmail(ctx context.Context, to string, msg service.Message) error {
	subject := msg.EmailSubject
	if subject == "" {
		subject = b.svc.Settings.Get().General.ShopName
	}
	html := "<div style=\"font-family:sans-serif;font-size:15px;line-height:1.5\">"
	if msg.Photo != "" {
		html += fmt.Sprintf(`<p><img src="%s" alt="" style="max-width:100%%;border-radius:12px"></p>`, msg.Photo)
	}
	html += strings.ReplaceAll(msg.Text, "\n", "<br>")
	if msg.ButtonURL != "" {
		html += fmt.Sprintf(`<p><a href="%s">%s</a></p>`, msg.ButtonURL, esc(msg.ButtonText))
	}
	for _, x := range msg.Buttons {
		html += fmt.Sprintf(`<p><a href="%s">%s</a></p>`, x.URL, esc(x.Text))
	}
	html += "</div>"
	return b.svc.Mailer.Send(ctx, to, subject, html)
}

// Admin sends an event to its chat/topic, or to every admin in ADMIN_TELEGRAM_IDS.
func (b *Bot) Admin(ctx context.Context, n service.AdminNote) {
	targets := []int64{n.ChatID}
	if n.ChatID == 0 {
		targets = b.svc.Cfg.AdminTelegram
	}
	for _, id := range targets {
		p := &tg.SendMessageParams{ChatID: id, Text: n.Text, ParseMode: models.ParseModeHTML,
			LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tg.True()}}
		if n.TopicID > 0 && n.ChatID != 0 {
			p.MessageThreadID = n.TopicID
		}
		if n.ButtonPath != "" {
			p.ReplyMarkup = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
				b.button(n.ButtonText, b.webAppURL(n.ButtonPath), id > 0),
			}}}
		}
		if _, err := b.tg.SendMessage(ctx, p); err != nil {
			b.log.Warn("notify admin", "event", n.Event, "chat", id, "err", err)
		}
	}
}

// LogNotifier is used when no bot token is configured (development).
type LogNotifier struct{ Log *slog.Logger }

func (l LogNotifier) Customer(_ context.Context, c *store.Customer, m service.Message) {
	l.Log.Info("notify customer", "customer", c.ID, "text", m.Text)
}

func (l LogNotifier) Deliver(_ context.Context, c *store.Customer, m service.Message, ch string) error {
	l.Log.Info("deliver", "customer", c.ID, "channel", ch, "text", m.Text)
	return nil
}

func (l LogNotifier) Admin(_ context.Context, n service.AdminNote) {
	l.Log.Info("notify admin", "event", n.Event, "chat", n.ChatID, "customer", n.CustomerID, "text", n.Text)
}

// IsChannelMember implements service.ChannelChecker. chatID is -100… or @username.
func (b *Bot) IsChannelMember(ctx context.Context, chatID string, userID int64) (bool, error) {
	var chat any = chatID
	if id, err := strconv.ParseInt(chatID, 10, 64); err == nil {
		chat = id
	}
	m, err := b.tg.GetChatMember(ctx, &tg.GetChatMemberParams{ChatID: chat, UserID: userID})
	if err != nil {
		return false, err
	}
	switch m.Type {
	case models.ChatMemberTypeOwner, models.ChatMemberTypeAdministrator, models.ChatMemberTypeMember:
		return true, nil
	case models.ChatMemberTypeRestricted:
		return m.Restricted != nil && m.Restricted.IsMember, nil
	}
	return false, nil
}

const checkChannelsData = "check_channels"

// channelGate shows the required channels instead of the menu. Returns true if the customer must subscribe first.
func (b *Bot) channelGate(ctx context.Context, chatID int64, c *store.Customer) bool {
	missing := b.svc.MissingChannels(ctx, c)
	if len(missing) == 0 {
		return false
	}
	rows := [][]models.InlineKeyboardButton{}
	for _, ch := range missing {
		rows = append(rows, []models.InlineKeyboardButton{{Text: "📢 " + ch.Title, URL: ch.URL}})
	}
	rows = append(rows, []models.InlineKeyboardButton{{Text: "✅ Я подписался", CallbackData: checkChannelsData}})
	text := "Чтобы пользоваться сервисом, подпишитесь на наш канал. После подписки нажмите «Я подписался»."
	if len(missing) > 1 {
		text = "Чтобы пользоваться сервисом, подпишитесь на наши каналы. После подписки нажмите «Я подписался»."
	}
	_, err := b.tg.SendMessage(ctx, &tg.SendMessageParams{ChatID: chatID, Text: text,
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: rows}})
	b.checkBlocked(ctx, chatID, err)
	return true
}

func (b *Bot) onCheckChannels(ctx context.Context, q *models.CallbackQuery) {
	c, err := store.GetCustomerByTelegram(ctx, b.svc.Store.Pool, q.From.ID)
	if err != nil {
		_, _ = b.tg.AnswerCallbackQuery(ctx, &tg.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
		return
	}
	if len(b.svc.MissingChannels(ctx, c)) > 0 {
		_, _ = b.tg.AnswerCallbackQuery(ctx, &tg.AnswerCallbackQueryParams{CallbackQueryID: q.ID, ShowAlert: true,
			Text: "Подписка пока не видна. Подпишитесь на канал и нажмите ещё раз."})
		return
	}
	_, _ = b.tg.AnswerCallbackQuery(ctx, &tg.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: "Спасибо за подписку!"})
	b.sendMenu(ctx, q.From.ID, c)
}
