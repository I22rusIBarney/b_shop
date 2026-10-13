// Package importer moves the shop's own data out of an RWP Shop database: customers and their logins,
// plans, subscriptions, payments, saved cards, promo codes, referrals, device names, FAQ and tickets.
// It reads RWP's tables directly (a copy restored from pg_dump, or the live DB read-only) and writes
// everything in one transaction: a dry run rolls back and only prints the report.
package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Options struct {
	DryRun bool
	// BotID limits the import to one bot of a multi-bot RWP install (0 = all rows).
	BotID int64
	// Force allows importing into a database that already has customers.
	Force bool
	// Wipe deletes customers (with everything that belongs to them), plans, promo codes and FAQ first,
	// in the same transaction. Settings, branding, automations and broadcasts stay.
	Wipe bool
}

// Report says what was imported and what needs a look.
type Report struct {
	Counts   map[string]int            `json:"counts"`
	Unknown  map[string]map[string]int `json:"unknown"` // field → unrecognized value → rows
	Warnings []string                  `json:"warnings"`
}

func (r *Report) add(k string, n int) { r.Counts[k] += n }
func (r *Report) unknown(field, v string) {
	if r.Unknown[field] == nil {
		r.Unknown[field] = map[string]int{}
	}
	r.Unknown[field][v]++
}
func (r *Report) warn(f string, a ...any) {
	if len(r.Warnings) < 200 {
		r.Warnings = append(r.Warnings, fmt.Sprintf(f, a...))
	}
}

// String renders the report for the console.
func (r *Report) String() string {
	var b strings.Builder
	keys := make([]string, 0, len(r.Counts))
	for k := range r.Counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b.WriteString("Импортировано:\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "  %-28s %d\n", k, r.Counts[k])
	}
	if len(r.Unknown) > 0 {
		b.WriteString("Незнакомые значения (строки импортированы с безопасным значением по умолчанию):\n")
		for f, m := range r.Unknown {
			for v, n := range m {
				fmt.Fprintf(&b, "  %s = %q: %d\n", f, v, n)
			}
		}
	}
	if len(r.Warnings) > 0 {
		b.WriteString("Предупреждения:\n")
		for _, w := range r.Warnings {
			b.WriteString("  - " + w + "\n")
		}
	}
	return b.String()
}

type run struct {
	ctx    context.Context
	src    *pgxpool.Pool
	tx     pgx.Tx
	opt    Options
	rep    *Report
	cust   map[uuid.UUID]int64 // RWP customer → ours
	plan   map[int64]int64
	sub    map[int64]int64
	pay    map[int64]uuid.UUID // RWP purchase → ours
	promo  map[int64]int64
	method map[string]int64 // RWP payment_method.id → ours
}

// RWP lets a column be missing in older versions; every query goes through src.Query and a missing
// table/column is reported as a warning instead of failing the whole import.
func (r *run) query(what, q string, args ...any) (pgx.Rows, bool) {
	rows, err := r.src.Query(r.ctx, q, args...)
	if err != nil {
		r.rep.warn("%s: пропущено (%v)", what, err)
		return nil, false
	}
	return rows, true
}

func (r *run) botFilter(alias string) string {
	if r.opt.BotID == 0 {
		return "true"
	}
	return fmt.Sprintf("(%s.bot_id IS NULL OR %s.bot_id = %d)", alias, alias, r.opt.BotID)
}

// Run imports src into dst.
func Run(ctx context.Context, src, dst *pgxpool.Pool, opt Options) (*Report, error) {
	rep := &Report{Counts: map[string]int{}, Unknown: map[string]map[string]int{}}
	if !opt.Force && !opt.Wipe {
		var n int
		if err := dst.QueryRow(ctx, `SELECT count(*) FROM customers`).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, fmt.Errorf("в новой базе уже есть клиенты (%d): импорт делается в чистую установку (или --force)", n)
		}
	}
	tx, err := dst.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	r := &run{ctx: ctx, src: src, tx: tx, opt: opt, rep: rep, cust: map[uuid.UUID]int64{}, plan: map[int64]int64{},
		sub: map[int64]int64{}, pay: map[int64]uuid.UUID{}, promo: map[int64]int64{}, method: map[string]int64{}}
	if opt.Wipe {
		for _, q := range []string{`DELETE FROM customers`, `DELETE FROM promo_codes`, `DELETE FROM plans`, `DELETE FROM faq_items`} {
			if _, err := tx.Exec(ctx, q); err != nil {
				return nil, fmt.Errorf("очистка: %w", err)
			}
		}
	}
	steps := []struct {
		name string
		fn   func() error
	}{
		{"тарифы", r.plans},
		{"клиенты", r.customers},
		{"рефералы", r.referrals},
		{"способы оплаты", r.paymentMethods},
		{"подписки", r.subscriptions},
		{"платежи", r.purchases},
		{"смены тарифа", r.planChanges},
		{"промокоды", r.promos},
		{"открытые тарифы", r.unlockedPlans},
		{"реферальные бонусы", r.referralBonuses},
		{"имена устройств", r.deviceNames},
		{"FAQ", r.faq},
		{"обращения", r.tickets},
	}
	for _, s := range steps {
		if err := s.fn(); err != nil {
			return rep, fmt.Errorf("%s: %w", s.name, err)
		}
	}
	if opt.DryRun {
		return rep, nil
	}
	return rep, tx.Commit(ctx)
}

// ---- helpers ----

