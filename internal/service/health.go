package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"bshop/internal/moynalog"
	"bshop/internal/yookassa"
)

// Health check (old bot: "Healthcheck — 6/6 up"): every round checks the database, the Telegram Bot API,
// Remnawave, the enabled integrations and the background jobs. A component is reported down only after
// settings.health.fail_threshold failed rounds in a row (each round already retries a failed attempt once),
// reminded about every remind_minutes while it stays down, and reported again when it is back.
// All changes of one round go into one message, so a common outage is one report, not a burst.

// TelegramPinger checks that the Bot API answers (getMe); nil without a bot.
type TelegramPinger interface {
	Ping(ctx context.Context) error
}

// HealthCheck is one component check.
type HealthCheck struct {
	Name string
	Run  func(ctx context.Context) error
}

// ComponentHealth is the last known state of a component (admin status page).
type ComponentHealth struct {
	Name      string     `json:"name"`
	Up        bool       `json:"up"`
	Latency   int64      `json:"latency_ms"`
	Error     string     `json:"error,omitempty"`
	Fails     int        `json:"fails"` // failed rounds in a row
	Down      bool       `json:"down"`  // reported down (threshold reached)
	Since     *time.Time `json:"since"` // first failure of the current problem
	CheckedAt time.Time  `json:"checked_at"`
}

// HealthReport is what GET /api/admin/health returns.
type HealthReport struct {
	Enabled    bool              `json:"enabled"`
	CheckedAt  *time.Time        `json:"checked_at"`
	Up         int               `json:"up"`
	Total      int               `json:"total"`
	Components []ComponentHealth `json:"components"`
}

type compState struct {
	ComponentHealth
	notifiedAt    time.Time // last down/reminder report
	recoveredAt   time.Time // last recovery, to mark a repeat failure
	lastLatencyOK time.Duration
}

type healthState struct {
	run     sync.Mutex // one round at a time (scheduled and "check now")
	mu      sync.Mutex
	comps   map[string]*compState
	checked time.Time

	wmu     sync.Mutex
	workers map[string]workerBeat
	started time.Time
}

type workerBeat struct {
	every   time.Duration
	started time.Time // start of the last run
	ended   time.Time // end of the last run (zero while running)
}

// beat records a background job run for the "workers" component.
func (s *Service) beat(name string, every time.Duration, start bool) {
	h := &s.health
	h.wmu.Lock()
	defer h.wmu.Unlock()
	if h.workers == nil {
		h.workers = map[string]workerBeat{}
		h.started = time.Now()
	}
	b := h.workers[name]
	b.every = every
	if start {
		b.started, b.ended = time.Now(), time.Time{}
	} else {
		b.ended = time.Now()
	}
	h.workers[name] = b
}

// stuckWorkers lists jobs that have not finished a run for much longer than their period.
func (s *Service) stuckWorkers(now time.Time) []string {
	h := &s.health
	h.wmu.Lock()
	defer h.wmu.Unlock()
	var out []string
	for name, b := range h.workers {
		// A run may take up to 3 periods (its timeout); allow one more period and at least 5 minutes.
		limit := max(4*b.every, 5*time.Minute)
		if b.ended.IsZero() && now.Sub(b.started) > limit {
			out = append(out, name+" (выполняется "+shortDur(now.Sub(b.started))+")")
		} else if !b.ended.IsZero() && now.Sub(b.ended) > limit+b.every {
			out = append(out, name+" (не запускался "+shortDur(now.Sub(b.ended))+")")
		}
	}
	sort.Strings(out)
	return out
}

// healthChecks lists the components present in this installation.
func (s *Service) healthChecks() []HealthCheck {
	if s.HealthChecksOverride != nil {
		return s.HealthChecksOverride
	}
	st := s.set()
	checks := []HealthCheck{{"postgres", func(ctx context.Context) error {
		var one int
		return s.Store.Pool.QueryRow(ctx, `SELECT 1`).Scan(&one)
	}}}
	if s.Telegram != nil {
		checks = append(checks, HealthCheck{"telegram_bot_api", s.Telegram.Ping})
	}
	if s.Panel != nil && s.Cfg.RemnawaveURL != "" {
		checks = append(checks, HealthCheck{"remnawave", func(ctx context.Context) error {
			_, err := s.Panel.InternalSquads(ctx)
			return err
		}})
	}
	if y := st.YooKassa; y.Enabled && y.ShopID != "" && y.SecretKey != "" {
		checks = append(checks, HealthCheck{"yookassa", func(ctx context.Context) error {
			return yookassa.New(y.APIURL, y.ShopID, y.SecretKey).Ping(ctx)
		}})
	}
	if m := st.MoyNalog; m.Enabled {
		checks = append(checks, HealthCheck{"moynalog", func(ctx context.Context) error {
			return moynalog.New(m.APIURL, "", "").Reachable(ctx)
		}})
	}
	if s.Mailer != nil && s.Mailer.Configured() {
		checks = append(checks, HealthCheck{"smtp", s.Mailer.Check})
	}
	checks = append(checks, HealthCheck{"workers", func(ctx context.Context) error {
		if stuck := s.stuckWorkers(time.Now()); len(stuck) > 0 {
			return errors.New("фоновые задачи зависли: " + strings.Join(stuck, ", "))
		}
		return nil
	}})
	return checks
}

