package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"bshop/internal/store"
)

// Statistics for the admin panel. Everything is computed on request from the live tables:
// for a shop of tens of thousands of customers a handful of aggregate queries is cheap enough,
// and there is no second copy of the numbers to drift out of sync.

// StatsRange is the period a report covers, in the shop's timezone.
type StatsRange struct {
	From, To  time.Time // [From, To)
	PrevFrom  time.Time // previous period of the same length, for "Δ to previous"
	Group     string    // day | week | month
	TZ        string
	Period    string
	loc       *time.Location
	unbounded bool
}

// statsRange parses period (7d, 1m, 3m, 6m, 12m, all) and group (day, week, month).
func (s *Service) statsRange(period, group string) StatsRange {
	loc := s.Settings.Get().General.Location()
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	r := StatsRange{To: today.AddDate(0, 0, 1), TZ: loc.String(), Period: period, loc: loc}
	switch period {
	case "7d":
		r.From = today.AddDate(0, 0, -6)
	case "1w":
		r.From = today.AddDate(0, 0, -6)
	case "3m":
		r.From = today.AddDate(0, -3, 0)
	case "6m":
		r.From = today.AddDate(0, -6, 0)
	case "12m":
		r.From = today.AddDate(-1, 0, 0)
	case "all":
		r.From = time.Date(2020, 1, 1, 0, 0, 0, 0, loc)
		r.unbounded = true
	default:
		r.Period = "1m"
		r.From = today.AddDate(0, -1, 0)
	}
	if r.unbounded {
		var first *time.Time
		_ = s.Store.Pool.QueryRow(context.Background(), `SELECT min(created_at) FROM customers`).Scan(&first)
		if first != nil {
			f := first.In(loc)
			r.From = time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, loc)
		}
	}
	r.PrevFrom = r.From.Add(-r.To.Sub(r.From))
	switch group {
	case "day", "week", "month":
		r.Group = group
	default:
		days := r.To.Sub(r.From).Hours() / 24
		switch {
		case days <= 62:
			r.Group = "day"
		case days <= 200:
			r.Group = "week"
		default:
			r.Group = "month"
		}
	}
	return r
}

// Point is one bucket of a time series: T is the bucket start (YYYY-MM-DD), V the values.
type Point struct {
	T string  `json:"t"`
	V []int64 `json:"v"`
}

