package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"bshop/internal/settings"
	"bshop/internal/store"

	"github.com/jackc/pgx/v5"
)

// Step is one message of an automation, in the RWP export format (kept as-is for import/export).
type Step struct {
	StepOrder      int               `json:"step_order"`
	DelayDuration  string            `json:"delay_duration"`
	TemplateID     *int64            `json:"template_id"`
	MessageContent string            `json:"message_content"`
	StopOnPurchase bool              `json:"stop_on_purchase"`
	Buttons        []Button          `json:"buttons"`
	RepeatCount    int               `json:"repeat_count"`
	RepeatInterval *string           `json:"repeat_interval"`
	Slots          []json.RawMessage `json:"slots"`
	WebhookURL     *string           `json:"webhook_url"`
	WebhookMethod  *string           `json:"webhook_method"`
	Image          string            `json:"image,omitempty"`
}

// TriggerInfo describes a trigger for the admin UI.
type TriggerInfo struct {
	Factor string   `json:"factor"`
	Type   string   `json:"type"` // event | cron
	Title  string   `json:"title"`
	About  string   `json:"about"`
	Vars   []string `json:"vars"`
}

var baseVars = []string{"first_name", "name", "referral_link", "promo:20"}

var Triggers = []TriggerInfo{
	{"recently_expired", "event", "Недавно истекла подписка", "Платная подписка закончилась, новой нет", []string{"plan_name"}},
	{"abandoned_cart", "event", "Брошенная корзина", "Счёт создан, но не оплачен и отменён", []string{"plan_name"}},
	{"active_trial", "event", "Активен пробный период", "Клиент только что получил пробный период", []string{"plan_name", "days_left"}},
	{"expiring_soon", "cron", "Скоро истекает", "Платная подписка заканчивается, автопродление выключено", []string{"plan_name", "days_left"}},
	{"inactive_30d", "cron", "Давно не заходил", "Нет активной подписки и давно не открывал кабинет", nil},
	{"lead_temperature_hot", "event", "Лид: горячий", "Не платил, но недавно заходил и пытался оплатить", nil},
	{"lead_temperature_warm", "event", "Лид: тёплый", "Не платил, недавно заходил, брал пробный период", nil},
	{"lead_temperature_cool", "event", "Лид: прохладный", "Не платил, заходил в последний месяц", nil},
	{"lead_temperature_cold", "event", "Лид: холодный", "Не платил и давно не заходил", nil},
	{"lead_temperature_stable", "event", "Стабильный клиент", "Платит давно и регулярно", nil},
}

func triggerKnown(f string) bool {
	for _, t := range Triggers {
		if t.Factor == f {
			return true
		}
	}
	return false
}

// ParseDelay understands Postgres-style intervals ("7 days", "1 day 02:00:00", "00:30:00", "3 hours") and Go durations ("48h").
func ParseDelay(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	var total time.Duration
	f := strings.Fields(s)
	for i := 0; i < len(f); i++ {
		tok := f[i]
		if strings.Count(tok, ":") >= 1 {
			parts := strings.Split(tok, ":")
			var h, m, sec float64
			var err error
			if h, err = strconv.ParseFloat(parts[0], 64); err != nil {
				return 0, fmt.Errorf("не понял длительность %q", s)
			}
			if len(parts) > 1 {
				m, _ = strconv.ParseFloat(parts[1], 64)
			}
			if len(parts) > 2 {
				sec, _ = strconv.ParseFloat(parts[2], 64)
			}
			total += time.Duration((h*3600 + m*60 + sec) * float64(time.Second))
			continue
		}
		n, err := strconv.ParseFloat(tok, 64)
		if err != nil || i+1 >= len(f) {
			return 0, fmt.Errorf("не понял длительность %q", s)
		}
		unit := strings.TrimSuffix(f[i+1], "s")
		i++
		var u time.Duration
		switch unit {
		case "week", "нед", "неделя", "недели", "недель":
			u = 7 * 24 * time.Hour
		case "day", "дн", "день", "дня", "дней":
			u = 24 * time.Hour
		case "hour", "ч", "час", "часа", "часов":
			u = time.Hour
		case "min", "minute", "мин", "минута", "минуты", "минут":
			u = time.Minute
		case "sec", "second", "сек":
			u = time.Second
		default:
			return 0, fmt.Errorf("не понял единицу %q", f[i])
		}
		total += time.Duration(n * float64(u))
	}
	return total, nil
}