// healthRetryPause separates the two attempts of a round (shortened in tests).
var healthRetryPause = 2 * time.Second

// probe runs a check with a timeout and one retry after a short pause, so a single blip is not a failure.
func probe(ctx context.Context, c HealthCheck, timeout time.Duration) (time.Duration, error) {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(healthRetryPause):
			}
		}
		actx, cancel := context.WithTimeout(ctx, timeout)
		start := time.Now()
		err = safeRun(actx, c.Run)
		took := time.Since(start)
		timedOut := errors.Is(actx.Err(), context.DeadlineExceeded)
		cancel()
		if err == nil {
			return took, nil
		}
		if timedOut {
			err = fmt.Errorf("нет ответа за %s", shortDur(timeout))
		}
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
	}
	return 0, err
}

func safeRun(ctx context.Context, fn func(context.Context) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("проверка упала: %v", r)
		}
	}()
	return fn(ctx)
}

var botTokenRe = regexp.MustCompile(`\d{6,}:[A-Za-z0-9_-]{30,}`)

// Redact removes secrets from an error before it goes to a chat or a log (HTTP client errors include URLs,
// and the Telegram one has the bot token in its path) and shortens it.
func (s *Service) Redact(msg string) string { return truncate(s.stripSecrets(msg), 200) }

// stripSecrets replaces the configured tokens and passwords, and anything shaped like a bot token, with •••.
func (s *Service) stripSecrets(msg string) string {
	st := s.set()
	for _, sec := range []string{s.Cfg.BotToken, s.Cfg.RemnawaveToken, s.Cfg.RemnawaveCookie, s.Cfg.SecretKey,
		st.YooKassa.SecretKey, st.MoyNalog.Password, st.SMTP.Password, st.AI.APIKey, st.Webhooks.Secret} {
		if len(sec) >= 6 {
			msg = strings.ReplaceAll(msg, sec, "•••")
		}
	}
	return botTokenRe.ReplaceAllString(msg, "•••")
}

// RunHealth is one round; it returns false if another round is still running.
func (s *Service) RunHealth(ctx context.Context) bool {
	h := &s.health
	if !h.run.TryLock() {
		return false
	}
	defer h.run.Unlock()
	set := s.set().Health
	checks := s.healthChecks()

	type result struct {
		took time.Duration
		err  error
	}
	res := make([]result, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			took, err := probe(ctx, c, time.Duration(set.TimeoutSec)*time.Second)
			res[i] = result{took, err}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return true // shutting down: an interrupted round says nothing about the components
	}

	now := time.Now()
	if s.HealthClock != nil {
		now = s.HealthClock()
	}
	remind := time.Duration(set.RemindMinutes) * time.Minute
	var issues []string
	h.mu.Lock()
	if h.comps == nil {
		h.comps = map[string]*compState{}
	}
	seen := map[string]bool{}
	for i, c := range checks {
		seen[c.Name] = true
		st := h.comps[c.Name]
		if st == nil {
			st = &compState{ComponentHealth: ComponentHealth{Name: c.Name, Up: true}}
			h.comps[c.Name] = st
		}
		st.CheckedAt = now
		if err := res[i].err; err == nil {
			st.Latency = res[i].took.Milliseconds()
			st.lastLatencyOK = res[i].took
			if st.Down && set.NotifyRecovery {
				issues = append(issues, fmt.Sprintf("✅ <b>%s</b> — восстановлен (%s) · был недоступен %s (с %s)",
					c.Name, escape(ms(res[i].took)), shortDur(now.Sub(*st.Since)), s.clock(*st.Since)))
			}
			if st.Down {
				st.recoveredAt = now
			}
			st.Up, st.Down, st.Fails, st.Error, st.Since = true, false, 0, "", nil
			continue
		}
		st.Up = false
		st.Error = s.Redact(res[i].err.Error())
		st.Fails++
		if st.Since == nil {
			t := now
			st.Since = &t
		}
		switch {
		case !st.Down && st.Fails >= set.FailThreshold:
			st.Down, st.notifiedAt = true, now
			again := ""
			if !st.recoveredAt.IsZero() && now.Sub(st.recoveredAt) < time.Hour {
				again = fmt.Sprintf(" · снова, восстанавливался в %s", s.clock(st.recoveredAt))
			}
			issues = append(issues, fmt.Sprintf("❌ <b>%s</b> — недоступен: %s · с %s%s",
				c.Name, escape(st.Error), s.clock(*st.Since), again))
		case st.Down && remind > 0 && now.Sub(st.notifiedAt) >= remind:
			st.notifiedAt = now
			issues = append(issues, fmt.Sprintf("⏳ <b>%s</b> — всё ещё недоступен (%s): %s",
				c.Name, shortDur(now.Sub(*st.Since)), escape(st.Error)))
		}
	}
	for name := range h.comps { // integration switched off in the settings
		if !seen[name] {
			delete(h.comps, name)
		}
	}
	h.checked = now
	report := s.healthReportLocked()
	h.mu.Unlock()

	if len(issues) > 0 {
		s.event(ctx, "healthcheck", nil, map[string]any{"report": Raw(s.healthText(report, issues))})
	}
	return true
}

