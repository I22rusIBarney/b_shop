package service_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bshop/internal/service"
)

// fakeComp is a component whose answer the test switches.
type fakeComp struct {
	mu    sync.Mutex
	err   error
	calls atomic.Int32
	// failFirst fails only the first attempt of each round (a blip the retry must absorb).
	failFirst bool
	block     chan struct{} // when set, the check waits for it or for the timeout
}

func (f *fakeComp) set(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }

func (f *fakeComp) run(ctx context.Context) error {
	n := f.calls.Add(1)
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.failFirst && n%2 == 1 {
		return errors.New("blip")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func healthNotes(e *env) []string {
	e.notes.mu.Lock()
	defer e.notes.mu.Unlock()
	var out []string
	for _, n := range e.notes.adminNotes {
		if n.Event == "healthcheck" {
			out = append(out, n.Text)
		}
	}
	return out
}

func healthEnv(t *testing.T, comps map[string]*fakeComp) (*env, *time.Time) {
	e := setup(t)
	service.SetHealthRetryPause(50 * time.Millisecond)
	mustSet(t, e, "health", `{"enabled":true,"interval_sec":30,"timeout_sec":2,"fail_threshold":2,"remind_minutes":60,"notify_recovery":true}`)
	for name, c := range comps {
		e.svc.HealthChecksOverride = append(e.svc.HealthChecksOverride, service.HealthCheck{Name: name, Run: c.run})
	}
	now := time.Date(2026, 10, 19, 3, 50, 25, 0, time.UTC)
	e.svc.HealthClock = func() time.Time { return now }
	return e, &now
}

func TestHealthDownOnlyAfterThresholdThenRecovered(t *testing.T) {
	db, smtp := &fakeComp{}, &fakeComp{}
	e, now := healthEnv(t, map[string]*fakeComp{"postgres": db, "smtp": smtp})
	ctx := context.Background()

	e.svc.RunHealth(ctx)
	if n := healthNotes(e); len(n) != 0 {
		t.Fatalf("all up must be silent, got %v", n)
	}

	smtp.set(errors.New("smtp connect: i/o timeout"))
	*now = now.Add(time.Minute)
	e.svc.RunHealth(ctx)
	if n := healthNotes(e); len(n) != 0 {
		t.Fatalf("one failed round is below the threshold, got %v", n)
	}
	*now = now.Add(time.Minute)
	e.svc.RunHealth(ctx)
	n := healthNotes(e)
	if len(n) != 1 || !strings.Contains(n[0], "<b>smtp</b> — недоступен") || !strings.Contains(n[0], "1/2 в работе") {
		t.Fatalf("expected one down report, got %v", n)
	}
	// Telegram HTML: a bare "<1ms" is an unknown tag and the whole message would be rejected
	if bad := badTelegramHTML(n[0]); bad != "" || !strings.Contains(n[0], "&lt;1ms") {
		t.Fatalf("latency not escaped: %s", n[0])
	}
	st := e.svc.HealthStatus()
	if st.Up != 1 || st.Total != 2 {
		t.Fatalf("status %+v", st)
	}

	// still down, reminder not due yet: silence
	*now = now.Add(10 * time.Minute)
	e.svc.RunHealth(ctx)
	if n := healthNotes(e); len(n) != 1 {
		t.Fatalf("repeated failure must not repeat the report, got %d", len(n))
	}
	// an hour after the report: one reminder
	*now = now.Add(55 * time.Minute)
	e.svc.RunHealth(ctx)
	n = healthNotes(e)
	if len(n) != 2 || !strings.Contains(n[1], "всё ещё недоступен") {
		t.Fatalf("expected a reminder, got %v", n)
	}

	smtp.set(nil)
	*now = now.Add(time.Minute)
	e.svc.RunHealth(ctx)
	n = healthNotes(e)
	if len(n) != 3 || !strings.Contains(n[2], "<b>smtp</b> — восстановлен") || !strings.Contains(n[2], "был недоступен 1 ч 7 мин") ||
		!strings.Contains(n[2], "2/2 в работе") {
		t.Fatalf("expected a recovery report, got %v", n[len(n)-1])
	}

	// fails again soon after: the report says it is a repeat
	smtp.set(errors.New("smtp auth: 535"))
	for i := 0; i < 2; i++ {
		*now = now.Add(time.Minute)
		e.svc.RunHealth(ctx)
	}
	n = healthNotes(e)
	if len(n) != 4 || !strings.Contains(n[3], "снова, восстанавливался") {
		t.Fatalf("expected a repeat-failure report, got %v", n[len(n)-1])
	}
}

func TestHealthCommonOutageIsOneMessage(t *testing.T) {
	a, b, c := &fakeComp{}, &fakeComp{}, &fakeComp{}
	e, now := healthEnv(t, map[string]*fakeComp{"postgres": a, "remnawave": b, "yookassa": c})
	for _, x := range []*fakeComp{a, b, c} {
		x.set(errors.New("network is unreachable"))
	}
	for i := 0; i < 2; i++ {
		*now = now.Add(time.Minute)
		e.svc.RunHealth(context.Background())
	}
	n := healthNotes(e)
	if len(n) != 1 || strings.Count(n[0], "— недоступен") != 3 || !strings.Contains(n[0], "0/3 в работе") {
		t.Fatalf("three failures must be one report, got %v", n)
	}
}

func TestHealthRetryAbsorbsBlip(t *testing.T) {
	c := &fakeComp{failFirst: true}
	e, now := healthEnv(t, map[string]*fakeComp{"remnawave": c})
	for i := 0; i < 3; i++ {
		*now = now.Add(time.Minute)
		e.svc.RunHealth(context.Background())
	}
	if st := e.svc.HealthStatus(); st.Up != 1 || st.Components[0].Fails != 0 {
		t.Fatalf("a failed first attempt followed by a good retry is healthy: %+v", st)
	}
	if n := healthNotes(e); len(n) != 0 {
		t.Fatalf("no reports expected, got %v", n)
	}
}

func TestHealthTimeoutAndSecretsRedacted(t *testing.T) {
	hang := &fakeComp{block: make(chan struct{})}
	leak := &fakeComp{}
	e, now := healthEnv(t, map[string]*fakeComp{"remnawave": hang, "telegram_bot_api": leak})
	leak.set(errors.New(`Post "https://api.telegram.org/bot` + botToken + `/getMe": dial tcp: timeout`))
	start := time.Now()
	for i := 0; i < 2; i++ {
		*now = now.Add(time.Minute)
		e.svc.RunHealth(context.Background())
	}
	// 2 rounds × two 2 s attempts — must not hang on the stuck check
	if took := time.Since(start); took > 20*time.Second {
		t.Fatalf("round took %s", took)
	}
	n := healthNotes(e)
	if len(n) != 1 {
		t.Fatalf("expected one report, got %v", n)
	}
	if !strings.Contains(n[0], "нет ответа за 2 с") {
		t.Fatalf("timeout must be named: %s", n[0])
	}
	if strings.Contains(n[0], botToken) || strings.Contains(n[0], "TEST-TOKEN") {
		t.Fatalf("bot token leaked: %s", n[0])
	}
	for _, c := range e.svc.HealthStatus().Components {
		if strings.Contains(c.Error, "TEST-TOKEN") {
			t.Fatalf("token in status: %s", c.Error)
		}
	}
}

func TestHealthNoParallelRounds(t *testing.T) {
	slow := &fakeComp{block: make(chan struct{})}
	e, _ := healthEnv(t, map[string]*fakeComp{"postgres": slow})
	done := make(chan bool)
	go func() { done <- e.svc.RunHealth(context.Background()) }()
	for slow.calls.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if e.svc.RunHealth(context.Background()) {
		t.Fatal("a second round started while the first is running")
	}
	close(slow.block)
	if !<-done {
		t.Fatal("first round did not run")
	}
}

func TestHealthLoopStopsOnShutdown(t *testing.T) {
	e, _ := healthEnv(t, map[string]*fakeComp{"postgres": {}})
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { e.svc.RunHealthLoop(ctx); close(stopped) }()
	cancel()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("health loop did not stop")
	}
	// an interrupted round reports nothing
	hang := &fakeComp{block: make(chan struct{})}
	e.svc.HealthChecksOverride = []service.HealthCheck{{Name: "remnawave", Run: hang.run}}
	ctx2, cancel2 := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel2() }()
	e.svc.RunHealth(ctx2)
	if n := healthNotes(e); len(n) != 0 {
		t.Fatalf("shutdown must not produce reports, got %v", n)
	}
}

func TestHealthSettingsValidation(t *testing.T) {
	e := setup(t)
	for _, bad := range []string{
		`{"enabled":true,"interval_sec":10,"timeout_sec":2,"fail_threshold":2}`,
		`{"enabled":true,"interval_sec":30,"timeout_sec":20,"fail_threshold":2}`,
		`{"enabled":true,"interval_sec":60,"timeout_sec":10,"fail_threshold":0}`,
		`{"enabled":true,"interval_sec":60,"timeout_sec":10,"fail_threshold":2,"remind_minutes":-1}`,
	} {
		if err := e.svc.Settings.Update(context.Background(), "health", []byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