// series fills every bucket in r (empty ones with zeros). inner must select
// `date_trunc($3, <ts> AT TIME ZONE $4) AS t` and n numeric columns, filtered by $1 <= ts < $2.
func (s *Service) series(ctx context.Context, r StatsRange, n int, inner string, args ...any) ([]Point, error) {
	cols := make([]string, n)
	for i := range cols {
		cols[i] = fmt.Sprintf("COALESCE(x.c%d, 0)::bigint", i)
	}
	inner = renameCols(inner, n)
	q := `WITH b AS (SELECT generate_series(date_trunc($3::text, $1::timestamptz AT TIME ZONE $4::text),
			date_trunc($3::text, ($2::timestamptz - interval '1 second') AT TIME ZONE $4::text), ('1 ' || $3::text)::interval) AS t)
		SELECT to_char(b.t, 'YYYY-MM-DD'), ` + strings.Join(cols, ", ") + ` FROM b LEFT JOIN (` + inner + `) x ON x.t = b.t ORDER BY b.t`
	all := append([]any{r.From, r.To, r.Group, r.TZ}, args...)
	rows, err := s.Store.Pool.Query(ctx, q, all...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Point{}
	for rows.Next() {
		p := Point{V: make([]int64, n)}
		dst := []any{&p.T}
		for i := range p.V {
			dst = append(dst, &p.V[i])
		}
		if err := rows.Scan(dst...); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// renameCols makes sure the inner query's value columns are named c0..cN-1: callers write
// "... AS t, count(*) AS c0, sum(x) AS c1" themselves; this only validates it.
func renameCols(inner string, n int) string {
	for i := 0; i < n; i++ {
		if !strings.Contains(inner, fmt.Sprintf(" c%d", i)) {
			panic(fmt.Sprintf("stats: series column c%d missing", i))
		}
	}
	return inner
}

// Named is a labelled count/sum pair for breakdowns.
type Named struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int64  `json:"count"`
	Sum   int64  `json:"sum"`
}

func (s *Service) named(ctx context.Context, q string, args ...any) ([]Named, error) {
	rows, err := s.Store.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Named{}
	for rows.Next() {
		var n Named
		if err := rows.Scan(&n.Key, &n.Label, &n.Count, &n.Sum); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// scan runs a single-row query into dst.
func (s *Service) scan(ctx context.Context, q string, args []any, dst ...any) error {
	return s.Store.Pool.QueryRow(ctx, q, args...).Scan(dst...)
}

// Delta is a value for the current and previous period.
type Delta struct {
	Cur  int64 `json:"cur"`
	Prev int64 `json:"prev"`
}

var methodNames = map[string]string{"sbp": "СБП", "bank_card": "Карта", "sberbank": "SberPay", "tinkoff_bank": "T-Pay",
	"yoo_money": "ЮMoney", "mir_pay": "Mir Pay", "": "Не указан", "saved": "Сохранённая карта"}

var operationNames = map[string]string{"buy": "Покупка", "renew": "Продление", "change": "Смена тарифа", "devices": "Устройства",
	"traffic": "Трафик", "autopay": "Автопродление"}

var providerNames = map[string]string{"telegram": "Telegram", "telegram_bot": "Telegram", "telegram_webapp": "Telegram",
	"email": "Почта", "yandex": "Яндекс", "vk": "VK", "google": "Google"}

// paidOps are operations that buy subscription time (MRR, cohorts, LTV).
const paidOps = `('buy','renew','change','autopay')`

// ---------------------------------------------------------------------------------------------
// Overview

type OverviewStats struct {
	Range    StatsRange `json:"range"`
	Segments []struct {
		Key        string `json:"key"`
		Label      string `json:"label"`
		All        int64  `json:"all"`
		BotBlocked int64  `json:"bot_blocked"`
		Blocked    int64  `json:"blocked"`
	} `json:"segments"`
	Users struct {
		Total, DAU, MAU, NewToday, NewWeek int64
	} `json:"users"`
	Plans         []Named `json:"plans"`
	SubsTotal     int64   `json:"subs_total"`
	SubsExpired   int64   `json:"subs_expired"`
	SubsUnlimited int64   `json:"subs_unlimited"`
	Revenue       struct {
		Today, Week, Month                   Delta
		AvgCheck, AllTime                    int64
		Expiring7, Expiring30, Expiring30Sum int64
	} `json:"revenue"`
	NewUsers  []Point `json:"new_users"`
	Income    []Point `json:"income"` // v: [sum, count]
	Methods   []Named `json:"methods"`
	Referrals struct {
		Referred, ActiveReferrers, BonusDays, Paid int64
	} `json:"referrals"`
	Promos struct {
		Total, Active, Uses, BonusDays int64
	} `json:"promos"`
	ByPlan    []Named                               `json:"by_plan"`
	ByMonths  []Named                               `json:"by_months"`
	Purchases []Point                               `json:"purchases"` // v: [buy, renew, change, autopay, extra] counts per bucket
	Autopay   struct{ Enabled, SavedMethods int64 } `json:"autopay"`
	Trial     struct{ Used, Paid int64 }            `json:"trial"`
	Extras    []Named                               `json:"extras"`
	Logins    struct {
		Sessions, Today, Week, Month int64
		ByProvider                   []Named `json:"by_provider"`
	} `json:"logins"`
	Languages []Named `json:"languages"`
}

func (s *Service) StatsOverview(ctx context.Context, period, group string) (*OverviewStats, error) {
	r := s.statsRange(period, group)
	o := &OverviewStats{Range: r}
	now := time.Now().In(r.loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, r.loc)
	weekStart := today.AddDate(0, 0, -6)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, r.loc)

	// Segments by the customer's longest-running subscription.
	rows, err := s.Store.Pool.Query(ctx, `
		WITH x AS (
			SELECT c.id, c.bot_blocked, c.is_blocked,
				(SELECT max(expires_at) FROM subscriptions s WHERE s.customer_id=c.id AND s.status <> 'deleted') AS exp
			FROM customers c)
		SELECT seg, count(*), count(*) FILTER (WHERE bot_blocked), count(*) FILTER (WHERE is_blocked) FROM (
			SELECT *, CASE
				WHEN exp IS NULL THEN 'none'
				WHEN exp > now() + interval '10 years' THEN 'unlimited'
				WHEN exp > now() + interval '7 days' THEN 'active'
				WHEN exp > now() THEN 'expiring'
				WHEN exp > now() - interval '30 days' THEN 'expired30'
				ELSE 'expired_old' END AS seg FROM x) y
		GROUP BY seg`)
	if err != nil {
		return nil, err
	}
	segOrder := []string{"expiring", "active", "unlimited", "expired30", "expired_old", "none"}
	segLabels := map[string]string{"expiring": "Истекает ≤ 7 дней", "active": "Активные", "unlimited": "Без срока",
		"expired30": "Истекла ≤ 30 дней", "expired_old": "Истекла давно", "none": "Без подписки"}
	got := map[string][3]int64{}
	for rows.Next() {
		var k string
		var a, b, c int64
		if err := rows.Scan(&k, &a, &b, &c); err != nil {
			rows.Close()
			return nil, err
		}
		got[k] = [3]int64{a, b, c}
	}
	rows.Close()
	for _, k := range segOrder {
		v := got[k]
		o.Segments = append(o.Segments, struct {
			Key        string `json:"key"`
			Label      string `json:"label"`
			All        int64  `json:"all"`
			BotBlocked int64  `json:"bot_blocked"`
			Blocked    int64  `json:"blocked"`
		}{k, segLabels[k], v[0], v[1], v[2]})
	}

	if err := s.scan(ctx, `SELECT count(*),
			count(*) FILTER (WHERE last_seen_at >= $1), count(*) FILTER (WHERE last_seen_at >= now() - interval '30 days'),
			count(*) FILTER (WHERE created_at >= $1), count(*) FILTER (WHERE created_at >= $2) FROM customers`,
		[]any{today, weekStart}, &o.Users.Total, &o.Users.DAU, &o.Users.MAU, &o.Users.NewToday, &o.Users.NewWeek); err != nil {
		return nil, err
	}

	if o.Plans, err = s.named(ctx, `SELECT COALESCE(p.id::text, '0'), COALESCE(p.name, 'Без тарифа'), count(*), 0::bigint
		FROM subscriptions s LEFT JOIN plans p ON p.id = s.plan_id
		WHERE s.status='active' AND s.expires_at > now() GROUP BY 1, 2 ORDER BY 3 DESC`); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT count(*), count(*) FILTER (WHERE expires_at <= now()), count(*) FILTER (WHERE expires_at > now() + interval '10 years')
		FROM subscriptions WHERE status <> 'deleted'`, nil, &o.SubsTotal, &o.SubsExpired, &o.SubsUnlimited); err != nil {
		return nil, err
	}

	rev := func(from, to time.Time) (int64, error) {
		var v int64
		err := s.scan(ctx, `SELECT COALESCE(sum(amount),0) FROM payments WHERE status='succeeded' AND paid_at >= $1 AND paid_at < $2`, []any{from, to}, &v)
		return v, err
	}
	pairs := []struct {
		d        *Delta
		from, to time.Time
		pf, pt   time.Time
	}{
		{&o.Revenue.Today, today, today.AddDate(0, 0, 1), today.AddDate(0, 0, -1), today},
		{&o.Revenue.Week, weekStart, today.AddDate(0, 0, 1), weekStart.AddDate(0, 0, -7), weekStart},
		{&o.Revenue.Month, monthStart, today.AddDate(0, 0, 1), monthStart.AddDate(0, -1, 0), monthStart.AddDate(0, -1, today.Day())},
	}
	for _, p := range pairs {
		if p.d.Cur, err = rev(p.from, p.to); err != nil {
			return nil, err
		}
		if p.d.Prev, err = rev(p.pf, p.pt); err != nil {
			return nil, err
		}
	}
	if err := s.scan(ctx, `SELECT COALESCE(sum(amount),0), COALESCE(avg(amount),0)::bigint FROM payments WHERE status='succeeded'`, nil,
		&o.Revenue.AllTime, &o.Revenue.AvgCheck); err != nil {
		return nil, err
	}
	// Money at risk: paid subscriptions ending soon without autopay, valued at their last payment.
	if err := s.scan(ctx, `SELECT count(*) FILTER (WHERE s.expires_at <= now() + interval '7 days'), count(*),
			COALESCE(sum((SELECT p.amount FROM payments p WHERE p.subscription_id=s.id AND p.status='succeeded' ORDER BY p.paid_at DESC LIMIT 1)),0)
		FROM subscriptions s WHERE s.kind='paid' AND s.status='active' AND s.expires_at > now() AND s.expires_at <= now() + interval '30 days' AND NOT s.autopay`,
		nil, &o.Revenue.Expiring7, &o.Revenue.Expiring30, &o.Revenue.Expiring30Sum); err != nil {
		return nil, err
	}

	if o.NewUsers, err = s.series(ctx, r, 1, `SELECT date_trunc($3, created_at AT TIME ZONE $4) AS t, count(*) AS c0
		FROM customers WHERE created_at >= $1 AND created_at < $2 GROUP BY 1`); err != nil {
		return nil, err
	}
	if o.Income, err = s.series(ctx, r, 2, `SELECT date_trunc($3, paid_at AT TIME ZONE $4) AS t, sum(amount) AS c0, count(*) AS c1
		FROM payments WHERE status='succeeded' AND paid_at >= $1 AND paid_at < $2 GROUP BY 1`); err != nil {
		return nil, err
	}
	if o.Methods, err = s.named(ctx, `SELECT CASE WHEN autopay THEN 'saved' ELSE method END, '', count(*), sum(amount) FROM payments
		WHERE status='succeeded' AND paid_at >= $1 AND paid_at < $2 GROUP BY 1 ORDER BY 4 DESC`, r.From, r.To); err != nil {
		return nil, err
	}
	for i := range o.Methods {
		o.Methods[i].Label = label(methodNames, o.Methods[i].Key)
	}

	if err := s.scan(ctx, `SELECT
			(SELECT count(*) FROM customers WHERE referrer_id IS NOT NULL),
			(SELECT count(DISTINCT c.referrer_id) FROM customers c WHERE c.referrer_id IS NOT NULL
				AND EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=c.id AND p.status='succeeded')),
			(SELECT COALESCE(sum(days),0) FROM referral_bonuses),
			(SELECT count(*) FROM customers c WHERE c.referrer_id IS NOT NULL
				AND EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=c.id AND p.status='succeeded'))`, nil,
		&o.Referrals.Referred, &o.Referrals.ActiveReferrers, &o.Referrals.BonusDays, &o.Referrals.Paid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT count(*) FILTER (WHERE customer_id IS NULL),
			count(*) FILTER (WHERE customer_id IS NULL AND is_active AND (valid_until IS NULL OR valid_until > now())),
			(SELECT count(*) FROM promo_redemptions WHERE status='used'),
			(SELECT COALESCE(sum(days),0) FROM promo_redemptions WHERE status='used') FROM promo_codes`, nil,
		&o.Promos.Total, &o.Promos.Active, &o.Promos.Uses, &o.Promos.BonusDays); err != nil {
		return nil, err
	}

	if o.ByPlan, err = s.named(ctx, `SELECT COALESCE(pl.id::text,'0'), COALESCE(pl.name, 'Удалённый тариф'), count(*), sum(p.amount)
		FROM payments p LEFT JOIN plans pl ON pl.id = p.plan_id
		WHERE p.status='succeeded' AND p.operation IN `+paidOps+` AND p.paid_at >= $1 AND p.paid_at < $2 GROUP BY 1,2 ORDER BY 4 DESC`, r.From, r.To); err != nil {
		return nil, err
	}
	if o.ByMonths, err = s.named(ctx, `SELECT m::text, '', count(*), sum(amount) FROM (
			SELECT CASE WHEN months > 0 THEN months WHEN days >= 28 THEN round(days/30.0)::int ELSE 0 END AS m, amount
			FROM payments WHERE status='succeeded' AND operation IN `+paidOps+` AND paid_at >= $1 AND paid_at < $2) x
		GROUP BY m ORDER BY m`, r.From, r.To); err != nil {
		return nil, err
	}
	for i, n := range o.ByMonths {
		if n.Key == "0" {
			o.ByMonths[i].Label = "Меньше месяца"
		} else {
			o.ByMonths[i].Label = n.Key + " мес."
		}
	}
	if o.Purchases, err = s.series(ctx, r, 5, `SELECT date_trunc($3, paid_at AT TIME ZONE $4) AS t,
			count(*) FILTER (WHERE operation='buy') AS c0, count(*) FILTER (WHERE operation='renew') AS c1,
			count(*) FILTER (WHERE operation='change') AS c2, count(*) FILTER (WHERE operation='autopay') AS c3,
			count(*) FILTER (WHERE operation IN ('devices','traffic')) AS c4
		FROM payments WHERE status='succeeded' AND paid_at >= $1 AND paid_at < $2 GROUP BY 1`); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT (SELECT count(*) FROM subscriptions WHERE autopay AND status='active' AND expires_at > now()),
		(SELECT count(DISTINCT customer_id) FROM payment_methods)`, nil, &o.Autopay.Enabled, &o.Autopay.SavedMethods); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT count(*), count(*) FILTER (WHERE EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=c.id AND p.status='succeeded'))
		FROM customers c WHERE trial_used`, nil, &o.Trial.Used, &o.Trial.Paid); err != nil {
		return nil, err
	}
	if o.Extras, err = s.named(ctx, `SELECT operation, '', count(*), sum(amount) FROM payments
		WHERE status='succeeded' AND operation IN ('devices','traffic') AND paid_at >= $1 AND paid_at < $2 GROUP BY 1`, r.From, r.To); err != nil {
		return nil, err
	}
	for i := range o.Extras {
		o.Extras[i].Label = label(operationNames, o.Extras[i].Key)
	}

	if err := s.scan(ctx, `SELECT (SELECT count(*) FROM sessions WHERE expires_at > now()),
			count(DISTINCT customer_id) FILTER (WHERE created_at >= $1),
			count(DISTINCT customer_id) FILTER (WHERE created_at >= $2),
			count(DISTINCT customer_id) FILTER (WHERE created_at >= $3)
		FROM customer_events WHERE kind = 'login'`, []any{today, weekStart, monthStart},
		&o.Logins.Sessions, &o.Logins.Today, &o.Logins.Week, &o.Logins.Month); err != nil {
		return nil, err
	}
	if o.Logins.ByProvider, err = s.named(ctx, `SELECT COALESCE(data->>'provider',''), '', count(*), count(DISTINCT customer_id)
		FROM customer_events WHERE kind='login' AND created_at >= $1 AND created_at < $2 GROUP BY 1 ORDER BY 3 DESC`, r.From, r.To); err != nil {
		return nil, err
	}
	o.Logins.ByProvider = mergeLabels(o.Logins.ByProvider, providerNames)
	if o.Languages, err = s.named(ctx, `SELECT language, language, count(*), 0::bigint FROM customers GROUP BY 1 ORDER BY 3 DESC`); err != nil {
		return nil, err
	}
	return o, nil
}