func parseSteps(raw json.RawMessage) ([]Step, error) {
	var steps []Step
	if len(raw) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &steps); err != nil {
		return nil, err
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].StepOrder < steps[j].StepOrder })
	return steps, nil
}

// ValidateAutomation normalizes and checks an automation before saving.
func ValidateAutomation(a *store.Automation) error {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		return userErr("укажите название")
	}
	if a.TriggerType != "event" && a.TriggerType != "cron" {
		return userErr("тип запуска: событие или по расписанию")
	}
	if !triggerKnown(a.TriggerFactor) {
		return userErr("неизвестный триггер " + a.TriggerFactor)
	}
	if a.CooldownHours < 0 {
		return userErr("повторный запуск не может быть отрицательным")
	}
	if a.LoopCount < 1 {
		a.LoopCount = 1
	}
	if a.LoopCount > 20 {
		return userErr("повторов цепочки не больше 20")
	}
	if a.ScheduleMode == "" {
		a.ScheduleMode = "relative"
	}
	steps, err := parseSteps(a.Steps)
	if err != nil {
		return userErr("шаги: некорректный формат")
	}
	if len(steps) == 0 {
		return userErr("добавьте хотя бы один шаг")
	}
	if len(steps) > 30 {
		return userErr("не больше 30 шагов")
	}
	for i := range steps {
		st := &steps[i]
		st.StepOrder = i + 1
		if strings.TrimSpace(st.MessageContent) == "" {
			return userErr(fmt.Sprintf("шаг %d: пустой текст", i+1))
		}
		if len([]rune(st.MessageContent)) > 4096 {
			return userErr(fmt.Sprintf("шаг %d: текст длиннее 4096 символов", i+1))
		}
		if _, err := ParseDelay(st.DelayDuration); err != nil {
			return userErr(fmt.Sprintf("шаг %d: %v", i+1, err))
		}
		if st.RepeatCount < 1 {
			st.RepeatCount = 1
		}
		if st.RepeatCount > 10 {
			return userErr(fmt.Sprintf("шаг %d: повторов не больше 10", i+1))
		}
		if st.RepeatInterval != nil {
			if _, err := ParseDelay(*st.RepeatInterval); err != nil {
				return userErr(fmt.Sprintf("шаг %d, интервал повтора: %v", i+1, err))
			}
		}
		if st.Buttons == nil {
			st.Buttons = []Button{}
		}
		for _, b := range st.Buttons {
			if strings.TrimSpace(b.Text) == "" || !(strings.HasPrefix(b.URL, "https://") || strings.HasPrefix(b.URL, "http://") || strings.HasPrefix(b.URL, "/")) {
				return userErr(fmt.Sprintf("шаг %d: у кнопки нужен текст и ссылка https:// или путь кабинета /...", i+1))
			}
		}
		if st.Slots == nil {
			st.Slots = []json.RawMessage{}
		}
	}
	a.Steps, _ = json.Marshal(steps)
	if a.SkipIfActiveIn == nil {
		a.SkipIfActiveIn = []string{}
	}
	return nil
}

// ---- running ----

func (s *Service) automationsOn() bool { return s.set().Automations.Enabled }