func kopecks(rub *float64) int64 {
	if rub == nil {
		return 0
	}
	return int64(math.Round(*rub * 100))
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

func lower(p *string) string { return strings.ToLower(str(p)) }

// unitOf maps RWP period units to ours.
func (r *run) unitOf(field string, u *string) string {
	switch strings.TrimSuffix(lower(u), "s") {
	case "", "month", "mon", "m":
		return "month"
	case "day", "d":
		return "day"
	case "week", "w":
		return "week"
	case "year", "y":
		return "year"
	}
	r.rep.unknown(field, str(u))
	return "month"
}

// months/days a purchase covers.
func coverage(unit string, n int) (months, days int) {
	if n <= 0 {
		n = 1
	}
	switch unit {
	case "day":
		return 0, n
	case "week":
		return 0, 7 * n
	case "year":
		return 12 * n, 365 * n
	}
	return n, 31 * n
}

func parseSquads(s string) []uuid.UUID {
	s = strings.TrimSpace(s)
	out := []uuid.UUID{}
	if s == "" {
		return out
	}
	var list []string
	if strings.HasPrefix(s, "[") {
		var raw []any
		if json.Unmarshal([]byte(s), &raw) == nil {
			for _, x := range raw {
				switch v := x.(type) {
				case string:
					list = append(list, v)
				case map[string]any:
					if u, ok := v["uuid"].(string); ok {
						list = append(list, u)
					}
				}
			}
		}
	} else {
		list = strings.FieldsFunc(strings.Trim(s, "{}"), func(r rune) bool { return r == ',' || r == ' ' || r == ';' })
	}
	for _, x := range list {
		if u, err := uuid.Parse(strings.Trim(x, `"`)); err == nil {
			out = append(out, u)
		}
	}
	return out
}

// ---- plans ----

func (r *run) plans() error {
	rows, ok := r.query("тарифы", `SELECT id, name, COALESCE(type,''), COALESCE(traffic_limit,0), COALESCE(device_limit,0), COALESCE(is_active,true),
			COALESCE(is_default,false), COALESCE(is_hidden,false), COALESCE(internal_squads,''), COALESCE(external_squad_uuid,''), COALESCE(remnawave_tag,''),
			COALESCE(sort_order,0), COALESCE(traffic_limit_strategy,''), COALESCE(allow_extra_devices,false), COALESCE(max_total_devices,0),
			COALESCE(renewal_disabled,false), COALESCE(change_from_disabled,false), COALESCE(change_to_disabled,false),
			COALESCE(conversion_disabled,false), required_plan_id, COALESCE(max_subscriptions_per_customer,0), COALESCE(reset_traffic_on_purchase,false)
		FROM plan ORDER BY id`)
	if !ok {
		return nil
	}
	type pl struct {
		id                                int64
		name, typ                         string
		traffic, devices                  int
		active, def, hidden               bool
		squads, ext, tag                  string
		sort                              int
		strategy                          string
		extra                             bool
		maxDevices                        int
		renewOff, fromOff, toOff, convOff bool
		required                          *int64
		maxSubs                           int
		resetOnBuy                        bool
	}
	var list []pl
	for rows.Next() {
		var p pl
		if err := rows.Scan(&p.id, &p.name, &p.typ, &p.traffic, &p.devices, &p.active, &p.def, &p.hidden, &p.squads, &p.ext, &p.tag, &p.sort,
			&p.strategy, &p.extra, &p.maxDevices, &p.renewOff, &p.fromOff, &p.toOff, &p.convOff, &p.required, &p.maxSubs, &p.resetOnBuy); err != nil {
			rows.Close()
			return err
		}
		list = append(list, p)
	}
	rows.Close()
	for _, p := range list {
		kind := "period"
		switch strings.ToLower(p.typ) {
		case "", "subscription", "period", "time":
		case "traffic":
			kind = "traffic"
		default:
			r.rep.unknown("plan.type", p.typ)
		}
		strategy := strings.ToUpper(p.strategy)
		switch strategy {
		case "NO_RESET", "DAY", "WEEK", "MONTH", "MONTH_ROLLING":
		case "":
			strategy = "MONTH"
		default:
			r.rep.unknown("plan.traffic_limit_strategy", p.strategy)
			strategy = "MONTH"
		}
		var ext *uuid.UUID
		if u, err := uuid.Parse(strings.TrimSpace(p.ext)); err == nil {
			ext = &u
		}
		maxExtra := 0
		if p.extra && p.maxDevices > p.devices {
			maxExtra = p.maxDevices - p.devices
		}
		var id int64
		if err := r.tx.QueryRow(r.ctx, `INSERT INTO plans (name, kind, sort, is_active, is_hidden, is_default, renewal_disabled, change_from_disabled,
				change_to_disabled, conversion_disabled, max_per_customer, traffic_limit_gb, device_limit, extra_devices_allowed, max_extra_devices,
				rw_tag, rw_internal_squads, rw_external_squad, traffic_reset_strategy, reset_traffic_on_buy)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20) RETURNING id`,
			p.name, kind, p.sort, p.active, p.hidden, p.def, p.renewOff, p.fromOff, p.toOff, p.convOff, p.maxSubs, p.traffic, p.devices,
			p.extra, maxExtra, p.tag, parseSquads(p.squads), ext, strategy, p.resetOnBuy).Scan(&id); err != nil {
			return err
		}
		r.plan[p.id] = id
		r.rep.add("тарифы", 1)
	}
	for _, p := range list {
		if p.required != nil && r.plan[*p.required] != 0 {
			if _, err := r.tx.Exec(r.ctx, `UPDATE plans SET required_plan_id=$2 WHERE id=$1`, r.plan[p.id], r.plan[*p.required]); err != nil {
				return err
			}
		}
	}
	// Prices: rubles only (the shop takes YooKassa).
	rows, ok = r.query("цены тарифов", `SELECT plan_id, COALESCE(price,0)::float8, COALESCE(currency,''), COALESCE(period_value,1), period_unit,
			COALESCE(is_active,true), COALESCE(price_type,''), COALESCE(is_highlighted,false)
		FROM plan_prices ORDER BY plan_id, period_unit, period_value`)
	if !ok {
		return nil
	}
	defer rows.Close()
	type price struct {
		plan       int64
		price      float64
		cur, typ   string
		n          int
		unit       *string
		active, hi bool
	}
	var prices []price
	for rows.Next() {
		var p price
		if err := rows.Scan(&p.plan, &p.price, &p.cur, &p.n, &p.unit, &p.active, &p.typ, &p.hi); err != nil {
			return err
		}
		prices = append(prices, p)
	}
	rows.Close()
	sortIdx := map[int64]int{}
	for _, p := range prices {
		pid := r.plan[p.plan]
		if pid == 0 || !p.active {
			continue
		}
		if c := strings.ToUpper(p.cur); c != "" && c != "RUB" {
			r.rep.add("цены в другой валюте (пропущены)", 1)
			continue
		}
		switch t := strings.ToLower(p.typ); t {
		case "base", "period", "subscription", "default":
		case "extra_device":
			// Our plans have one monthly price per extra device: take the 1-month one.
			if p.n == 1 {
				if _, err := r.tx.Exec(r.ctx, `UPDATE plans SET extra_device_price=$2 WHERE id=$1`, pid, int64(math.Round(p.price*100))); err != nil {
					return err
				}
				r.rep.add("цены доп. устройств", 1)
			}
			continue
		case "":
			r.rep.add("цены без периода (трафик и т.п., пропущены)", 1)
			continue
		default:
			r.rep.unknown("plan_prices.price_type", p.typ)
			continue
		}
		unit := r.unitOf("plan_prices.period_unit", p.unit)
		n := p.n
		if unit == "year" {
			unit, n = "month", 12*n
		}
		sortIdx[pid]++
		if _, err := r.tx.Exec(r.ctx, `INSERT INTO plan_periods (plan_id, unit, amount, price, is_best, sort) VALUES ($1,$2,$3,$4,$5,$6)`,
			pid, unit, max(n, 1), int64(math.Round(p.price*100)), p.hi, sortIdx[pid]); err != nil {
			return err
		}
		r.rep.add("цены тарифов", 1)
	}
	return nil
}

// ---- customers ----

func (r *run) customers() error {
	rows, ok := r.query("клиенты", `SELECT id, telegram_id, email, tg_username, tg_first_name, tg_last_name, language, COALESCE(is_blocked,false),
			COALESCE(is_blocked_by_user,false), COALESCE(trial_used,false), COALESCE(discount_percent,0), admin_tag, admin_notes, referral_code,
			created_at, last_activity_at, lead_temperature, yandex_id, vk_id, google_id, COALESCE(email_verified,false), COALESCE(is_superadmin,false)
		FROM customer c WHERE `+r.botFilter("c")+` ORDER BY created_at`)
	if !ok {
		return errors.New("не удалось прочитать таблицу customer")
	}
	defer rows.Close()
	type c struct {
		id                             uuid.UUID
		tg                             *int64
		email, user, first, last, lang *string
		blocked, botBlocked, trial     bool
		discount                       int
		tag, notes, ref                *string
		created                        time.Time
		seen                           *time.Time
		lead, yandex, vk, google       *string
		verified, superadmin           bool
	}
	var list []c
	for rows.Next() {
		var x c
		if err := rows.Scan(&x.id, &x.tg, &x.email, &x.user, &x.first, &x.last, &x.lang, &x.blocked, &x.botBlocked, &x.trial, &x.discount,
			&x.tag, &x.notes, &x.ref, &x.created, &x.seen, &x.lead, &x.yandex, &x.vk, &x.google, &x.verified, &x.superadmin); err != nil {
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	seenEmail := map[string]bool{}
	seenRef := map[string]bool{}
	for _, x := range list {
		email := lower(x.email)
		if email != "" && seenEmail[email] {
			r.rep.warn("почта %s встречается у нескольких клиентов — оставлена у первого", email)
			email = ""
		}
		if email != "" {
			seenEmail[email] = true
		}
		ref := str(x.ref)
		if ref == "" || seenRef[strings.ToLower(ref)] {
			ref = ""
		} else {
			seenRef[strings.ToLower(ref)] = true
		}
		lang := lower(x.lang)
		if lang == "" {
			lang = "ru"
		}
		lead := lower(x.lead)
		switch lead {
		case "", "hot", "warm", "cool", "cold", "stable":
		case "none":
			lead = ""
		default:
			r.rep.unknown("customer.lead_temperature", lead)
			lead = ""
		}
		disc := min(max(x.discount, 0), 100)
		var id int64
		err := r.tx.QueryRow(r.ctx, `INSERT INTO customers (telegram_id, email, username, first_name, last_name, language, is_blocked, bot_blocked,
				personal_discount, tag, notes, ref_code, trial_used, source, created_at, last_seen_at, lead_temperature)
			VALUES ($1, NULLIF($2,''), NULLIF($3,''), NULLIF($4,''), NULLIF($5,''), $6, $7, $8, $9, $10, $11, NULLIF($12,''), $13, 'rwp', $14, $15, $16)
			RETURNING id`, x.tg, email, str(x.user), str(x.first), str(x.last), lang, x.blocked, x.botBlocked, disc, str(x.tag), str(x.notes),
			ref, x.trial, x.created, x.seen, lead).Scan(&id)
		if err != nil {
			return fmt.Errorf("клиент %s: %w", x.id, err)
		}
		r.cust[x.id] = id
		r.rep.add("клиенты", 1)
		ident := func(provider, subject, display string) error {
			if subject == "" {
				return nil
			}
			_, err := r.tx.Exec(r.ctx, `INSERT INTO auth_identities (customer_id, provider, subject, display) VALUES ($1,$2,$3,$4)
				ON CONFLICT DO NOTHING`, id, provider, subject, display)
			if err == nil {
				r.rep.add("способы входа", 1)
			}
			return err
		}
		if x.tg != nil {
			if err := ident("telegram", strconv.FormatInt(*x.tg, 10), "@"+str(x.user)); err != nil {
				return err
			}
		}
		if email != "" {
			if err := ident("email", email, email); err != nil {
				return err
			}
		}
		if err := ident("yandex", str(x.yandex), ""); err != nil {
			return err
		}
		if err := ident("vk", str(x.vk), ""); err != nil {
			return err
		}
		if err := ident("google", str(x.google), ""); err != nil {
			return err
		}
		if x.superadmin {
			r.rep.warn("клиент %s был суперадмином в RWP — админов задаёт ADMIN_TELEGRAM_IDS", displayOf(x.user, x.tg))
		}
	}
	return nil
}

func displayOf(user *string, tg *int64) string {
	if u := str(user); u != "" {
		return "@" + u
	}
	if tg != nil {
		return strconv.FormatInt(*tg, 10)
	}
	return "?"
}

func (r *run) referrals() error {
	rows, ok := r.query("рефералы", `SELECT referrer_id, referee_id FROM referral ref WHERE `+r.botFilter("ref")+` ORDER BY used_at`)
	if !ok {
		return nil
	}
	defer rows.Close()
	type pair struct{ a, b uuid.UUID }
	var list []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.a, &p.b); err != nil {
			return err
		}
		list = append(list, p)
	}
	rows.Close()
	for _, p := range list {
		a, b := r.cust[p.a], r.cust[p.b]
		if a == 0 || b == 0 || a == b {
			continue
		}
		tag, err := r.tx.Exec(r.ctx, `UPDATE customers SET referrer_id=$2 WHERE id=$1 AND referrer_id IS NULL`, b, a)
		if err != nil {
			return err
		}
		r.rep.add("рефералы", int(tag.RowsAffected()))
	}
	return nil
}

func (r *run) paymentMethods() error {
	rows, ok := r.query("способы оплаты", `SELECT id, customer_id, COALESCE(type,''), COALESCE(title,''), COALESCE(card_type,''), COALESCE(last4,''),
			COALESCE(provider,''), COALESCE(provider_payment_method_id,''), created_at
		FROM payment_method pm WHERE COALESCE(is_active,true) AND `+r.botFilter("pm"))
	if !ok {
		return nil
	}
	defer rows.Close()
	type m struct {
		id                                     string
		cust                                   uuid.UUID
		typ, title, card, last4, prov, provRef string
		created                                time.Time
	}
	var list []m
	for rows.Next() {
		var x m
		if err := rows.Scan(&x.id, &x.cust, &x.typ, &x.title, &x.card, &x.last4, &x.prov, &x.provRef, &x.created); err != nil {
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		cid := r.cust[x.cust]
		if cid == 0 {
			continue
		}
		if p := strings.ToLower(x.prov); p != "" && !strings.Contains(p, "yoo") && !strings.Contains(p, "yookassa") {
			r.rep.add("сохранённые методы не ЮKassa (пропущены)", 1)
			continue
		}
		ref := x.provRef
		if ref == "" {
			ref = x.id
		}
		title := x.title
		if title == "" {
			switch {
			case x.last4 != "":
				title = strings.TrimSpace(x.card + " •• " + x.last4)
			case strings.EqualFold(x.typ, "sbp"):
				title = "СБП"
			default:
				title = "Сохранённый способ"
			}
		}
		var id int64
		if err := r.tx.QueryRow(r.ctx, `INSERT INTO payment_methods (customer_id, provider, provider_ref, title, created_at)
			VALUES ($1,'yookassa',$2,$3,$4) ON CONFLICT DO NOTHING RETURNING id`, cid, ref, title, x.created).Scan(&id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return err
		}
		r.method[x.id] = id
		r.rep.add("сохранённые карты/СБП", 1)
	}
	return nil
}

// ---- subscriptions ----

func (r *run) subscriptions() error {
	rows, ok := r.query("подписки", `SELECT id, customer_id, COALESCE(name,''), remnawave_uuid, remnawave_id, COALESCE(subscription_link,''), expire_at, plan_id,
			COALESCE(is_primary,false), COALESCE(devices_count,0), COALESCE(extra_devices,0), COALESCE(autopay_enabled,false), autopay_plan_id,
			COALESCE(autopay_months,0), autopay_period_unit, COALESCE(kind,''), created_at, updated_at, parent_subscription_id
		FROM subscription s WHERE `+r.botFilter("s")+` ORDER BY id`)
	if !ok {
		return errors.New("не удалось прочитать таблицу subscription")
	}
	defer rows.Close()
	type s struct {
		id               int64
		cust             uuid.UUID
		name             string
		rwUUID           *uuid.UUID
		rwID             *int64
		link             string
		expire           *time.Time
		plan             *int64
		primary          bool
		devices, extra   int
		autopay          bool
		apPlan           *int64
		apMonths         int
		apUnit           *string
		kind             string
		created, updated time.Time
		parent           *int64
	}
	var list []s
	for rows.Next() {
		var x s
		if err := rows.Scan(&x.id, &x.cust, &x.name, &x.rwUUID, &x.rwID, &x.link, &x.expire, &x.plan, &x.primary, &x.devices, &x.extra,
			&x.autopay, &x.apPlan, &x.apMonths, &x.apUnit, &x.kind, &x.created, &x.updated, &x.parent); err != nil {
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		cid := r.cust[x.cust]
		if cid == 0 {
			continue
		}
		if x.parent != nil {
			r.rep.add("подписки-«подарки/дочерние» (пропущены)", 1)
			continue
		}
		kind := "paid"
		switch strings.ToLower(x.kind) {
		case "", "paid", "regular", "default", "subscription", "standard":
		case "trial":
			kind = "trial"
		case "free":
			kind = "free"
		default:
			r.rep.unknown("subscription.kind", x.kind)
		}
		var planID *int64
		if x.plan != nil {
			if p := r.plan[*x.plan]; p != 0 {
				planID = &p
			}
		}
		exp := x.created
		if x.expire != nil {
			exp = *x.expire
		}
		title := x.name
		if title == "" && planID != nil {
			_ = r.tx.QueryRow(r.ctx, `SELECT name FROM plans WHERE id=$1`, *planID).Scan(&title)
		}
		// devices_count in RWP is the subscription's total device limit (plan + extra).
		var methodID *int64
		var periodID *int64
		autopay := x.autopay
		if autopay {
			_ = r.tx.QueryRow(r.ctx, `SELECT id FROM payment_methods WHERE customer_id=$1 ORDER BY created_at DESC LIMIT 1`, cid).Scan(&methodID)
			if methodID == nil {
				autopay = false
				r.rep.add("автопродление выключено: нет сохранённой карты", 1)
			} else {
				ap := planID
				if x.apPlan != nil && r.plan[*x.apPlan] != 0 {
					p := r.plan[*x.apPlan]
					ap = &p
				}
				if ap != nil {
					unit := r.unitOf("subscription.autopay_period_unit", x.apUnit)
					n := max(x.apMonths, 1)
					if unit == "year" {
						unit, n = "month", n*12
					}
					_ = r.tx.QueryRow(r.ctx, `SELECT id FROM plan_periods WHERE plan_id=$1 AND unit=$2 AND amount=$3 LIMIT 1`, *ap, unit, n).Scan(&periodID)
				}
			}
		}
		var id int64
		err := r.tx.QueryRow(r.ctx, `INSERT INTO subscriptions (customer_id, plan_id, kind, is_primary, title, rw_user_id, rw_uuid, subscription_url,
				expires_at, device_limit, extra_devices, status, created_at, updated_at, autopay, autopay_method_id, autopay_period_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'active',$12,$13,$14,$15,$16) RETURNING id`,
			cid, planID, kind, x.primary, title, x.rwID, x.rwUUID, x.link, exp, x.devices, x.extra, x.created, x.updated, autopay, methodID, periodID).Scan(&id)
		if err != nil {
			return fmt.Errorf("подписка %d: %w", x.id, err)
		}
		r.sub[x.id] = id
		r.rep.add("подписки", 1)
		if x.rwID == nil {
			r.rep.warn("подписка %d без пользователя Remnawave — проверьте её в карточке клиента", x.id)
		}
	}
	// One primary per customer.
	_, err := r.tx.Exec(r.ctx, `UPDATE subscriptions s SET is_primary = (s.id = (SELECT s2.id FROM subscriptions s2 WHERE s2.customer_id=s.customer_id
		ORDER BY s2.is_primary DESC, s2.expires_at DESC LIMIT 1))`)
	return err
}

// ---- payments ----

func (r *run) purchases() error {
	rows, ok := r.query("платежи", `SELECT id, customer_id, COALESCE(amount,0)::float8, original_amount::float8, COALESCE(currency,''), COALESCE(status,''),
			COALESCE(operation_type,''), COALESCE(invoice_type,''), COALESCE(period,0), period_unit, plan_id, subscription_id, promo_id,
			COALESCE(provider_id,''), COALESCE(device_count,0), traffic_amount, COALESCE(receipt_moy_nalog_id,''), created_at, paid_at, payment_method_id
		FROM purchase p WHERE `+r.botFilter("p")+` ORDER BY id`)
	if !ok {
		return errors.New("не удалось прочитать таблицу purchase")
	}
	defer rows.Close()
	type p struct {
		id                       int64
		cust                     uuid.UUID
		amount                   float64
		orig                     *float64
		cur, status, op, invoice string
		period                   int
		unit                     *string
		plan, sub, promo         *int64
		provider                 string
		devices                  int
		traffic                  *int64
		receipt                  string
		created                  time.Time
		paid                     *time.Time
		method                   *string
	}
	var list []p
	for rows.Next() {
		var x p
		if err := rows.Scan(&x.id, &x.cust, &x.amount, &x.orig, &x.cur, &x.status, &x.op, &x.invoice, &x.period, &x.unit, &x.plan, &x.sub, &x.promo,
			&x.provider, &x.devices, &x.traffic, &x.receipt, &x.created, &x.paid, &x.method); err != nil {
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	sort.SliceStable(list, func(i, j int) bool { return list[i].created.Before(list[j].created) })
	paidSubs := map[int64]bool{}
	for _, x := range list {
		cid := r.cust[x.cust]
		if cid == 0 {
			continue
		}
		if c := strings.ToUpper(x.cur); c != "" && c != "RUB" {
			r.rep.add("платежи в другой валюте (пропущены)", 1)
			continue
		}
		status := ""
		switch strings.ToLower(x.status) {
		case "paid", "succeeded", "success", "completed", "done", "applied":
			status = "succeeded"
		case "pending", "new", "created", "waiting", "processing", "waiting_for_capture":
			status = "canceled" // old unfinished invoices never complete now
		case "cancel", "canceled", "cancelled", "failed", "expired", "error", "rejected":
			status = "canceled"
		case "refunded", "refund":
			status = "refunded"
		default:
			r.rep.unknown("purchase.status", x.status)
			status = "canceled"
		}
		if status == "succeeded" && x.paid == nil {
			t := x.created
			x.paid = &t
		}
		op := ""
		switch strings.ToLower(x.op) {
		case "buy", "new", "subscription", "new_subscription", "":
			op = "buy"
		case "purchase":
			// RWP calls renewals "purchase" too: a paid purchase for a subscription that was paid before is a renewal.
			op = "buy"
			if x.sub != nil && paidSubs[*x.sub] {
				op = "renew"
			}
		case "renew", "renewal", "extend", "prolong", "prolongation":
			op = "renew"
		case "change", "upgrade", "plan_change", "change_plan", "downgrade", "conversion":
			op = "change"
		case "devices", "device", "extra_devices", "add_devices":
			op = "devices"
		case "traffic", "add_traffic", "traffic_topup":
			op = "traffic"
		case "autopay", "auto_renew", "autorenew", "recurring":
			op = "autopay"
		default:
			r.rep.unknown("purchase.operation_type", x.op)
			op = "buy"
		}
		autopay := op == "autopay" || strings.Contains(strings.ToLower(x.invoice), "auto")
		unit := r.unitOf("purchase.period_unit", x.unit)
		months, days := coverage(unit, x.period)
		if op == "devices" || op == "traffic" {
			months, days = 0, 0
		}
		amount := int64(math.Round(x.amount * 100))
		base := amount
		if x.orig != nil && *x.orig > 0 {
			base = int64(math.Round(*x.orig * 100))
		}
		disc := 0
		if base > amount && base > 0 {
			disc = int(math.Round(float64(base-amount) / float64(base) * 100))
		}
		var planID, subID *int64
		if x.plan != nil && r.plan[*x.plan] != 0 {
			v := r.plan[*x.plan]
			planID = &v
		}
		if x.sub != nil && r.sub[*x.sub] != 0 {
			v := r.sub[*x.sub]
			subID = &v
		}
		receipt := "none"
		if x.receipt != "" {
			receipt = "done"
		}
		title := ""
		if planID != nil {
			_ = r.tx.QueryRow(r.ctx, `SELECT name FROM plans WHERE id=$1`, *planID).Scan(&title)
		}
		var provRef *string
		if x.provider != "" {
			provRef = &x.provider
		}
		var methodID *int64
		if x.method != nil && r.method[*x.method] != 0 {
			v := r.method[*x.method]
			methodID = &v
		}
		id := uuid.New()
		_, err := r.tx.Exec(r.ctx, `INSERT INTO payments (id, customer_id, subscription_id, plan_id, operation, title, days, months, extra_devices,
				amount, base_amount, discount_percent, provider, provider_ref, status, created_at, paid_at, applied_at, receipt_status, receipt_uuid,
				referral_done, autopay, payment_method_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'yookassa',$13,$14,$15,$16,$17,$18,$19,true,$20,$21)`,
			id, cid, subID, planID, op, title, days, months, x.devices, amount, base, disc, provRef, status, x.created, x.paid,
			appliedAt(status, x.paid), receipt, x.receipt, autopay, methodID)
		if err != nil {
			return fmt.Errorf("платёж %d: %w", x.id, err)
		}
		r.pay[x.id] = id
		if status == "succeeded" && x.sub != nil {
			paidSubs[*x.sub] = true
		}
		r.rep.add("платежи", 1)
		if status == "succeeded" {
			r.rep.add("платежи: оплаченные", 1)
		}
	}
	return nil
}

func appliedAt(status string, paid *time.Time) *time.Time {
	if status == "succeeded" || status == "refunded" {
		return paid
	}
	return nil
}

func (r *run) planChanges() error {
	rows, ok := r.query("смены тарифа", `SELECT customer_id, subscription_id, from_plan_id, to_plan_id, COALESCE(change_type,''), COALESCE(value_before,0),
			COALESCE(value_after,0), created_at FROM plan_change_history ORDER BY id`)
	if !ok {
		return nil
	}
	defer rows.Close()
	type c struct {
		cust          uuid.UUID
		sub, from, to *int64
		typ           string
		before, after int
		at            time.Time
	}
	var list []c
	for rows.Next() {
		var x c
		if err := rows.Scan(&x.cust, &x.sub, &x.from, &x.to, &x.typ, &x.before, &x.after, &x.at); err != nil {
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	m := func(id *int64, mp map[int64]int64) *int64 {
		if id == nil || mp[*id] == 0 {
			return nil
		}
		v := mp[*id]
		return &v
	}
	for _, x := range list {
		cid := r.cust[x.cust]
		if cid == 0 {
			continue
		}
		mode := "admin"
		switch t := strings.ToLower(x.typ); {
		case strings.Contains(t, "upgrade") || strings.Contains(t, "pay"):
			mode = "upgrade"
		case strings.Contains(t, "conver"):
			mode = "convert"
		case strings.Contains(t, "down"):
			mode = "downgrade"
		case t == "" || strings.Contains(t, "admin"):
		default:
			r.rep.unknown("plan_change_history.change_type", x.typ)
		}
		if _, err := r.tx.Exec(r.ctx, `INSERT INTO plan_changes (customer_id, subscription_id, from_plan_id, to_plan_id, mode, days_before, days_after, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, cid, m(x.sub, r.sub), m(x.from, r.plan), m(x.to, r.plan), mode, x.before, x.after, x.at); err != nil {
			return err
		}
		r.rep.add("смены тарифа", 1)
	}
	return nil
}

// ---- promo codes ----

func (r *run) promos() error {
	rows, ok := r.query("промокоды", `SELECT id, code, COALESCE(bonus_days,0), COALESCE(max_uses,0), expires_at, COALESCE(active,true), created_at,
			COALESCE(promo_type,''), COALESCE(discount_percent,0), owner_customer_id, allowed_plans, unlocked_plan_id
		FROM promo pr WHERE `+r.botFilter("pr")+` ORDER BY id`)
	if !ok {
		return nil
	}
	defer rows.Close()
	type pr struct {
		id            int64
		code          string
		days, maxUses int
		expires       *time.Time
		active        bool
		created       time.Time
		typ           string
		discount      int
		owner         *uuid.UUID
		allowed       []byte
		unlock        *int64
	}
	var list []pr
	for rows.Next() {
		var x pr
		if err := rows.Scan(&x.id, &x.code, &x.days, &x.maxUses, &x.expires, &x.active, &x.created, &x.typ, &x.discount, &x.owner, &x.allowed, &x.unlock); err != nil {
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		kind, value := "", 0
		var unlockPlans []int64
		switch {
		case strings.EqualFold(x.typ, "unlock_plan") || x.unlock != nil:
			if x.unlock == nil || r.plan[*x.unlock] == 0 {
				r.rep.warn("промокод %s открывает тариф, которого нет среди перенесённых — не перенесён", x.code)
				continue
			}
			kind, value, unlockPlans = "unlock", 1, []int64{r.plan[*x.unlock]}
		case x.discount > 0 && x.days == 0:
			kind, value = "discount", min(x.discount, 100)
		case x.days > 0:
			kind, value = "days", x.days
		default:
			r.rep.warn("промокод %s (%s) не перенесён: ни скидки, ни дней (промокоды на трафик у нас не поддерживаются)", x.code, x.typ)
			continue
		}
		var plans []int64
		if len(x.allowed) > 0 {
			var raw []any
			if json.Unmarshal(x.allowed, &raw) == nil {
				for _, v := range raw {
					if f, ok := v.(float64); ok && r.plan[int64(f)] != 0 {
						plans = append(plans, r.plan[int64(f)])
					}
				}
			}
		}
		if unlockPlans != nil {
			plans = unlockPlans
		}
		if plans == nil {
			plans = []int64{}
		}
		var owner *int64
		if x.owner != nil && r.cust[*x.owner] != 0 {
			v := r.cust[*x.owner]
			owner = &v
		}
		var id int64
		err := r.tx.QueryRow(r.ctx, `INSERT INTO promo_codes (code, kind, value, folder, description, is_active, max_uses, valid_until, plan_ids, customer_id, created_at)
			VALUES ($1,$2,$3,'Из RWP','',$4,$5,$6,$7,$8,$9) ON CONFLICT (code) DO NOTHING RETURNING id`,
			x.code, kind, value, x.active, x.maxUses, x.expires, plans, owner, x.created).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			r.rep.warn("промокод %s повторяется — оставлен первый", x.code)
			continue
		}
		if err != nil {
			return fmt.Errorf("промокод %s: %w", x.code, err)
		}
		r.promo[x.id] = id
		r.rep.add("промокоды", 1)
	}
	rows, ok = r.query("использования промокодов", `SELECT promo_id, customer_id, subscription_id, used_at FROM promo_usage ORDER BY id`)
	if !ok {
		return nil
	}
	defer rows.Close()
	type u struct {
		promo int64
		cust  uuid.UUID
		sub   *int64
		at    time.Time
	}
	var uses []u
	for rows.Next() {
		var x u
		if err := rows.Scan(&x.promo, &x.cust, &x.sub, &x.at); err != nil {
			return err
		}
		uses = append(uses, x)
	}
	rows.Close()
	for _, x := range uses {
		pid, cid := r.promo[x.promo], r.cust[x.cust]
		if pid == 0 || cid == 0 {
			continue
		}
		var subID *int64
		if x.sub != nil && r.sub[*x.sub] != 0 {
			v := r.sub[*x.sub]
			subID = &v
		}
		if _, err := r.tx.Exec(r.ctx, `INSERT INTO promo_redemptions (promo_id, customer_id, status, subscription_id, days, created_at, used_at)
			SELECT $1, $2, 'used', $3, CASE WHEN kind='days' THEN value ELSE 0 END, $4, $4 FROM promo_codes WHERE id=$1
			ON CONFLICT DO NOTHING`, pid, cid, subID, x.at); err != nil {
			return err
		}
		r.rep.add("использования промокодов", 1)
	}
	return nil
}

// ---- referral bonuses ----

func (r *run) referralBonuses() error {
	rows, ok := r.query("реферальные бонусы", `SELECT ref.referrer_id, ref.referee_id, h.purchase_id, COALESCE(h.bonus_days,0), COALESCE(h.is_first_bonus,false),
			COALESCE(h.is_referee,false), COALESCE(h.bonus_type,''), h.granted_at
		FROM referral_bonus_history h JOIN referral ref ON ref.id = h.referral_id ORDER BY h.id`)
	if !ok {
		return nil
	}
	defer rows.Close()
	type b struct {
		referrer, referee uuid.UUID
		purchase          *int64
		days              int
		first, isReferee  bool
		typ               string
		at                time.Time
	}
	var list []b
	for rows.Next() {
		var x b
		if err := rows.Scan(&x.referrer, &x.referee, &x.purchase, &x.days, &x.first, &x.isReferee, &x.typ, &x.at); err != nil {
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		recipient, referral := r.cust[x.referrer], r.cust[x.referee]
		kind := "recurring"
		switch strings.ToLower(x.typ) {
		case "first":
			kind = "first"
		case "recurring":
		case "referee":
			recipient, kind = r.cust[x.referee], "invitee"
		case "manual":
			kind = "manual"
		case "manual_referee":
			recipient, kind = r.cust[x.referee], "manual"
		default:
			switch {
			case x.isReferee:
				recipient, kind = r.cust[x.referee], "invitee"
			case x.first:
				kind = "first"
			}
			if x.typ != "" {
				r.rep.unknown("referral_bonus_history.bonus_type", x.typ)
			}
		}
		if recipient == 0 || x.days == 0 {
			continue
		}
		var payID *uuid.UUID
		if x.purchase != nil {
			if p, ok := r.pay[*x.purchase]; ok {
				payID = &p
			}
		}
		var refID *int64
		if referral != 0 {
			refID = &referral
		}
		if _, err := r.tx.Exec(r.ctx, `INSERT INTO referral_bonuses (recipient_id, referral_id, payment_id, kind, days, created_at)
			VALUES ($1,$2,$3,$4,$5,$6)`, recipient, refID, payID, kind, x.days, x.at); err != nil {
			return err
		}
		r.rep.add("реферальные бонусы", 1)
	}
	return nil
}

// ---- small things ----

func (r *run) deviceNames() error {
	rows, ok := r.query("имена устройств", `SELECT subscription_id, hwid, readable_name FROM device_names`)
	if !ok {
		return nil
	}
	defer rows.Close()
	type d struct {
		sub        int64
		hwid, name string
	}
	var list []d
	for rows.Next() {
		var x d
		if err := rows.Scan(&x.sub, &x.hwid, &x.name); err != nil {
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		sid := r.sub[x.sub]
		if sid == 0 || strings.TrimSpace(x.name) == "" {
			continue
		}
		if _, err := r.tx.Exec(r.ctx, `INSERT INTO device_names (subscription_id, hwid, name) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, sid, x.hwid, x.name); err != nil {
			return err
		}
		r.rep.add("имена устройств", 1)
	}
	return nil
}

func (r *run) faq() error {
	rows, ok := r.query("FAQ", `SELECT COALESCE(s.title,''), f.question, f.answer, COALESCE(f.is_active,true), COALESCE(s.sort_order,0)*1000 + COALESCE(f.sort_order,0)
		FROM faq f LEFT JOIN faq_sections s ON s.id = f.section_id WHERE `+r.botFilter("f")+` ORDER BY 5`)
	if !ok {
		return nil
	}
	defer rows.Close()
	type f struct {
		section, q, a string
		active        bool
		sort          int
	}
	var list []f
	for rows.Next() {
		var x f
		if err := rows.Scan(&x.section, &x.q, &x.a, &x.active, &x.sort); err != nil {
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		if _, err := r.tx.Exec(r.ctx, `INSERT INTO faq_items (section, question, answer, is_active, sort) VALUES ($1,$2,$3,$4,$5)`,
			x.section, x.q, x.a, x.active, x.sort); err != nil {
			return err
		}
		r.rep.add("FAQ", 1)
	}
	return nil
}

func (r *run) tickets() error {
	rows, ok := r.query("обращения", `SELECT id, customer_id, COALESCE(subject,''), COALESCE(status,''), created_at, updated_at, closed_at, first_response_at
		FROM support_ticket t WHERE `+r.botFilter("t")+` ORDER BY id`)
	if !ok {
		return nil
	}
	defer rows.Close()
	type t struct {
		id               int64
		cust             uuid.UUID
		subject, status  string
		created, updated time.Time
		closed, first    *time.Time
	}
	var list []t
	for rows.Next() {
		var x t
		if err := rows.Scan(&x.id, &x.cust, &x.subject, &x.status, &x.created, &x.updated, &x.closed, &x.first); err != nil {
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	ids := map[int64]int64{}
	for _, x := range list {
		cid := r.cust[x.cust]
		if cid == 0 {
			continue
		}
		status := "closed"
		switch strings.ToLower(x.status) {
		case "open", "new", "in_progress", "pending", "escalated", "assigned":
			status = "open"
		case "waiting", "waiting_customer", "awaiting_customer", "waiting_for_customer":
			status = "waiting"
		case "resolved", "solved":
			status = "resolved"
		case "closed", "archived", "done":
		default:
			r.rep.unknown("support_ticket.status", x.status)
		}
		var id int64
		if err := r.tx.QueryRow(r.ctx, `INSERT INTO tickets (customer_id, subject, status, ai_enabled, unread_admin, created_at, updated_at,
				last_customer_at, first_response_at, closed_at, resolved_at)
			VALUES ($1,$2,$3,false,$4,$5,$6,$5,$7,$8,$8) RETURNING id`, cid, x.subject, status, status == "open", x.created, x.updated, x.first, x.closed).Scan(&id); err != nil {
			return err
		}
		ids[x.id] = id
		r.rep.add("обращения", 1)
	}
	rows, ok = r.query("сообщения обращений", `SELECT ticket_id, COALESCE(sender_type,''), COALESCE(message,''), created_at FROM support_message
		WHERE deleted_at IS NULL ORDER BY id`)
	if !ok {
		return nil
	}
	defer rows.Close()
	type m struct {
		ticket       int64
		sender, text string
		at           time.Time
	}
	var msgs []m
	for rows.Next() {
		var x m
		if err := rows.Scan(&x.ticket, &x.sender, &x.text, &x.at); err != nil {
			return err
		}
		msgs = append(msgs, x)
	}
	rows.Close()
	for _, x := range msgs {
		tid := ids[x.ticket]
		if tid == 0 || strings.TrimSpace(x.text) == "" {
			continue
		}
		author := "operator"
		switch strings.ToLower(x.sender) {
		case "customer", "user", "client":
			author = "customer"
		case "ai", "bot", "assistant": // RWP's "bot" is its AI assistant
			author = "ai"
		case "system":
			author = "system"
		case "note", "internal":
			author = "note"
		case "admin", "operator", "agent", "support", "staff":
		default:
			r.rep.unknown("support_message.sender_type", x.sender)
		}
		if _, err := r.tx.Exec(r.ctx, `INSERT INTO ticket_messages (ticket_id, author, body, created_at) VALUES ($1,$2,$3,$4)`, tid, author, x.text, x.at); err != nil {
			return err
		}
		r.rep.add("сообщения обращений", 1)
	}
	_, err := r.tx.Exec(r.ctx, `UPDATE tickets t SET last_customer_at = COALESCE((SELECT max(created_at) FROM ticket_messages m
		WHERE m.ticket_id=t.id AND m.author='customer'), t.created_at) WHERE id = ANY($1)`, values(ids))
	return err
}

func values(m map[int64]int64) []int64 {
	out := make([]int64, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// unlockedPlans: hidden plans customers opened with promo codes.
func (r *run) unlockedPlans() error {
	rows, ok := r.query("открытые тарифы", `SELECT customer_id, plan_id, promo_id, COALESCE(unlocked_at, now()) FROM customer_unlocked_plans`)
	if !ok {
		return nil
	}
	defer rows.Close()
	type u struct {
		cust  uuid.UUID
		plan  int64
		promo *int64
		at    time.Time
	}
	var list []u
	for rows.Next() {
		var x u
		if err := rows.Scan(&x.cust, &x.plan, &x.promo, &x.at); err != nil {
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		cid, pid := r.cust[x.cust], r.plan[x.plan]
		if cid == 0 || pid == 0 {
			continue
		}
		var promo *int64
		if x.promo != nil && r.promo[*x.promo] != 0 {
			v := r.promo[*x.promo]
			promo = &v
		}
		tag, err := r.tx.Exec(r.ctx, `INSERT INTO unlocked_plans (customer_id, plan_id, promo_id, created_at) VALUES ($1,$2,$3,$4)
			ON CONFLICT DO NOTHING`, cid, pid, promo, x.at)
		if err != nil {
			return err
		}
		r.rep.add("открытые скрытые тарифы", int(tag.RowsAffected()))
	}
	return nil
}

// Bot is one bot of an RWP install (partner bots share the database).
type Bot struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Username  string `json:"username"`
	Customers int64  `json:"customers"`
}

// ListBots returns the bots in an RWP database, with how many customers each has.
func ListBots(ctx context.Context, src *pgxpool.Pool) ([]Bot, error) {
	rows, err := src.Query(ctx, `SELECT b.id, COALESCE(b.type,''), COALESCE(b.tg_username,''),
		(SELECT count(*) FROM customer c WHERE c.bot_id = b.id) FROM bot b ORDER BY b.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Bot{}
	for rows.Next() {
		var b Bot
		if err := rows.Scan(&b.ID, &b.Type, &b.Username, &b.Customers); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LooksLikeRWP checks that the restored database has RWP's core tables.
func LooksLikeRWP(ctx context.Context, src *pgxpool.Pool) error {
	var n int
	if err := src.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public'
		AND table_name IN ('customer','subscription','purchase','plan')`).Scan(&n); err != nil {
		return err
	}
	if n < 4 {
		return errors.New("в файле нет таблиц RWP Shop (customer, subscription, purchase, plan) — похоже, это не та база")
	}
	return nil
}