func label(m map[string]string, k string) string {
	if v, ok := m[k]; ok {
		return v
	}
	return k
}

// mergeLabels relabels keys and merges rows that end up with the same label (telegram_bot + telegram → Telegram).
func mergeLabels(in []Named, m map[string]string) []Named {
	idx := map[string]int{}
	out := []Named{}
	for _, n := range in {
		l := label(m, n.Key)
		if i, ok := idx[l]; ok {
			out[i].Count += n.Count
			out[i].Sum += n.Sum
			continue
		}
		n.Label = l
		idx[l] = len(out)
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

// ---------------------------------------------------------------------------------------------
// Unit economics: MRR and its components, churn, LTV, cohorts.

type paidRow struct {
	customer int64
	amount   int64
	at       time.Time
	months   float64 // coverage in months
	planID   int64
	plan     string
	referred bool
	created  time.Time
}

func (s *Service) paidRows(ctx context.Context) ([]paidRow, error) {
	dim := float64(s.Settings.Get().General.DaysInMonth)
	if dim <= 0 {
		dim = 30
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT p.customer_id, p.amount, p.paid_at, p.months, p.days, COALESCE(p.plan_id,0), COALESCE(pl.name,''),
			c.referrer_id IS NOT NULL, c.created_at
		FROM payments p JOIN customers c ON c.id=p.customer_id LEFT JOIN plans pl ON pl.id=p.plan_id
		WHERE p.status='succeeded' AND p.paid_at IS NOT NULL AND p.operation IN `+paidOps+` ORDER BY p.paid_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []paidRow
	for rows.Next() {
		var p paidRow
		var months, days int
		if err := rows.Scan(&p.customer, &p.amount, &p.at, &months, &days, &p.planID, &p.plan, &p.referred, &p.created); err != nil {
			return nil, err
		}
		switch {
		case months > 0:
			p.months = float64(months)
		case days > 0:
			p.months = float64(days) / dim
		default:
			p.months = 1
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type MonthEconomics struct {
	Month       string `json:"month"` // YYYY-MM
	MRR         int64  `json:"mrr"`
	New         int64  `json:"new"`
	Reactivated int64  `json:"reactivated"`
	Expansion   int64  `json:"expansion"`
	Contraction int64  `json:"contraction"` // ≤ 0
	Churn       int64  `json:"churn"`       // ≤ 0
	Active      int64  `json:"active"`
	Churned     int64  `json:"churned"`
	NewCount    int64  `json:"new_count"`
	ARPPU       int64  `json:"arppu"`
	Revenue     int64  `json:"revenue"` // cash actually collected that month
}

type Cohort struct {
	Month      string    `json:"month"`
	Size       int64     `json:"size"`
	Retention  []float64 `json:"retention"` // M0..M5, %, -1 = not reached yet
	RevPerUser int64     `json:"rev_per_user"`
}

type EconomicsStats struct {
	Months []MonthEconomics `json:"months"`
	Cards  struct {
		MRR, MRRPrev, ARR, Active, ARPPU, LTV int64
		Churn, Lifetime, NRR, QuickRatio      float64
	} `json:"cards"`
	Cohorts   []Cohort `json:"cohorts"`
	Retention struct {
		DaysToChurn       float64 `json:"days_to_churn"`
		ReactivationShare float64 `json:"reactivation_share"`
	} `json:"retention"`
	LTV struct {
		Paying, Users           int64
		DaysToFirst, DaysMedian float64
		BySource                []Named `json:"by_source"` // sum = avg LTV, count = customers
		ByPlan                  []Named `json:"by_plan"`
	} `json:"ltv"`
}

func monthKey(t time.Time) string { return t.Format("2006-01") }

func (s *Service) StatsEconomics(ctx context.Context, months int) (*EconomicsStats, error) {
	if months != 3 && months != 6 && months != 12 {
		months = 6
	}
	loc := s.Settings.Get().General.Location()
	pays, err := s.paidRows(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().In(loc)
	cur := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	// Months to compute: the window plus one before it for deltas, and all history for "paid before".
	start := cur.AddDate(0, -months, 0)
	if len(pays) > 0 {
		f := pays[0].at.In(loc)
		if first := time.Date(f.Year(), f.Month(), 1, 0, 0, 0, 0, loc); first.Before(start) {
			start = first
		}
	}
	var monthStarts []time.Time
	for m := start; !m.After(cur); m = m.AddDate(0, 1, 0) {
		monthStarts = append(monthStarts, m)
	}
	idx := map[string]int{}
	for i, m := range monthStarts {
		idx[monthKey(m)] = i
	}
	// contribution[customer][month] = monthly value (max over payments covering that month).
	contrib := map[int64][]int64{}
	cash := make([]int64, len(monthStarts))
	firstPaid := map[int64]time.Time{}
	totalRev := map[int64]int64{}
	firstPlan := map[int64]string{}
	referred := map[int64]bool{}
	created := map[int64]time.Time{}
	for _, p := range pays {
		at := p.at.In(loc)
		if _, ok := firstPaid[p.customer]; !ok {
			firstPaid[p.customer] = at
			firstPlan[p.customer] = p.plan
		}
		totalRev[p.customer] += p.amount
		referred[p.customer] = p.referred
		created[p.customer] = p.created
		if i, ok := idx[monthKey(at)]; ok {
			cash[i] += p.amount
		}
		monthly := int64(math.Round(float64(p.amount) / math.Max(p.months, 1.0/31)))
		if p.months < 1 {
			monthly = p.amount // short top-ups count fully in the month they were bought
		}
		end := at.Add(time.Duration(p.months * 30 * 24 * float64(time.Hour)))
		c := contrib[p.customer]
		if c == nil {
			c = make([]int64, len(monthStarts))
			contrib[p.customer] = c
		}
		// A customer counts in a month when their paid time covers the month's end (for the current
		// month: now). Counting any overlap would put every monthly payment into two months.
		for i, m := range monthStarts {
			point := m.AddDate(0, 1, 0).Add(-time.Second)
			if i == len(monthStarts)-1 {
				point = now
			}
			if !at.After(point) && end.After(point) && monthly > c[i] {
				c[i] = monthly
			}
		}
	}

	all := make([]MonthEconomics, len(monthStarts))
	for i, m := range monthStarts {
		e := MonthEconomics{Month: monthKey(m), Revenue: cash[i]}
		for cust, c := range contrib {
			v := c[i]
			var prev int64
			if i > 0 {
				prev = c[i-1]
			}
			e.MRR += v
			if v > 0 {
				e.Active++
			}
			switch {
			case v > 0 && prev == 0:
				fp := firstPaid[cust]
				if !fp.Before(m) {
					e.New += v
					e.NewCount++
				} else {
					e.Reactivated += v
				}
			case v > 0 && prev > 0 && v > prev:
				e.Expansion += v - prev
			case v > 0 && prev > 0 && v < prev:
				e.Contraction -= prev - v
			case v == 0 && prev > 0:
				e.Churn -= prev
				e.Churned++
			}
		}
		if e.Active > 0 {
			e.ARPPU = e.MRR / e.Active
		}
		all[i] = e
	}
	out := &EconomicsStats{}
	if len(all) > months+1 {
		out.Months = all[len(all)-months-1:]
	} else {
		out.Months = all
	}
	n := len(all)
	last := all[n-1]
	var prev MonthEconomics
	if n > 1 {
		prev = all[n-2]
	}
	c := &out.Cards
	c.MRR, c.MRRPrev, c.ARR, c.Active, c.ARPPU = last.MRR, prev.MRR, last.MRR*12, last.Active, last.ARPPU
	// Churn over the window: average monthly churn of fully finished months.
	var churnSum float64
	var churnN int
	for i := n - months; i < n-1; i++ {
		if i < 1 || all[i-1].Active == 0 {
			continue
		}
		churnSum += float64(all[i].Churned) / float64(all[i-1].Active)
		churnN++
	}
	if churnN > 0 {
		c.Churn = churnSum / float64(churnN) * 100
	}
	if c.Churn > 0 {
		c.Lifetime = 100 / c.Churn
		c.LTV = int64(float64(c.ARPPU) * c.Lifetime)
	}
	if prev.MRR > 0 {
		c.NRR = float64(prev.MRR+last.Expansion+last.Contraction+last.Churn) / float64(prev.MRR) * 100
	}
	if lost := -(last.Churn + last.Contraction); lost > 0 {
		c.QuickRatio = float64(last.New+last.Reactivated+last.Expansion) / float64(lost)
	}

	// Cohorts by month of first payment, retention M0..M5.
	firstIdx := map[int64]int{}
	for cust, fp := range firstPaid {
		firstIdx[cust] = idx[monthKey(fp)]
	}
	for ci := max(0, n-months); ci < n; ci++ {
		co := Cohort{Month: monthKey(monthStarts[ci]), Retention: make([]float64, 6)}
		var rev int64
		var members []int64
		for cust, fi := range firstIdx {
			if fi == ci {
				members = append(members, cust)
				rev += totalRev[cust]
			}
		}
		co.Size = int64(len(members))
		for k := 0; k < 6; k++ {
			if ci+k >= n {
				co.Retention[k] = -1
				continue
			}
			if co.Size == 0 {
				continue
			}
			var alive int
			for _, cust := range members {
				if contrib[cust][ci+k] > 0 {
					alive++
				}
			}
			co.Retention[k] = math.Round(float64(alive)/float64(co.Size)*1000) / 10
		}
		if co.Size > 0 {
			co.RevPerUser = rev / co.Size
		}
		out.Cohorts = append(out.Cohorts, co)
	}

	// Retention: average paid months before churn, reactivation share.
	var lifeSum, lifeN, churnedCust, reactivated float64
	for _, c := range contrib {
		run, everChurned, back := 0, false, false
		for i, v := range c {
			if v > 0 {
				run++
				if everChurned {
					back = true
				}
			} else if i > 0 && c[i-1] > 0 {
				lifeSum += float64(run)
				lifeN++
				run = 0
				everChurned = true
			}
		}
		if everChurned {
			churnedCust++
			if back {
				reactivated++
			}
		}
	}
	if lifeN > 0 {
		out.Retention.DaysToChurn = math.Round(lifeSum / lifeN * 30)
	}
	if churnedCust > 0 {
		out.Retention.ReactivationShare = math.Round(reactivated/churnedCust*1000) / 10
	}

	// LTV details.
	if err := s.scan(ctx, `SELECT count(*) FROM customers`, nil, &out.LTV.Users); err != nil {
		return nil, err
	}
	out.LTV.Paying = int64(len(firstPaid))
	var gaps []float64
	src := map[string]*Named{"direct": {Key: "direct", Label: "Прямые"}, "referral": {Key: "referral", Label: "По приглашению"}}
	plans := map[string]*Named{}
	for cust, fp := range firstPaid {
		gaps = append(gaps, fp.Sub(created[cust]).Hours()/24)
		k := "direct"
		if referred[cust] {
			k = "referral"
		}
		src[k].Count++
		src[k].Sum += totalRev[cust]
		pn := firstPlan[cust]
		if pn == "" {
			pn = "Удалённый тариф"
		}
		if plans[pn] == nil {
			plans[pn] = &Named{Key: pn, Label: pn}
		}
		plans[pn].Count++
		plans[pn].Sum += totalRev[cust]
	}
	if len(gaps) > 0 {
		sort.Float64s(gaps)
		var sum float64
		for _, g := range gaps {
			sum += math.Max(g, 0)
		}
		out.LTV.DaysToFirst = math.Round(sum/float64(len(gaps))*10) / 10
		out.LTV.DaysMedian = math.Round(math.Max(gaps[len(gaps)/2], 0)*10) / 10
	}
	for _, k := range []string{"direct", "referral"} {
		v := *src[k]
		if v.Count > 0 {
			v.Sum /= v.Count
		}
		out.LTV.BySource = append(out.LTV.BySource, v)
	}
	for _, v := range plans {
		x := *v
		x.Sum /= x.Count
		out.LTV.ByPlan = append(out.LTV.ByPlan, x)
	}
	sort.Slice(out.LTV.ByPlan, func(i, j int) bool { return out.LTV.ByPlan[i].Count > out.LTV.ByPlan[j].Count })
	return out, nil
}

// ---------------------------------------------------------------------------------------------
// Payments

type PaymentStats struct {
	Range        StatsRange `json:"range"`
	Attempts     int64      `json:"attempts"`
	Succeeded    int64      `json:"succeeded"`
	Failed       int64      `json:"failed"`
	Pending      int64      `json:"pending"`
	Refunded     int64      `json:"refunded"`
	RetrySuccess float64    `json:"retry_success"` // % of customers with a failed payment who paid within 24h
	AutopayShare float64    `json:"autopay_share"` // % of successful payments made by autopay
	Conversion   []Point    `json:"conversion"`    // v: [succeeded, failed]
	Methods      []Named    `json:"methods"`       // count = attempts, sum = succeeded sum
	MethodsOK    []Named    `json:"methods_ok"`    // count = succeeded
	Operations   []Named    `json:"operations"`
}

func (s *Service) StatsPayments(ctx context.Context, period, group string) (*PaymentStats, error) {
	r := s.statsRange(period, group)
	o := &PaymentStats{Range: r}
	var autopay int64
	if err := s.scan(ctx, `SELECT count(*), count(*) FILTER (WHERE status='succeeded'), count(*) FILTER (WHERE status='canceled'),
			count(*) FILTER (WHERE status='pending'), count(*) FILTER (WHERE status='refunded'),
			count(*) FILTER (WHERE status='succeeded' AND autopay)
		FROM payments WHERE created_at >= $1 AND created_at < $2`, []any{r.From, r.To},
		&o.Attempts, &o.Succeeded, &o.Failed, &o.Pending, &o.Refunded, &autopay); err != nil {
		return nil, err
	}
	if o.Succeeded > 0 {
		o.AutopayShare = pct(autopay, o.Succeeded)
	}
	var failedCust, recovered int64
	if err := s.scan(ctx, `SELECT count(DISTINCT f.customer_id),
			count(DISTINCT f.customer_id) FILTER (WHERE EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=f.customer_id AND p.status='succeeded'
				AND p.created_at > f.created_at AND p.created_at <= f.created_at + interval '24 hours'))
		FROM payments f WHERE f.status='canceled' AND f.created_at >= $1 AND f.created_at < $2`, []any{r.From, r.To}, &failedCust, &recovered); err != nil {
		return nil, err
	}
	o.RetrySuccess = pct(recovered, failedCust)
	var err error
	if o.Conversion, err = s.series(ctx, r, 2, `SELECT date_trunc($3, created_at AT TIME ZONE $4) AS t,
			count(*) FILTER (WHERE status IN ('succeeded','refunded')) AS c0, count(*) FILTER (WHERE status='canceled') AS c1
		FROM payments WHERE created_at >= $1 AND created_at < $2 GROUP BY 1`); err != nil {
		return nil, err
	}
	if o.Methods, err = s.named(ctx, `SELECT CASE WHEN autopay THEN 'saved' ELSE method END, '', count(*),
			COALESCE(sum(amount) FILTER (WHERE status='succeeded'),0)
		FROM payments WHERE created_at >= $1 AND created_at < $2 GROUP BY 1 ORDER BY 3 DESC`, r.From, r.To); err != nil {
		return nil, err
	}
	if o.MethodsOK, err = s.named(ctx, `SELECT CASE WHEN autopay THEN 'saved' ELSE method END, '', count(*), 0::bigint
		FROM payments WHERE status='succeeded' AND created_at >= $1 AND created_at < $2 GROUP BY 1`, r.From, r.To); err != nil {
		return nil, err
	}
	for _, l := range [][]Named{o.Methods, o.MethodsOK} {
		for i := range l {
			l[i].Label = label(methodNames, l[i].Key)
		}
	}
	if o.Operations, err = s.named(ctx, `SELECT operation, '', count(*), sum(amount) FROM payments
		WHERE status='succeeded' AND paid_at >= $1 AND paid_at < $2 GROUP BY 1 ORDER BY 4 DESC`, r.From, r.To); err != nil {
		return nil, err
	}
	for i := range o.Operations {
		o.Operations[i].Label = label(operationNames, o.Operations[i].Key)
	}
	return o, nil
}

func pct(a, b int64) float64 {
	if b == 0 {
		return 0
	}
	return math.Round(float64(a)/float64(b)*1000) / 10
}

// ---------------------------------------------------------------------------------------------
// Referrals

type ReferralStats struct {
	Range     StatsRange `json:"range"`
	Invited   int64      `json:"invited"`
	Paid      int64      `json:"paid"`
	BonusDays int64      `json:"bonus_days"`
	Revenue   int64      `json:"revenue"`    // paid by invited customers in the period
	BonusCost int64      `json:"bonus_cost"` // bonus days × average price of a paid day
	DayPrice  int64      `json:"day_price"`
	ROI       float64    `json:"roi"`
	Series    []Point    `json:"series"`  // v: [invited, paid payments by invited]
	Bonuses   []Point    `json:"bonuses"` // v: [first, recurring, invitee, manual] days
	ByKind    []Named    `json:"by_kind"`
	Funnel    struct {
		Invited, Started, Paid, Repeat int64
	} `json:"funnel"`
	LTVInvited int64 `json:"ltv_invited"`
	LTVRegular int64 `json:"ltv_regular"`
	Top        []struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Invited   int64  `json:"invited"`
		Paid      int64  `json:"paid"`
		BonusDays int64  `json:"bonus_days"`
		Revenue   int64  `json:"revenue"`
	} `json:"top"`
	Recent []struct {
		At        time.Time `json:"at"`
		Kind      string    `json:"kind"`
		Days      int64     `json:"days"`
		Recipient string    `json:"recipient"`
		RecipID   int64     `json:"recipient_id"`
		Referral  string    `json:"referral"`
	} `json:"recent"`
}

var bonusKinds = map[string]string{"first": "За первую оплату", "recurring": "За повторную оплату", "invitee": "Приглашённому", "manual": "Вручную"}

const custName = `COALESCE(NULLIF('@' || c.username, '@'), NULLIF(trim(COALESCE(c.first_name,'') || ' ' || COALESCE(c.last_name,'')), ''), c.email::text, '#' || c.id)`

func (s *Service) StatsReferrals(ctx context.Context, period, group string) (*ReferralStats, error) {
	r := s.statsRange(period, group)
	o := &ReferralStats{Range: r}
	args := []any{r.From, r.To}
	if err := s.scan(ctx, `SELECT
			(SELECT count(*) FROM customers WHERE referrer_id IS NOT NULL AND created_at >= $1 AND created_at < $2),
			(SELECT count(DISTINCT p.customer_id) FROM payments p JOIN customers c ON c.id=p.customer_id
				WHERE c.referrer_id IS NOT NULL AND p.status='succeeded' AND p.paid_at >= $1 AND p.paid_at < $2),
			(SELECT COALESCE(sum(days),0) FROM referral_bonuses WHERE created_at >= $1 AND created_at < $2),
			(SELECT COALESCE(sum(p.amount),0) FROM payments p JOIN customers c ON c.id=p.customer_id
				WHERE c.referrer_id IS NOT NULL AND p.status='succeeded' AND p.paid_at >= $1 AND p.paid_at < $2)`, args,
		&o.Invited, &o.Paid, &o.BonusDays, &o.Revenue); err != nil {
		return nil, err
	}
	// Price of one paid day: what customers paid per day of subscription over the last 90 days.
	if err := s.scan(ctx, `SELECT COALESCE(sum(amount) / NULLIF(sum(CASE WHEN days > 0 THEN days ELSE months * 30 END), 0), 0)::bigint
		FROM payments WHERE status='succeeded' AND operation IN `+paidOps+` AND paid_at >= now() - interval '90 days'`, nil, &o.DayPrice); err != nil {
		return nil, err
	}
	o.BonusCost = o.BonusDays * o.DayPrice
	if o.BonusCost > 0 {
		o.ROI = math.Round(float64(o.Revenue-o.BonusCost)/float64(o.BonusCost)*1000) / 10
	}
	var err error
	if o.Series, err = s.series(ctx, r, 2, `SELECT t, sum(a) AS c0, sum(b) AS c1 FROM (
			SELECT date_trunc($3, created_at AT TIME ZONE $4) AS t, 1 AS a, 0 AS b FROM customers
				WHERE referrer_id IS NOT NULL AND created_at >= $1 AND created_at < $2
			UNION ALL
			SELECT date_trunc($3, p.paid_at AT TIME ZONE $4), 0, 1 FROM payments p JOIN customers c ON c.id=p.customer_id
				WHERE c.referrer_id IS NOT NULL AND p.status='succeeded' AND p.paid_at >= $1 AND p.paid_at < $2) u GROUP BY t`); err != nil {
		return nil, err
	}
	if o.Bonuses, err = s.series(ctx, r, 4, `SELECT date_trunc($3, created_at AT TIME ZONE $4) AS t,
			sum(days) FILTER (WHERE kind='first') AS c0, sum(days) FILTER (WHERE kind='recurring') AS c1,
			sum(days) FILTER (WHERE kind='invitee') AS c2, sum(days) FILTER (WHERE kind='manual') AS c3
		FROM referral_bonuses WHERE created_at >= $1 AND created_at < $2 GROUP BY 1`); err != nil {
		return nil, err
	}
	if o.ByKind, err = s.named(ctx, `SELECT kind, '', count(*), sum(days) FROM referral_bonuses
		WHERE created_at >= $1 AND created_at < $2 GROUP BY 1 ORDER BY 4 DESC`, r.From, r.To); err != nil {
		return nil, err
	}
	for i := range o.ByKind {
		o.ByKind[i].Label = label(bonusKinds, o.ByKind[i].Key)
	}
	if err := s.scan(ctx, `SELECT count(*),
			count(*) FILTER (WHERE EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=c.id)),
			count(*) FILTER (WHERE (SELECT count(*) FROM payments p WHERE p.customer_id=c.id AND p.status='succeeded') >= 1),
			count(*) FILTER (WHERE (SELECT count(*) FROM payments p WHERE p.customer_id=c.id AND p.status='succeeded') >= 2)
		FROM customers c WHERE c.referrer_id IS NOT NULL AND c.created_at >= $1 AND c.created_at < $2`, args,
		&o.Funnel.Invited, &o.Funnel.Started, &o.Funnel.Paid, &o.Funnel.Repeat); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT
			COALESCE(avg(t) FILTER (WHERE referred), 0)::bigint, COALESCE(avg(t) FILTER (WHERE NOT referred), 0)::bigint FROM (
			SELECT c.referrer_id IS NOT NULL AS referred, sum(p.amount) AS t FROM payments p JOIN customers c ON c.id=p.customer_id
			WHERE p.status='succeeded' GROUP BY c.id, c.referrer_id) x`, nil, &o.LTVInvited, &o.LTVRegular); err != nil {
		return nil, err
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT c.id, `+custName+`,
			(SELECT count(*) FROM customers r WHERE r.referrer_id=c.id),
			(SELECT count(*) FROM customers r WHERE r.referrer_id=c.id AND EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=r.id AND p.status='succeeded')),
			(SELECT COALESCE(sum(days),0) FROM referral_bonuses b WHERE b.recipient_id=c.id AND b.kind <> 'invitee'),
			(SELECT COALESCE(sum(p.amount),0) FROM payments p JOIN customers r ON r.id=p.customer_id WHERE r.referrer_id=c.id AND p.status='succeeded')
		FROM customers c WHERE EXISTS (SELECT 1 FROM customers r WHERE r.referrer_id=c.id)
		ORDER BY 4 DESC, 3 DESC LIMIT 10`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Invited   int64  `json:"invited"`
			Paid      int64  `json:"paid"`
			BonusDays int64  `json:"bonus_days"`
			Revenue   int64  `json:"revenue"`
		}
		if err := rows.Scan(&t.ID, &t.Name, &t.Invited, &t.Paid, &t.BonusDays, &t.Revenue); err != nil {
			rows.Close()
			return nil, err
		}
		o.Top = append(o.Top, t)
	}
	rows.Close()
	rows, err = s.Store.Pool.Query(ctx, `SELECT b.created_at, b.kind, b.days, `+custName+`, c.id,
			COALESCE((SELECT `+strings.ReplaceAll(custName, "c.", "r.")+` FROM customers r WHERE r.id=b.referral_id), '')
		FROM referral_bonuses b JOIN customers c ON c.id=b.recipient_id ORDER BY b.created_at DESC LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t struct {
			At        time.Time `json:"at"`
			Kind      string    `json:"kind"`
			Days      int64     `json:"days"`
			Recipient string    `json:"recipient"`
			RecipID   int64     `json:"recipient_id"`
			Referral  string    `json:"referral"`
		}
		if err := rows.Scan(&t.At, &t.Kind, &t.Days, &t.Recipient, &t.RecipID, &t.Referral); err != nil {
			return nil, err
		}
		t.Kind = label(bonusKinds, t.Kind)
		o.Recent = append(o.Recent, t)
	}
	return o, rows.Err()
}

// ---------------------------------------------------------------------------------------------
// Support

type SupportStats struct {
	Range         StatsRange `json:"range"`
	Total         int64      `json:"total"`
	ThisMonth     Delta      `json:"this_month"`
	PerUser       float64    `json:"per_user"`
	PerDay        float64    `json:"per_day"`
	FirstAvg      float64    `json:"first_avg"` // minutes
	FirstMedian   float64    `json:"first_median"`
	ResolveAvg    float64    `json:"resolve_avg"`
	ResolveMedian float64    `json:"resolve_median"`
	Rating        float64    `json:"rating"`
	Rated         int64      `json:"rated"`
	AIShare       float64    `json:"ai_share"` // % of tickets the assistant answered at least once
	Statuses      []Named    `json:"statuses"`
	Series        []Point    `json:"series"` // v: [created, avg first response minutes]
}

var ticketStatuses = map[string]string{"open": "Открыт", "waiting": "Ждёт клиента", "resolved": "Решён", "closed": "Закрыт"}

func (s *Service) StatsSupport(ctx context.Context, period, group string) (*SupportStats, error) {
	r := s.statsRange(period, group)
	o := &SupportStats{Range: r}
	now := time.Now().In(r.loc)
	ms := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, r.loc)
	args := []any{r.From, r.To}
	var customers, ai int64
	if err := s.scan(ctx, `SELECT count(*), count(DISTINCT customer_id),
			COALESCE(extract(epoch FROM avg(first_response_at - created_at)) / 60, 0),
			COALESCE(extract(epoch FROM percentile_cont(0.5) WITHIN GROUP (ORDER BY first_response_at - created_at)) / 60, 0),
			COALESCE(extract(epoch FROM avg(COALESCE(resolved_at, closed_at) - created_at)) / 60, 0),
			COALESCE(extract(epoch FROM percentile_cont(0.5) WITHIN GROUP (ORDER BY COALESCE(resolved_at, closed_at) - created_at)) / 60, 0),
			COALESCE(avg(rating), 0), count(rating),
			count(*) FILTER (WHERE EXISTS (SELECT 1 FROM ticket_messages m WHERE m.ticket_id=t.id AND m.author='ai'))
		FROM tickets t WHERE created_at >= $1 AND created_at < $2`, args,
		&o.Total, &customers, &o.FirstAvg, &o.FirstMedian, &o.ResolveAvg, &o.ResolveMedian, &o.Rating, &o.Rated, &ai); err != nil {
		return nil, err
	}
	if customers > 0 {
		o.PerUser = math.Round(float64(o.Total)/float64(customers)*100) / 100
	}
	if days := r.To.Sub(r.From).Hours() / 24; days > 0 {
		o.PerDay = math.Round(float64(o.Total)/days*100) / 100
	}
	o.AIShare = pct(ai, o.Total)
	for _, p := range []*float64{&o.FirstAvg, &o.FirstMedian, &o.ResolveAvg, &o.ResolveMedian} {
		*p = math.Round(*p*10) / 10
	}
	o.Rating = math.Round(o.Rating*100) / 100
	if err := s.scan(ctx, `SELECT count(*) FILTER (WHERE created_at >= $1), count(*) FILTER (WHERE created_at >= $2 AND created_at < $3)
		FROM tickets WHERE created_at >= $2`, []any{ms, ms.AddDate(0, -1, 0), ms.AddDate(0, -1, now.Day())}, &o.ThisMonth.Cur, &o.ThisMonth.Prev); err != nil {
		return nil, err
	}
	var err error
	if o.Statuses, err = s.named(ctx, `SELECT status, '', count(*), 0::bigint FROM tickets WHERE created_at >= $1 AND created_at < $2 GROUP BY 1`, r.From, r.To); err != nil {
		return nil, err
	}
	for i := range o.Statuses {
		o.Statuses[i].Label = label(ticketStatuses, o.Statuses[i].Key)
	}
	if o.Series, err = s.series(ctx, r, 2, `SELECT date_trunc($3, created_at AT TIME ZONE $4) AS t, count(*) AS c0,
			COALESCE(extract(epoch FROM avg(first_response_at - created_at)) / 60, 0)::bigint AS c1
		FROM tickets WHERE created_at >= $1 AND created_at < $2 GROUP BY 1`); err != nil {
		return nil, err
	}
	return o, nil
}

// ---------------------------------------------------------------------------------------------
// Analytics: sales funnel, events, sources, leads.

type FunnelStep struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Cur   int64  `json:"cur"`
	Prev  int64  `json:"prev"`
}

type AnalyticsStats struct {
	Range   StatsRange   `json:"range"`
	Funnel  []FunnelStep `json:"funnel"`
	Steps   []Point      `json:"steps"` // v: [registered, trial, invoice, paid] by registration bucket
	Events  []FunnelStep `json:"events"`
	Sources []struct {
		Key        string  `json:"key"`
		Label      string  `json:"label"`
		Users      int64   `json:"users"`
		Paid       int64   `json:"paid"`
		Conversion float64 `json:"conversion"`
	} `json:"sources"`
	Systems []struct {
		Key        string  `json:"key"`
		Label      string  `json:"label"`
		Attempts   int64   `json:"attempts"`
		Paid       int64   `json:"paid"`
		Share      float64 `json:"share"`
		Conversion float64 `json:"conversion"`
	} `json:"systems"`
	Leads []struct {
		Key   string `json:"key"`
		Label string `json:"label"`
		Count int64  `json:"count"`
		// Paid within 30 days after the temperature was assigned (only for non-stable leads).
		Converted int64 `json:"converted"`
	} `json:"leads"`
}

var leadLabels = map[string]string{"hot": "Горячие", "warm": "Тёплые", "cool": "Прохладные", "cold": "Холодные", "stable": "Стабильные"}

func (s *Service) StatsAnalytics(ctx context.Context, period, group string) (*AnalyticsStats, error) {
	r := s.statsRange(period, group)
	o := &AnalyticsStats{Range: r}
	funnel := func(from, to time.Time) ([4]int64, error) {
		var v [4]int64
		err := s.scan(ctx, `SELECT count(*),
				count(*) FILTER (WHERE c.trial_used OR EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND s.kind='trial')),
				count(*) FILTER (WHERE EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=c.id)),
				count(*) FILTER (WHERE EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=c.id AND p.status='succeeded'))
			FROM customers c WHERE c.created_at >= $1 AND c.created_at < $2`, []any{from, to}, &v[0], &v[1], &v[2], &v[3])
		return v, err
	}
	cur, err := funnel(r.From, r.To)
	if err != nil {
		return nil, err
	}
	var prev [4]int64
	if !r.unbounded {
		if prev, err = funnel(r.PrevFrom, r.From); err != nil {
			return nil, err
		}
	}
	for i, k := range [][2]string{{"registered", "Регистрация"}, {"trial", "Пробный период"}, {"invoice", "Создали счёт"}, {"paid", "Оплатили"}} {
		o.Funnel = append(o.Funnel, FunnelStep{k[0], k[1], cur[i], prev[i]})
	}
	if o.Steps, err = s.series(ctx, r, 4, `SELECT date_trunc($3, c.created_at AT TIME ZONE $4) AS t, count(*) AS c0,
			count(*) FILTER (WHERE c.trial_used) AS c1,
			count(*) FILTER (WHERE EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=c.id)) AS c2,
			count(*) FILTER (WHERE EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=c.id AND p.status='succeeded')) AS c3
		FROM customers c WHERE c.created_at >= $1 AND c.created_at < $2 GROUP BY 1`); err != nil {
		return nil, err
	}

	events := func(from, to time.Time) ([8]int64, error) {
		var v [8]int64
		err := s.scan(ctx, `SELECT
			(SELECT count(*) FROM customers WHERE created_at >= $1 AND created_at < $2),
			(SELECT count(*) FROM payments WHERE created_at >= $1 AND created_at < $2),
			(SELECT count(*) FROM payments WHERE status='succeeded' AND paid_at >= $1 AND paid_at < $2),
			(SELECT count(*) FROM payments WHERE status='canceled' AND created_at >= $1 AND created_at < $2),
			(SELECT count(*) FROM subscriptions WHERE kind='trial' AND created_at >= $1 AND created_at < $2),
			(SELECT count(*) FROM subscriptions WHERE kind='free' AND created_at >= $1 AND created_at < $2),
			(SELECT count(*) FROM subscriptions WHERE expires_at >= $1 AND expires_at < $2 AND expires_at <= now()),
			(SELECT count(*) FROM tickets WHERE created_at >= $1 AND created_at < $2)`, []any{from, to},
			&v[0], &v[1], &v[2], &v[3], &v[4], &v[5], &v[6], &v[7])
		return v, err
	}
	ec, err := events(r.From, r.To)
	if err != nil {
		return nil, err
	}
	var ep [8]int64
	if !r.unbounded {
		if ep, err = events(r.PrevFrom, r.From); err != nil {
			return nil, err
		}
	}
	for i, k := range [][2]string{{"users", "Новые пользователи"}, {"started", "Начатые оплаты"}, {"succeeded", "Успешные оплаты"},
		{"canceled", "Отменённые оплаты"}, {"trial", "Активации пробного"}, {"free", "Бесплатный тариф"}, {"expired", "Истекшие подписки"},
		{"tickets", "Обращения"}} {
		o.Events = append(o.Events, FunnelStep{k[0], k[1], ec[i], ep[i]})
	}

	rows, err := s.Store.Pool.Query(ctx, `SELECT CASE WHEN referrer_id IS NOT NULL THEN 'referral' ELSE 'direct' END, count(*),
			count(*) FILTER (WHERE EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=c.id AND p.status='succeeded'))
		FROM customers c WHERE c.created_at >= $1 AND c.created_at < $2 GROUP BY 1 ORDER BY 1`, r.From, r.To)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x struct {
			Key        string  `json:"key"`
			Label      string  `json:"label"`
			Users      int64   `json:"users"`
			Paid       int64   `json:"paid"`
			Conversion float64 `json:"conversion"`
		}
		if err := rows.Scan(&x.Key, &x.Users, &x.Paid); err != nil {
			rows.Close()
			return nil, err
		}
		x.Label = map[string]string{"direct": "Прямой", "referral": "Рефералы"}[x.Key]
		x.Conversion = pct(x.Paid, x.Users)
		o.Sources = append(o.Sources, x)
	}
	rows.Close()

	methods, err := s.named(ctx, `SELECT CASE WHEN autopay THEN 'saved' ELSE method END, '', count(*), count(*) FILTER (WHERE status='succeeded')
		FROM payments WHERE created_at >= $1 AND created_at < $2 GROUP BY 1 ORDER BY 4 DESC`, r.From, r.To)
	if err != nil {
		return nil, err
	}
	var paidAll int64
	for _, m := range methods {
		paidAll += m.Sum
	}
	for _, m := range methods {
		o.Systems = append(o.Systems, struct {
			Key        string  `json:"key"`
			Label      string  `json:"label"`
			Attempts   int64   `json:"attempts"`
			Paid       int64   `json:"paid"`
			Share      float64 `json:"share"`
			Conversion float64 `json:"conversion"`
		}{m.Key, label(methodNames, m.Key), m.Count, m.Sum, pct(m.Sum, paidAll), pct(m.Sum, m.Count)})
	}

	rows, err = s.Store.Pool.Query(ctx, `SELECT lead_temperature, count(*),
			count(*) FILTER (WHERE lead_temperature <> 'stable' AND EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=c.id
				AND p.status='succeeded' AND p.paid_at >= c.lead_temperature_at AND p.paid_at < c.lead_temperature_at + interval '30 days'))
		FROM customers c WHERE lead_temperature <> '' GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	got := map[string][2]int64{}
	for rows.Next() {
		var k string
		var a, b int64
		if err := rows.Scan(&k, &a, &b); err != nil {
			return nil, err
		}
		got[k] = [2]int64{a, b}
	}
	for _, k := range []string{"hot", "warm", "cool", "cold", "stable"} {
		o.Leads = append(o.Leads, struct {
			Key       string `json:"key"`
			Label     string `json:"label"`
			Count     int64  `json:"count"`
			Converted int64  `json:"converted"`
		}{k, leadLabels[k], got[k][0], got[k][1]})
	}
	return o, rows.Err()
}