// HealthStatus is the last round for the admin page.
func (s *Service) HealthStatus() HealthReport {
	h := &s.health
	h.mu.Lock()
	defer h.mu.Unlock()
	return s.healthReportLocked()
}

func (s *Service) healthReportLocked() HealthReport {
	h := &s.health
	r := HealthReport{Enabled: s.set().Health.Enabled, Components: []ComponentHealth{}}
	if !h.checked.IsZero() {
		t := h.checked
		r.CheckedAt = &t
	}
	for _, c := range h.comps {
		r.Components = append(r.Components, c.ComponentHealth)
		r.Total++
		if c.Up {
			r.Up++
		}
	}
	sort.Slice(r.Components, func(i, j int) bool { return r.Components[i].Name < r.Components[j].Name })
	return r
}

// healthText is the report in the old bot's layout: summary, issues of this round, then every component.
func (s *Service) healthText(r HealthReport, issues []string) string {
	var b strings.Builder
	icon := "✅"
	if r.Up < r.Total {
		icon = "⚠️"
	}
	fmt.Fprintf(&b, "%s <b>Healthcheck</b> — %d/%d в работе\n", icon, r.Up, r.Total)
	if r.CheckedAt != nil {
		fmt.Fprintf(&b, "🕐 <i>Проверка: %s</i>\n", s.stamp(*r.CheckedAt))
	}
	b.WriteString("\n<b>Изменения:</b>\n")
	b.WriteString(strings.Join(issues, "\n"))
	b.WriteString("\n\n")
	for _, c := range r.Components {
		if c.Up {
			fmt.Fprintf(&b, "✅ %s (%s)\n", c.Name, escape(ms(time.Duration(c.Latency)*time.Millisecond)))
		} else {
			fmt.Fprintf(&b, "❌ %s — %s\n", c.Name, escape(c.Error))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// RunHealthLoop runs rounds every settings.health.interval_sec until ctx is done.
func (s *Service) RunHealthLoop(ctx context.Context) {
	// Give the bot and the panel client a moment after start-up before the first round.
	select {
	case <-ctx.Done():
		return
	case <-time.After(20 * time.Second):
	}
	for {
		if s.set().Health.Enabled {
			s.RunHealth(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(s.set().Health.IntervalSec) * time.Second):
		}
	}
}

// ms formats a latency like the old bot: <1ms, 138ms, 3.2s.
func ms(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return "<1ms"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// shortDur: 45 с, 1 мин 5 с, 2 ч 10 мин.
func shortDur(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d с", int(d.Seconds()))
	case d < time.Hour:
		if sec := int(d.Seconds()) % 60; sec > 0 {
			return fmt.Sprintf("%d мин %d с", int(d.Minutes()), sec)
		}
		return fmt.Sprintf("%d мин", int(d.Minutes()))
	}
	if m := int(d.Minutes()) % 60; m > 0 {
		return fmt.Sprintf("%d ч %d мин", int(d.Hours()), m)
	}
	return fmt.Sprintf("%d ч", int(d.Hours()))
}

// stamp / clock: event time in the shop time zone.
func (s *Service) stamp(t time.Time) string {
	return t.In(s.set().General.Location()).Format("2006-01-02 15:04:05")
}

func (s *Service) clock(t time.Time) string {
	return t.In(s.set().General.Location()).Format("15:04:05")
}