// buildPlan expands loops, repeats and shuffling into the list of messages to send.
func buildPlan(a *store.Automation, steps []Step) []store.PlanItem {
	var plan []store.PlanItem
	for loop := 0; loop < max(1, a.LoopCount); loop++ {
		order := make([]int, len(steps))
		for i := range order {
			order[i] = i
		}
		if a.ShuffleSteps {
			rand.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		}
		for _, idx := range order {
			for r := 0; r < max(1, steps[idx].RepeatCount); r++ {
				plan = append(plan, store.PlanItem{Step: idx, Repeat: r > 0})
			}
		}
	}
	return plan
}

func (s *Service) itemDelay(st Step, it store.PlanItem) time.Duration {
	src := st.DelayDuration
	if it.Repeat && st.RepeatInterval != nil {
		src = *st.RepeatInterval
	}
	d, _ := ParseDelay(src)
	return d
}

// schedule moves t out of quiet hours and into the step's time slots (shop timezone).
func (s *Service) schedule(t time.Time, st Step) time.Time {
	set := s.set()
	loc := set.General.Location()
	var slots [][2]int
	for _, raw := range st.Slots {
		var str string
		if json.Unmarshal(raw, &str) != nil {
			continue
		}
		if f, to, ok := settings.ParseWindow(str); ok {
			slots = append(slots, [2]int{f, to})
		}
	}
	qf, qt, quiet := settings.ParseWindow(set.Automations.QuietHours)
	ok := func(x time.Time) bool {
		m := x.In(loc).Hour()*60 + x.In(loc).Minute()
		if quiet && settings.InWindow(m, qf, qt) && qf != qt {
			return false
		}
		if len(slots) == 0 {
			return true
		}
		for _, sl := range slots {
			if settings.InWindow(m, sl[0], sl[1]) {
				return true
			}
		}
		return false
	}
	// Walk forward in 5-minute steps (at most two days) to the first allowed moment.
	x := t
	for i := 0; i < 2*24*12 && !ok(x); i++ {
		x = x.Add(5 * time.Minute).Truncate(5 * time.Minute)
	}
	return x
}

// startRun starts an automation for a customer if allowed. Returns true if started.
func (s *Service) startRun(ctx context.Context, a *store.Automation, customerID int64, rc map[string]any) (bool, error) {
	if !a.IsActive {
		return false, nil
	}
	c, err := store.GetCustomer(ctx, s.Store.Pool, customerID)
	if err != nil || c.IsBlocked || !s.reachable(c) {
		return false, err
	}
	okStart, err := store.CanStartRun(ctx, s.Store.Pool, a, customerID)
	if err != nil || !okStart {
		return false, err
	}
	steps, err := parseSteps(a.Steps)
	if err != nil || len(steps) == 0 {
		return false, err
	}
	plan := buildPlan(a, steps)
	first := steps[plan[0].Step]
	next := s.schedule(time.Now().Add(s.itemDelay(first, plan[0])), first)
	if rc == nil {
		rc = map[string]any{}
	}
	return store.InsertRun(ctx, s.Store.Pool, &store.AutomationRun{AutomationID: a.ID, CustomerID: customerID, Plan: plan, Context: rc, NextAt: &next})
}

func (s *Service) reachable(c *store.Customer) bool {
	return (c.TelegramID != nil && !c.BotBlocked) || (c.Email != nil && s.Mailer.Configured())
}

// AutomationEvent starts event automations with this trigger for the customer right away.
func (s *Service) AutomationEvent(ctx context.Context, factor string, customerID int64, rc map[string]any) {
	if !s.automationsOn() {
		return
	}
	list, err := store.ListAutomations(ctx, s.Store.Pool)
	if err != nil {
		return
	}
	for i := range list {
		if list[i].TriggerFactor == factor && list[i].IsActive {
			if _, err := s.startRun(ctx, &list[i], customerID, rc); err != nil {
				s.Log.Warn("automation start", "automation", list[i].ID, "err", err)
			}
		}
	}
}

// onPurchaseForAutomations credits conversions to recent runs.
func (s *Service) onPurchaseForAutomations(ctx context.Context, c *store.Customer, p *store.Payment) {
	if p.Operation == "autopay" {
		return
	}
	_ = store.MarkConverted(ctx, s.Store.Pool, c.ID, 7*24*time.Hour)
}