// Lead is a row of the leads list with its signal tags.
type Lead struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Temperature string     `json:"temperature"`
	Score       int        `json:"score"`
	LastSeen    *time.Time `json:"last_seen"`
	CreatedAt   time.Time  `json:"created_at"`
	Plan        string     `json:"plan"`
	Signals     []string   `json:"signals"`
}

// StatsLeads lists customers with a lead temperature, scored 0–100 for sorting.
// The score is a transparent sum of the same signals that are shown as tags.
func (s *Service) StatsLeads(ctx context.Context, temp, q string, limit, offset int) (map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.Store.Pool.Query(ctx, `
		WITH x AS (
		SELECT c.id, `+custName+` AS name, c.lead_temperature, c.last_seen_at, c.created_at,
			COALESCE((SELECT pl.name FROM subscriptions s JOIN plans pl ON pl.id=s.plan_id WHERE s.customer_id=c.id
				ORDER BY s.expires_at DESC LIMIT 1), '') AS plan,
			EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=c.id AND p.status IN ('pending','canceled') AND p.created_at > now() - interval '7 days')
				AND NOT EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=c.id AND p.status='succeeded' AND p.created_at > now() - interval '7 days') AS cart,
			EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND s.status='active' AND s.expires_at > now() AND s.expires_at <= now() + interval '7 days') AS expiring,
			EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND s.kind='trial' AND s.status='active' AND s.expires_at > now()) AS trial,
			EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND s.kind='free' AND s.status='active' AND s.expires_at > now()) AS free,
			EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND s.expires_at <= now() AND s.expires_at > now() - interval '14 days')
				AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND s.status='active' AND s.expires_at > now()) AS expired,
			(SELECT count(*) FROM payments p WHERE p.customer_id=c.id AND p.status='succeeded') AS paid,
			EXISTS (SELECT 1 FROM customer_events e WHERE e.customer_id=c.id AND e.kind='autopay_failed' AND e.created_at > now() - interval '14 days') AS autopay_err
		FROM customers c
		WHERE c.lead_temperature <> '' AND ($1 = '' OR c.lead_temperature = $1)
			AND ($2 = '' OR c.username ILIKE '%' || $2 || '%' OR c.email::text ILIKE '%' || $2 || '%' OR c.telegram_id::text = $2 OR c.first_name ILIKE '%' || $2 || '%'))
		SELECT *, count(*) OVER () FROM x`, temp, strings.TrimPrefix(strings.TrimSpace(q), "@"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Lead
	var total int64
	for rows.Next() {
		var l Lead
		var cart, expiring, trial, free, expired, autopayErr bool
		var paid int64
		if err := rows.Scan(&l.ID, &l.Name, &l.Temperature, &l.LastSeen, &l.CreatedAt, &l.Plan, &cart, &expiring, &trial, &free,
			&expired, &paid, &autopayErr, &total); err != nil {
			return nil, err
		}
		add := func(on bool, tag string, pts int) {
			if on {
				l.Signals = append(l.Signals, tag)
				l.Score += pts
			}
		}
		add(cart, "Брошенная корзина", 30)
		add(expiring, "Скоро истекает", 20)
		add(trial, "Активный триал", 15)
		add(free, "Бесплатный тариф", 5)
		add(expired, "Недавно истёк", 15)
		add(paid >= 2, "Повторный клиент", 10)
		add(autopayErr, "Ошибка автооплаты", 15)
		if l.LastSeen != nil {
			ago := time.Since(*l.LastSeen)
			add(ago < 24*time.Hour, "Активен сегодня", 20)
			add(ago >= 24*time.Hour && ago < 7*24*time.Hour, "Активен на неделе", 10)
			add(ago >= 90*24*time.Hour, "Неактивен 90+ дней", 0)
			add(ago >= 30*24*time.Hour && ago < 90*24*time.Hour, "Неактивен 30+ дней", 0)
		}
		if l.Score > 100 {
			l.Score = 100
		}
		list = append(list, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Score > list[j].Score })
	if offset > len(list) {
		offset = len(list)
	}
	end := min(offset+limit, len(list))
	return map[string]any{"items": nonNil(list[offset:end]), "total": total}, nil
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

// RecomputeLeads recalculates lead temperatures on demand ("Пересчитать").
func (s *Service) RecomputeLeads(ctx context.Context) (int, error) {
	list, err := store.ListAutomations(ctx, s.Store.Pool)
	if err != nil {
		return 0, err
	}
	return s.applyLeadTemperatures(ctx, list)
}
