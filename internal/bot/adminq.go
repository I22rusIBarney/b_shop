package bot

import (
	"context"
	"errors"
	"regexp"
	"time"

	"bshop/internal/service"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Admin notifications go through a queue: the code that raised the event (a payment, a webhook, a request)
// never waits for Telegram, and a rate limit or a short outage is retried instead of losing the message.
// The queue lives in memory: what is still queued at shutdown is lost (it is logged).

const (
	adminQueueSize = 1000
	adminMaxTries  = 4
	adminPace      = 60 * time.Millisecond // ~16 msg/s overall, below the Bot API limit of 30/s
)

type adminJob struct {
	n     service.AdminNote
	chat  int64
	tries int
}

// adminSender is the Telegram call, replaceable in tests.
type adminSender func(ctx context.Context, p *tg.SendMessageParams) error

type adminQueue struct {
	jobs  chan adminJob
	send  adminSender
	sleep func(ctx context.Context, d time.Duration) bool
	warn  func(msg string, args ...any)
}

func newAdminQueue(send adminSender, warn func(string, ...any)) *adminQueue {
	return &adminQueue{jobs: make(chan adminJob, adminQueueSize), send: send, sleep: sleepCtx, warn: warn}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// push never blocks: when the queue is full (Telegram down for a long time) the note is dropped and logged.
func (q *adminQueue) push(j adminJob) {
	select {
	case q.jobs <- j:
	default:
		q.warn("admin queue full, notification dropped", "event", j.n.Event, "chat", j.chat)
	}
}

// run delivers queued notes until ctx is done.
func (q *adminQueue) run(ctx context.Context, params func(adminJob) *tg.SendMessageParams) {
	for {
		select {
		case <-ctx.Done():
			if n := len(q.jobs); n > 0 {
				q.warn("admin notifications not sent at shutdown", "count", n)
			}
			return
		case j := <-q.jobs:
			q.deliver(ctx, j, params(j))
			if !q.sleep(ctx, adminPace) {
				return
			}
		}
	}
}

// deliver sends one note with bounded retries: 429 waits as Telegram says, network and 5xx errors back off,
// other client errors (blocked, wrong chat, bad HTML) are final.
func (q *adminQueue) deliver(ctx context.Context, j adminJob, p *tg.SendMessageParams) {
	for {
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := q.send(sctx, p)
		cancel()
		if err == nil {
			return
		}
		j.tries++
		var wait time.Duration
		var tooMany *tg.TooManyRequestsError
		switch {
		case errors.As(err, &tooMany):
			wait = time.Duration(min(max(tooMany.RetryAfter, 1), 120)) * time.Second
		case permanent(err):
			q.warn("notify admin failed", "event", j.n.Event, "chat", j.chat, "err", redactToken(err.Error()))
			return
		default:
			wait = time.Duration(1<<(2*j.tries)) * time.Second // 4 s, 16 s, 64 s
		}
		if j.tries >= adminMaxTries {
			q.warn("notify admin gave up", "event", j.n.Event, "chat", j.chat, "tries", j.tries, "err", redactToken(err.Error()))
			return
		}
		if !q.sleep(ctx, wait) {
			return
		}
	}
}

func permanent(err error) bool {
	return errors.Is(err, tg.ErrorForbidden) || errors.Is(err, tg.ErrorBadRequest) ||
		errors.Is(err, tg.ErrorUnauthorized) || errors.Is(err, tg.ErrorNotFound)
}

var tokenRe = regexp.MustCompile(`\d{6,}:[A-Za-z0-9_-]{30,}`)

func redactToken(s string) string { return tokenRe.ReplaceAllString(s, "•••") }

// Admin queues an event for its chat/topic, or for every admin in ADMIN_TELEGRAM_IDS.
func (b *Bot) Admin(_ context.Context, n service.AdminNote) {
	targets := []int64{n.ChatID}
	if n.ChatID == 0 {
		targets = b.svc.Cfg.AdminTelegram
	}
	for _, id := range targets {
		b.adminQ.push(adminJob{n: n, chat: id})
	}
}

func (b *Bot) adminParams(j adminJob) *tg.SendMessageParams {
	n := j.n
	p := &tg.SendMessageParams{ChatID: j.chat, Text: n.Text, ParseMode: models.ParseModeHTML,
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: tg.True()}}
	if n.TopicID > 0 && n.ChatID != 0 {
		p.MessageThreadID = n.TopicID
	}
	if kb := b.adminKeyboard(n, j.chat > 0); kb != nil {
		p.ReplyMarkup = kb
	}
	return p
}

var adminPathRe = regexp.MustCompile(`^/admin/(customers|tickets)/(\d+)$`)

// adminKeyboard: like the old bot, "Открыть профиль" / "Открыть чат" opens the Mini App (a web-app button in a
// private chat, a t.me/<bot>?startapp= link in groups and channels, where web-app buttons are not allowed),
// and "Открыть в браузере" opens the same admin page in a browser.
func (b *Bot) adminKeyboard(n service.AdminNote, private bool) *models.InlineKeyboardMarkup {
	if n.ButtonPath == "" {
		return nil
	}
	web := b.webAppURL(n.ButtonPath)
	app := models.InlineKeyboardButton{Text: n.ButtonText}
	switch m := adminPathRe.FindStringSubmatch(n.ButtonPath); {
	case private:
		app.WebApp = &models.WebAppInfo{URL: web}
	case m != nil && b.Username != "":
		app.URL = "https://t.me/" + b.Username + "?startapp=" + map[string]string{"customers": "ac_", "tickets": "at_"}[m[1]] + m[2]
	default:
		app.URL = web
	}
	rows := [][]models.InlineKeyboardButton{{app}}
	if app.URL != web {
		rows = append(rows, []models.InlineKeyboardButton{{Text: "Открыть в браузере", URL: web}})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// Ping implements service.TelegramPinger (health check).
func (b *Bot) Ping(ctx context.Context) error {
	_, err := b.tg.GetMe(ctx)
	if err != nil {
		return errors.New(redactToken(err.Error()))
	}
	return nil
}