// ScanAutomationTriggers finds customers matching time-based triggers and starts runs.
func (s *Service) ScanAutomationTriggers(ctx context.Context) {
	if !s.automationsOn() {
		return
	}
	set := s.set().Automations
	list, err := store.ListAutomations(ctx, s.Store.Pool)
	if err != nil {
		return
	}
	// Lead temperatures: changes fire lead_temperature_* scenarios.
	changes, err := store.RecomputeLeadTemperatures(ctx, s.Store.Pool, store.LeadParams{HotSeenDays: set.HotSeenDays,
		WarmSeenDays: set.WarmSeenDays, CoolSeenDays: set.CoolSeenDays, LeadMinAgeHours: set.LeadMinAgeHours,
		StableMinPayments: set.StableMinPayments, StableMinDays: set.StableMinDays})
	if err != nil {
		s.Log.Error("lead temperatures", "err", err)
	}
	for _, ch := range changes {
		if ch.First || ch.New == "" {
			continue
		}
		for i := range list {
			if list[i].IsActive && list[i].TriggerFactor == "lead_temperature_"+ch.New {
				_, _ = s.startRun(ctx, &list[i], ch.CustomerID, nil)
			}
		}
	}
	params := store.TriggerParams{RecentlyExpiredHours: set.RecentlyExpiredHours, AbandonedCartHours: set.AbandonedCartHours,
		ActiveTrialHours: set.ActiveTrialHours, ExpiringSoonDays: set.ExpiringSoonDays, InactiveDays: set.InactiveDays}
	cache := map[string][]store.TriggerCandidate{}
	for i := range list {
		a := &list[i]
		if !a.IsActive || strings.HasPrefix(a.TriggerFactor, "lead_temperature_") {
			continue
		}
		cands, ok := cache[a.TriggerFactor]
		if !ok {
			cands, err = store.TriggerCandidates(ctx, s.Store.Pool, a.TriggerFactor, params, 500)
			if err != nil {
				s.Log.Error("automation candidates", "factor", a.TriggerFactor, "err", err)
				continue
			}
			cache[a.TriggerFactor] = cands
		}
		for _, cd := range cands {
			rc := map[string]any{"plan_name": cd.PlanName}
			if cd.SubscriptionID != nil {
				rc["subscription_id"] = *cd.SubscriptionID
			}
			if cd.PaymentID != nil {
				rc["payment_id"] = *cd.PaymentID
			}
			if _, err := s.startRun(ctx, a, cd.CustomerID, rc); err != nil {
				s.Log.Warn("automation start", "automation", a.ID, "customer", cd.CustomerID, "err", err)
			}
		}
	}
}

// ProcessAutomationRuns sends due automation messages.
func (s *Service) ProcessAutomationRuns(ctx context.Context) {
	if !s.automationsOn() {
		return
	}
	autos := map[int64]*store.Automation{}
	for {
		var runs []store.AutomationRun
		err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
			var err error
			runs, err = store.DueRuns(ctx, tx, 50)
			if err != nil {
				return err
			}
			for i := range runs {
				// Push the due time a bit so a parallel worker or a crash doesn't send twice right away.
				_, err = tx.Exec(ctx, `UPDATE automation_runs SET next_at = now() + interval '10 minutes' WHERE id=$1`, runs[i].ID)
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			s.Log.Error("automation runs", "err", err)
			return
		}
		if len(runs) == 0 {
			return
		}
		for i := range runs {
			s.processRun(ctx, &runs[i], autos)
		}
		if len(runs) < 50 || ctx.Err() != nil {
			return
		}
	}
}

