package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"bshop/internal/config"
	"bshop/internal/service"

	tg "github.com/go-telegram/bot"
)

type sendLog struct {
	mu    sync.Mutex
	calls int
	waits []time.Duration
	warns []string
}

func testQueue(l *sendLog, answers ...error) *adminQueue {
	q := newAdminQueue(func(context.Context, *tg.SendMessageParams) error {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.calls++
		if l.calls <= len(answers) {
			return answers[l.calls-1]
		}
		return nil
	}, func(msg string, args ...any) {
		l.mu.Lock()
		l.warns = append(l.warns, msg+" "+fmt.Sprint(args...))
		l.mu.Unlock()
	})
	q.sleep = func(_ context.Context, d time.Duration) bool {
		l.mu.Lock()
		l.waits = append(l.waits, d)
		l.mu.Unlock()
		return true
	}
	return q
}

func TestAdminQueueRetriesRateLimitAsTelegramSays(t *testing.T) {
	l := &sendLog{}
	q := testQueue(l, &tg.TooManyRequestsError{Message: "Too Many Requests", RetryAfter: 7})
	q.deliver(context.Background(), adminJob{n: service.AdminNote{Event: "purchase"}}, &tg.SendMessageParams{})
	if l.calls != 2 || len(l.waits) != 1 || l.waits[0] != 7*time.Second {
		t.Fatalf("calls %d waits %v", l.calls, l.waits)
	}
}

func TestAdminQueueGivesUpAfterBoundedRetries(t *testing.T) {
	l := &sendLog{}
	net := errors.New("dial tcp: connection refused")
	q := testQueue(l, net, net, net, net, net, net)
	q.deliver(context.Background(), adminJob{n: service.AdminNote{Event: "healthcheck"}}, &tg.SendMessageParams{})
	if l.calls != adminMaxTries {
		t.Fatalf("expected %d attempts, got %d", adminMaxTries, l.calls)
	}
	if len(l.warns) != 1 || !strings.Contains(l.warns[0], "gave up") {
		t.Fatalf("warns %v", l.warns)
	}
}

func TestAdminQueueDoesNotRetryPermanentErrors(t *testing.T) {
	l := &sendLog{}
	q := testQueue(l, fmt.Errorf("%w, chat not found", tg.ErrorBadRequest))
	q.deliver(context.Background(), adminJob{n: service.AdminNote{Event: "trial"}}, &tg.SendMessageParams{})
	if l.calls != 1 || len(l.waits) != 0 {
		t.Fatalf("calls %d waits %v", l.calls, l.waits)
	}
}

func TestAdminQueueRedactsTokenInLogs(t *testing.T) {
	l := &sendLog{}
	tok := "123456789:AAH" + strings.Repeat("x", 32)
	q := testQueue(l, fmt.Errorf("%w: Post https://api.telegram.org/bot%s/sendMessage", tg.ErrorForbidden, tok))
	q.deliver(context.Background(), adminJob{}, &tg.SendMessageParams{})
	if strings.Contains(strings.Join(l.warns, " "), tok) {
		t.Fatalf("token leaked: %v", l.warns)
	}
}

func TestAdminQueuePushNeverBlocks(t *testing.T) {
	l := &sendLog{}
	q := testQueue(l)
	done := make(chan struct{})
	go func() {
		for i := 0; i < adminQueueSize+5; i++ {
			q.push(adminJob{})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("push blocked on a full queue")
	}
	if len(l.warns) != 5 {
		t.Fatalf("expected 5 dropped warnings, got %d", len(l.warns))
	}
}

func TestAdminQueueStopsOnShutdown(t *testing.T) {
	l := &sendLog{}
	q := testQueue(l)
	q.sleep = sleepCtx
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		q.run(ctx, func(adminJob) *tg.SendMessageParams { return &tg.SendMessageParams{} })
		close(stopped)
	}()
	q.push(adminJob{})
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("queue did not stop")
	}
	if l.calls != 1 {
		t.Fatalf("queued note not sent: %d", l.calls)
	}
}

func TestAdminKeyboard(t *testing.T) {
	b := &Bot{svc: &service.Service{Cfg: &config.Config{PublicURL: "https://shop.example"}}, Username: "bobrshop_bot"}
	n := service.AdminNote{ButtonText: "Открыть профиль", ButtonPath: "/admin/customers/42"}

	// channel / group: web-app buttons are not allowed — Mini App link + browser link
	kb := b.adminKeyboard(n, false)
	if len(kb.InlineKeyboard) != 2 || kb.InlineKeyboard[0][0].URL != "https://t.me/bobrshop_bot?startapp=ac_42" ||
		kb.InlineKeyboard[1][0].URL != "https://shop.example/admin/customers/42" || kb.InlineKeyboard[1][0].Text != "Открыть в браузере" {
		t.Fatalf("group keyboard %+v", kb.InlineKeyboard)
	}
	// private chat with an admin: web-app button + browser link
	kb = b.adminKeyboard(service.AdminNote{ButtonText: "Открыть чат", ButtonPath: "/admin/tickets/7"}, true)
	if kb.InlineKeyboard[0][0].WebApp == nil || kb.InlineKeyboard[0][0].WebApp.URL != "https://shop.example/admin/tickets/7" || len(kb.InlineKeyboard) != 2 {
		t.Fatalf("private keyboard %+v", kb.InlineKeyboard)
	}
	if b.adminKeyboard(service.AdminNote{}, false) != nil {
		t.Fatal("no path — no buttons")
	}
}