func (s *Service) processRun(ctx context.Context, r *store.AutomationRun, autos map[int64]*store.Automation) {
	pool := s.Store.Pool
	a, ok := autos[r.AutomationID]
	if !ok {
		var err error
		a, err = store.GetAutomation(ctx, pool, r.AutomationID)
		if err != nil {
			_ = store.FinishRun(ctx, pool, r.ID, "stopped", "deleted", false)
			return
		}
		autos[r.AutomationID] = a
	}
	if !a.IsActive {
		_ = store.FinishRun(ctx, pool, r.ID, "stopped", "disabled", false)
		return
	}
	steps, err := parseSteps(a.Steps)
	if err != nil || r.Position >= len(r.Plan) || r.Plan[r.Position].Step >= len(steps) {
		_ = store.FinishRun(ctx, pool, r.ID, "done", "", false)
		return
	}
	st := steps[r.Plan[r.Position].Step]
	c, err := store.GetCustomer(ctx, pool, r.CustomerID)
	if err != nil || c.IsBlocked {
		_ = store.FinishRun(ctx, pool, r.ID, "stopped", "condition", false)
		return
	}
	if st.StopOnPurchase {
		if paid, _ := store.PaidSince(ctx, pool, c.ID, r.StartedAt); paid {
			_ = store.FinishRun(ctx, pool, r.ID, "stopped", "purchase", true)
			return
		}
	}
	msg := Message{Text: s.renderCustomerText(ctx, c, st.MessageContent, r.Context), Photo: s.absURL(st.Image)}
	for _, b := range st.Buttons {
		msg.Buttons = append(msg.Buttons, Button{Text: b.Text, URL: s.absURL(b.URL)})
	}
	if err := s.Notify.Deliver(ctx, c, msg, "auto"); err != nil {
		if errors.Is(err, ErrUnreachable) {
			_ = store.FinishRun(ctx, pool, r.ID, "stopped", "unreachable", false)
			return
		}
		s.Log.Warn("automation send", "run", r.ID, "err", err)
		next := time.Now().Add(30 * time.Minute)
		_ = store.AdvanceRun(ctx, pool, r.ID, r.Position, &next, false)
		return
	}
	if st.WebhookURL != nil && *st.WebhookURL != "" {
		go s.callStepWebhook(*st.WebhookURL, st.WebhookMethod, a, c, r)
	}
	pos := r.Position + 1
	if pos >= len(r.Plan) {
		_ = store.AdvanceRun(ctx, pool, r.ID, pos, nil, true)
		_ = store.FinishRun(ctx, pool, r.ID, "done", "", false)
		return
	}
	nextSt := steps[r.Plan[pos].Step]
	next := s.schedule(time.Now().Add(s.itemDelay(nextSt, r.Plan[pos])), nextSt)
	_ = store.AdvanceRun(ctx, pool, r.ID, pos, &next, true)
}

func (s *Service) callStepWebhook(url string, method *string, a *store.Automation, c *store.Customer, r *store.AutomationRun) {
	m := http.MethodPost
	if method != nil && *method != "" {
		m = strings.ToUpper(*method)
	}
	body, _ := json.Marshal(map[string]any{"automation": a.Name, "automation_id": a.ID, "customer_id": c.ID,
		"telegram_id": c.TelegramID, "email": c.Email, "step": r.Position + 1, "context": r.Context})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var rdr *bytes.Reader
	if m != http.MethodGet {
		rdr = bytes.NewReader(body)
	}
	var req *http.Request
	var err error
	if rdr != nil {
		req, err = http.NewRequestWithContext(ctx, m, url, rdr)
	} else {
		req, err = http.NewRequestWithContext(ctx, m, url, nil)
	}
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.Log.Warn("automation webhook", "url", url, "err", err)
		return
	}
	resp.Body.Close()
}

// absURL turns cabinet paths into absolute links.
func (s *Service) absURL(u string) string {
	if strings.HasPrefix(u, "/") {
		return s.Cfg.PublicURL + u
	}
	return u
}

var promoVarRe = regexp.MustCompile(`\{promo:([a-z]*):?(\d+)(?::(\d+))?\}`)

// renderCustomerText fills {first_name}, {plan_name}, {days_left}, {referral_link}, {promo:...} for a customer.
// Variables are escaped; the template itself is Telegram HTML.
func (s *Service) renderCustomerText(ctx context.Context, c *store.Customer, tpl string, rc map[string]any) string {
	first := ""
	if c.FirstName != nil {
		first = *c.FirstName
	}
	if first == "" {
		first = c.DisplayName()
	}
	vars := map[string]string{"first_name": first, "name": c.DisplayName(), "referral_link": s.RefLink(c), "app_url": s.Cfg.PublicURL}
	if v, ok := rc["plan_name"].(string); ok {
		vars["plan_name"] = v
	}
	// days_left from the context subscription or the customer's primary active one.
	var sub *store.Subscription
	if id, ok := rc["subscription_id"].(float64); ok {
		sub, _ = store.GetSubscription(ctx, s.Store.Pool, int64(id))
	} else if id, ok := rc["subscription_id"].(int64); ok {
		sub, _ = store.GetSubscription(ctx, s.Store.Pool, id)
	}
	if sub == nil && (strings.Contains(tpl, "{days_left}") || strings.Contains(tpl, "{plan_name}")) {
		if subs, err := store.ListSubscriptions(ctx, s.Store.Pool, c.ID); err == nil {
			for i := range subs {
				if subs[i].Active() && (sub == nil || subs[i].IsPrimary) {
					sub = &subs[i]
				}
			}
		}
	}
	if sub != nil {
		left := int(time.Until(sub.ExpiresAt).Hours()/24 + 0.999)
		if left < 0 {
			left = 0
		}
		vars["days_left"] = strconv.Itoa(left)
		if vars["plan_name"] == "" {
			vars["plan_name"] = sub.Title
		}
	}
	if vars["plan_name"] == "" {
		vars["plan_name"] = "VPN"
	}
	out := promoVarRe.ReplaceAllStringFunc(tpl, func(m string) string {
		code, err := s.personalPromo(ctx, c, m)
		if err != nil {
			s.Log.Warn("personal promo", "err", err)
			return ""
		}
		return "<code>" + code + "</code>"
	})
	return renderVars(out, vars, true)
}

// personalPromo creates a one-time personal code from {promo:20}, {promo:discount:20:48} or {promo:days:3:72}.
func (s *Service) personalPromo(ctx context.Context, c *store.Customer, token string) (string, error) {
	m := promoVarRe.FindStringSubmatch(token)
	kind := m[1]
	if kind == "" {
		kind = "discount"
	}
	if kind != "discount" && kind != "days" {
		return "", fmt.Errorf("неизвестный тип промокода %q", kind)
	}
	value, _ := strconv.Atoi(m[2])
	if kind == "discount" && (value < 1 || value > 100) || value < 1 {
		return "", fmt.Errorf("неверное значение промокода %q", token)
	}
	hours := s.set().Automations.PersonalPromoHours
	if m[3] != "" {
		hours, _ = strconv.Atoi(m[3])
	}
	until := time.Now().Add(time.Duration(hours) * time.Hour)
	cid := c.ID
	for attempt := 0; attempt < 5; attempt++ {
		p := &store.Promo{Code: "BG" + store.RandomCode(6), Kind: kind, Value: value, Folder: "Автоматизации",
			Description: "Персональный код из автоматизации", IsActive: true, MaxUses: 1, CustomerID: &cid, PlanIDs: []int64{}}
		if hours > 0 {
			p.ValidUntil = &until
		}
		if err := store.SavePromo(ctx, s.Store.Pool, p); err != nil {
			if store.IsUniqueViolation(err) {
				continue
			}
			return "", err
		}
		return p.Code, nil
	}
	return "", errors.New("не удалось подобрать код")
}

// ---- import / export (RWP format) ----

type AutomationExport struct {
	Kind       string           `json:"kind"`
	Version    int              `json:"version"`
	ExportedAt time.Time        `json:"exported_at"`
	Sequences  []ExportSequence `json:"sequences"`
}

type ExportSequence struct {
	Name           string   `json:"name"`
	TriggerType    string   `json:"trigger_type"`
	TriggerFactor  string   `json:"trigger_factor"`
	IsActive       bool     `json:"is_active"`
	CooldownHours  int      `json:"cooldown_hours"`
	SkipIfActiveIn []string `json:"skip_if_active_in"`
	ScheduleMode   string   `json:"schedule_mode"`
	LoopCount      int      `json:"loop_count"`
	ShuffleSteps   bool     `json:"shuffle_steps"`
	Steps          []Step   `json:"steps"`
}

func (s *Service) ExportAutomations(ctx context.Context) (*AutomationExport, error) {
	list, err := store.ListAutomations(ctx, s.Store.Pool)
	if err != nil {
		return nil, err
	}
	out := &AutomationExport{Kind: "rwp-automations", Version: 1, ExportedAt: time.Now().UTC(), Sequences: []ExportSequence{}}
	for _, a := range list {
		steps, _ := parseSteps(a.Steps)
		out.Sequences = append(out.Sequences, ExportSequence{Name: a.Name, TriggerType: a.TriggerType, TriggerFactor: a.TriggerFactor,
			IsActive: a.IsActive, CooldownHours: a.CooldownHours, SkipIfActiveIn: a.SkipIfActiveIn, ScheduleMode: a.ScheduleMode,
			LoopCount: a.LoopCount, ShuffleSteps: a.ShuffleSteps, Steps: steps})
	}
	return out, nil
}

type ImportResult struct {
	Created  int      `json:"created"`
	Updated  int      `json:"updated"`
	Warnings []string `json:"warnings"`
}

// ImportAutomations adds sequences from an export; existing ones with the same name are replaced.
func (s *Service) ImportAutomations(ctx context.Context, raw []byte) (*ImportResult, error) {
	var in AutomationExport
	if err := json.Unmarshal(raw, &in); err != nil || len(in.Sequences) == 0 {
		return nil, userErr("файл не похож на экспорт автоматизаций")
	}
	existing, err := store.ListAutomations(ctx, s.Store.Pool)
	if err != nil {
		return nil, err
	}
	byName := map[string]int64{}
	for _, a := range existing {
		byName[a.Name] = a.ID
	}
	res := &ImportResult{Warnings: []string{}}
	var prepared []store.Automation
	for _, q := range in.Sequences {
		steps, _ := json.Marshal(q.Steps)
		a := store.Automation{ID: byName[q.Name], Name: q.Name, TriggerType: q.TriggerType, TriggerFactor: q.TriggerFactor,
			IsActive: q.IsActive, CooldownHours: q.CooldownHours, SkipIfActiveIn: q.SkipIfActiveIn, ScheduleMode: q.ScheduleMode,
			LoopCount: q.LoopCount, ShuffleSteps: q.ShuffleSteps, Steps: steps}
		if err := ValidateAutomation(&a); err != nil {
			return nil, userErr(fmt.Sprintf("«%s»: %v", q.Name, err))
		}
		prepared = append(prepared, a)
	}
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		for i := range prepared {
			if prepared[i].ID > 0 {
				res.Updated++
			} else {
				res.Created++
			}
			if err := store.SaveAutomation(ctx, tx, &prepared[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.Warnings = s.AutomationWarnings(ctx, prepared)
	return res, nil
}

var codeLikeRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]{3,15}\b`)
var tmeRe = regexp.MustCompile(`t\.me/([A-Za-z0-9_]+)`)

// AutomationWarnings checks scenarios for things that will not work as intended.
func (s *Service) AutomationWarnings(ctx context.Context, list []store.Automation) []string {
	var out []string
	trialDays := s.set().Trial.Days
	seenCodes := map[string]bool{}
	for _, a := range list {
		steps, _ := parseSteps(a.Steps)
		var elapsed time.Duration
		for i, st := range steps {
			d, _ := ParseDelay(st.DelayDuration)
			elapsed += d
			where := fmt.Sprintf("«%s», шаг %d", a.Name, i+1)
			if a.TriggerFactor == "active_trial" && trialDays > 0 && elapsed > time.Duration(trialDays)*24*time.Hour {
				out = append(out, fmt.Sprintf("%s: уходит через %s после старта, а пробный период — %d дн. Клиент получит его уже без доступа.",
					where, humanDur(elapsed), trialDays))
			}
			if a.TriggerFactor == "expiring_soon" && elapsed > time.Duration(s.set().Automations.ExpiringSoonDays)*24*time.Hour {
				out = append(out, fmt.Sprintf("%s: уходит через %s, когда подписка уже закончится. Текст про «осталось {days_left} дня» будет неверным.",
					where, humanDur(elapsed)))
			}
			if a.TriggerFactor == "abandoned_cart" && elapsed > 72*time.Hour && strings.Contains(strings.ToLower(st.MessageContent), "сегодня") {
				out = append(out, fmt.Sprintf("%s: в тексте «только сегодня», а шаг уходит через %s.", where, humanDur(elapsed)))
			}
			for _, code := range codeLikeRe.FindAllString(st.MessageContent, -1) {
				if seenCodes[code] || code == "VPN" || code == "HTTP" || code == "YOUTUBE" {
					continue
				}
				seenCodes[code] = true
				if _, err := store.GetPromoByCode(ctx, s.Store.Pool, code); err == store.ErrNotFound {
					out = append(out, fmt.Sprintf("%s: промокода %s нет в разделе «Промокоды» — создайте его или замените на {promo:20} (личный код).", where, code))
				}
			}
			for _, b := range st.Buttons {
				if m := tmeRe.FindStringSubmatch(b.URL); m != nil && s.BotUsername != "" && !strings.EqualFold(m[1], s.BotUsername) {
					out = append(out, fmt.Sprintf("%s: кнопка ведёт в @%s, а текущий бот — @%s.", where, m[1], s.BotUsername))
				}
			}
		}
	}
	return out
}

func humanDur(d time.Duration) string {
	days := int(d.Hours() / 24)
	if days >= 1 {
		return plural(days, "день", "дня", "дней")
	}
	return plural(int(d.Hours()), "час", "часа", "часов")
}

// AutomationOverview is the admin list with stats.
type AutomationOverview struct {
	store.Automation
	Stats store.AutomationStats `json:"stats"`
}

func (s *Service) AutomationsOverview(ctx context.Context) ([]AutomationOverview, []string, error) {
	list, err := store.ListAutomations(ctx, s.Store.Pool)
	if err != nil {
		return nil, nil, err
	}
	stats, err := store.AutomationStatsAll(ctx, s.Store.Pool)
	if err != nil {
		return nil, nil, err
	}
	by := map[int64]store.AutomationStats{}
	for _, st := range stats {
		by[st.AutomationID] = st
	}
	out := make([]AutomationOverview, 0, len(list))
	for _, a := range list {
		out = append(out, AutomationOverview{Automation: a, Stats: by[a.ID]})
	}
	return out, s.AutomationWarnings(ctx, list), nil
}

// TestAutomationStep sends one step to the admin, as the admin.
func (s *Service) TestAutomationStep(ctx context.Context, admin *store.Customer, raw json.RawMessage, rc map[string]any) error {
	var st Step
	if err := json.Unmarshal(raw, &st); err != nil {
		return userErr("некорректный шаг")
	}
	msg := Message{Text: s.renderCustomerText(ctx, admin, st.MessageContent, rc), Photo: s.absURL(st.Image)}
	for _, b := range st.Buttons {
		msg.Buttons = append(msg.Buttons, Button{Text: b.Text, URL: s.absURL(b.URL)})
	}
	if err := s.Notify.Deliver(ctx, admin, msg, "auto"); err != nil {
		return userErr("не удалось отправить: " + err.Error())
	}
	return nil
}
