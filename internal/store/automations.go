package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

type Automation struct {
	ID             int64           `db:"id" json:"id"`
	Name           string          `db:"name" json:"name"`
	TriggerType    string          `db:"trigger_type" json:"trigger_type"`
	TriggerFactor  string          `db:"trigger_factor" json:"trigger_factor"`
	IsActive       bool            `db:"is_active" json:"is_active"`
	CooldownHours  int             `db:"cooldown_hours" json:"cooldown_hours"`
	SkipIfActiveIn []string        `db:"skip_if_active_in" json:"skip_if_active_in"`
	ScheduleMode   string          `db:"schedule_mode" json:"schedule_mode"`
	LoopCount      int             `db:"loop_count" json:"loop_count"`
	ShuffleSteps   bool            `db:"shuffle_steps" json:"shuffle_steps"`
	Steps          json.RawMessage `db:"steps" json:"steps"`
	Sort           int             `db:"sort" json:"sort"`
	CreatedAt      time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at" json:"updated_at"`
}

type PlanItem struct {
	Step   int  `json:"s"`
	Repeat bool `json:"r,omitempty"`
}

type AutomationRun struct {
	ID           int64          `db:"id" json:"id"`
	AutomationID int64          `db:"automation_id" json:"automation_id"`
	CustomerID   int64          `db:"customer_id" json:"customer_id"`
	Status       string         `db:"status" json:"status"`
	StopReason   string         `db:"stop_reason" json:"stop_reason"`
	Position     int            `db:"position" json:"position"`
	Plan         []PlanItem     `db:"plan" json:"plan"`
	Context      map[string]any `db:"context" json:"context"`
	NextAt       *time.Time     `db:"next_at" json:"next_at"`
	Sent         int            `db:"sent" json:"sent"`
	Converted    bool           `db:"converted" json:"converted"`
	StartedAt    time.Time      `db:"started_at" json:"started_at"`
	FinishedAt   *time.Time     `db:"finished_at" json:"finished_at"`
	// joined
	AutomationName string `db:"automation_name" json:"automation_name"`
}

func ListAutomations(ctx context.Context, db DB) ([]Automation, error) {
	return many[Automation](db.Query(ctx, `SELECT * FROM automations ORDER BY sort, id`))
}

func GetAutomation(ctx context.Context, db DB, id int64) (*Automation, error) {
	return one[Automation](db.Query(ctx, `SELECT * FROM automations WHERE id=$1`, id))
}

func SaveAutomation(ctx context.Context, db DB, a *Automation) error {
	if a.SkipIfActiveIn == nil {
		a.SkipIfActiveIn = []string{}
	}
	if a.ID == 0 {
		return db.QueryRow(ctx, `INSERT INTO automations (name, trigger_type, trigger_factor, is_active, cooldown_hours, skip_if_active_in,
			schedule_mode, loop_count, shuffle_steps, steps, sort)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,COALESCE((SELECT max(sort)+1 FROM automations),0)) RETURNING id, created_at, updated_at`,
			a.Name, a.TriggerType, a.TriggerFactor, a.IsActive, a.CooldownHours, a.SkipIfActiveIn, a.ScheduleMode, a.LoopCount,
			a.ShuffleSteps, a.Steps).Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt)
	}
	tag, err := db.Exec(ctx, `UPDATE automations SET name=$2, trigger_type=$3, trigger_factor=$4, is_active=$5, cooldown_hours=$6,
		skip_if_active_in=$7, schedule_mode=$8, loop_count=$9, shuffle_steps=$10, steps=$11, updated_at=now() WHERE id=$1`,
		a.ID, a.Name, a.TriggerType, a.TriggerFactor, a.IsActive, a.CooldownHours, a.SkipIfActiveIn, a.ScheduleMode, a.LoopCount,
		a.ShuffleSteps, a.Steps)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func SetAutomationActive(ctx context.Context, db DB, id int64, on bool) error {
	_, err := db.Exec(ctx, `UPDATE automations SET is_active=$2, updated_at=now() WHERE id=$1`, id, on)
	return err
}

func DeleteAutomation(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `DELETE FROM automations WHERE id=$1`, id)
	return err
}

type AutomationStats struct {
	AutomationID int64 `db:"automation_id" json:"automation_id"`
	Active       int   `db:"active" json:"active"`
	Runs         int   `db:"runs" json:"runs"`
	Sent         int   `db:"sent" json:"sent"`
	Converted    int   `db:"converted" json:"converted"`
}

func AutomationStatsAll(ctx context.Context, db DB) ([]AutomationStats, error) {
	return many[AutomationStats](db.Query(ctx, `SELECT automation_id, count(*) FILTER (WHERE status='active')::int AS active,
		count(*)::int AS runs, COALESCE(sum(sent),0)::int AS sent, count(*) FILTER (WHERE converted)::int AS converted
		FROM automation_runs GROUP BY automation_id`))
}

// CanStartRun: no active run of this automation, none started within the cooldown, and none active in the listed automations.
func CanStartRun(ctx context.Context, db DB, a *Automation, customerID int64) (bool, error) {
	var blocked bool
	err := db.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM automation_runs WHERE automation_id=$1 AND customer_id=$2
			AND (status='active' OR started_at > now() - make_interval(hours => $3)))
		OR EXISTS (SELECT 1 FROM automation_runs r JOIN automations a ON a.id=r.automation_id
			WHERE r.customer_id=$2 AND r.status='active' AND a.name = ANY($4))`,
		a.ID, customerID, a.CooldownHours, a.SkipIfActiveIn).Scan(&blocked)
	return !blocked, err
}

// InsertRun starts a run; returns false if one is already active (unique index).
func InsertRun(ctx context.Context, db DB, r *AutomationRun) (bool, error) {
	err := db.QueryRow(ctx, `INSERT INTO automation_runs (automation_id, customer_id, plan, context, next_at)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING id, started_at`,
		r.AutomationID, r.CustomerID, r.Plan, r.Context, r.NextAt).Scan(&r.ID, &r.StartedAt)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// DueRuns locks due runs for processing.
func DueRuns(ctx context.Context, db DB, limit int) ([]AutomationRun, error) {
	return many[AutomationRun](db.Query(ctx, `SELECT r.*, '' AS automation_name FROM automation_runs r
		WHERE r.status='active' AND r.next_at <= now() ORDER BY r.next_at LIMIT $1 FOR UPDATE SKIP LOCKED`, limit))
}

func AdvanceRun(ctx context.Context, db DB, id int64, position int, next *time.Time, sent bool) error {
	inc := 0
	if sent {
		inc = 1
	}
	_, err := db.Exec(ctx, `UPDATE automation_runs SET position=$2, next_at=$3, sent=sent+$4 WHERE id=$1`, id, position, next, inc)
	return err
}

func FinishRun(ctx context.Context, db DB, id int64, status, reason string, converted bool) error {
	_, err := db.Exec(ctx, `UPDATE automation_runs SET status=$2, stop_reason=$3, converted=converted OR $4, next_at=NULL,
		finished_at=now() WHERE id=$1`, id, status, reason, converted)
	return err
}

// MarkConverted flags runs that led to a payment (active ones, or finished within the attribution window).
func MarkConverted(ctx context.Context, db DB, customerID int64, window time.Duration) error {
	_, err := db.Exec(ctx, `UPDATE automation_runs SET converted=true WHERE customer_id=$1 AND sent > 0 AND
		(status='active' OR finished_at > now() - make_interval(secs => $2))`, customerID, window.Seconds())
	return err
}

func StopCustomerRuns(ctx context.Context, db DB, customerID int64, runID int64) error {
	_, err := db.Exec(ctx, `UPDATE automation_runs SET status='stopped', stop_reason='admin', next_at=NULL, finished_at=now()
		WHERE customer_id=$1 AND status='active' AND ($2=0 OR id=$2)`, customerID, runID)
	return err
}

func StopAutomationRuns(ctx context.Context, db DB, automationID int64, reason string) error {
	_, err := db.Exec(ctx, `UPDATE automation_runs SET status='stopped', stop_reason=$2, next_at=NULL, finished_at=now()
		WHERE automation_id=$1 AND status='active'`, automationID, reason)
	return err
}

func CustomerRuns(ctx context.Context, db DB, customerID int64) ([]AutomationRun, error) {
	return many[AutomationRun](db.Query(ctx, `SELECT r.*, a.name AS automation_name FROM automation_runs r
		JOIN automations a ON a.id=r.automation_id WHERE r.customer_id=$1 ORDER BY r.started_at DESC LIMIT 50`, customerID))
}

// PaidSince reports whether the customer has a succeeded payment after t.
func PaidSince(ctx context.Context, db DB, customerID int64, t time.Time) (bool, error) {
	var ok bool
	err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM payments WHERE customer_id=$1 AND status='succeeded' AND paid_at > $2)`,
		customerID, t).Scan(&ok)
	return ok, err
}

// TriggerCandidate is a customer who matches a trigger right now, with context for message variables.
type TriggerCandidate struct {
	CustomerID     int64   `db:"customer_id"`
	PlanName       string  `db:"plan_name"`
	SubscriptionID *int64  `db:"subscription_id"`
	PaymentID      *string `db:"payment_id"`
}

// TriggerCandidates returns customers matching a time-based trigger. Unknown factors return nothing.
func TriggerCandidates(ctx context.Context, db DB, factor string, p TriggerParams, limit int) ([]TriggerCandidate, error) {
	var q string
	var args []any
	switch factor {
	case "recently_expired":
		q = `SELECT DISTINCT ON (s.customer_id) s.customer_id, s.title AS plan_name, s.id AS subscription_id, NULL::text AS payment_id
			FROM subscriptions s WHERE s.kind='paid' AND s.expires_at <= now() AND s.expires_at > now() - make_interval(hours => $1)
			AND NOT EXISTS (SELECT 1 FROM subscriptions x WHERE x.customer_id=s.customer_id AND x.expires_at > now() AND x.status='active')
			ORDER BY s.customer_id, s.expires_at DESC LIMIT $2`
		args = []any{p.RecentlyExpiredHours, limit}
	case "abandoned_cart":
		q = `SELECT DISTINCT ON (p.customer_id) p.customer_id, p.title AS plan_name, p.subscription_id, p.id::text AS payment_id
			FROM payments p WHERE p.status='canceled' AND NOT p.autopay AND p.operation IN ('buy','renew','change')
			AND p.created_at > now() - make_interval(hours => $1)
			AND NOT EXISTS (SELECT 1 FROM payments x WHERE x.customer_id=p.customer_id AND x.status IN ('succeeded','pending') AND x.created_at > p.created_at)
			ORDER BY p.customer_id, p.created_at DESC LIMIT $2`
		args = []any{p.AbandonedCartHours, limit}
	case "active_trial":
		q = `SELECT DISTINCT ON (s.customer_id) s.customer_id, s.title AS plan_name, s.id AS subscription_id, NULL::text AS payment_id
			FROM subscriptions s WHERE s.kind='trial' AND s.status='active' AND s.expires_at > now()
			AND s.created_at > now() - make_interval(hours => $1) ORDER BY s.customer_id, s.created_at DESC LIMIT $2`
		args = []any{p.ActiveTrialHours, limit}
	case "expiring_soon":
		q = `SELECT DISTINCT ON (s.customer_id) s.customer_id, s.title AS plan_name, s.id AS subscription_id, NULL::text AS payment_id
			FROM subscriptions s WHERE s.kind='paid' AND s.status='active' AND NOT s.autopay AND s.expires_at > now()
			AND s.expires_at <= now() + make_interval(days => $1)
			AND NOT EXISTS (SELECT 1 FROM subscriptions x WHERE x.customer_id=s.customer_id AND x.status='active'
				AND x.expires_at > now() + make_interval(days => $1))
			ORDER BY s.customer_id, s.expires_at LIMIT $2`
		args = []any{p.ExpiringSoonDays, limit}
	case "inactive_30d":
		q = `SELECT c.id AS customer_id, '' AS plan_name, NULL::bigint AS subscription_id, NULL::text AS payment_id
			FROM customers c WHERE NOT c.is_blocked AND COALESCE(c.last_seen_at, c.created_at) < now() - make_interval(days => $1)
			AND NOT EXISTS (SELECT 1 FROM subscriptions x WHERE x.customer_id=c.id AND x.status='active' AND x.expires_at > now())
			ORDER BY c.id LIMIT $2`
		args = []any{p.InactiveDays, limit}
	default:
		return nil, nil
	}
	return many[TriggerCandidate](db.Query(ctx, q, args...))
}

type TriggerParams struct {
	RecentlyExpiredHours, AbandonedCartHours, ActiveTrialHours, ExpiringSoonDays, InactiveDays int
}

type LeadParams struct {
	HotSeenDays, WarmSeenDays, CoolSeenDays, LeadMinAgeHours, StableMinPayments, StableMinDays int
}

type LeadChange struct {
	CustomerID int64  `db:"id"`
	Old        string `db:"old"`
	New        string `db:"new"`
	First      bool   `db:"first"` // never computed before: record, don't fire
}

// RecomputeLeadTemperatures updates customers whose temperature changed and returns the changes.
func RecomputeLeadTemperatures(ctx context.Context, db DB, p LeadParams) ([]LeadChange, error) {
	return many[LeadChange](db.Query(ctx, `
		WITH t AS (
			SELECT c.id, c.lead_temperature AS old, c.lead_temperature_at IS NULL AS first,
			CASE
				WHEN EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND s.kind='paid' AND s.status='active' AND s.expires_at > now())
					AND (SELECT count(*) FROM payments p WHERE p.customer_id=c.id AND p.status='succeeded') >= $5
					AND (SELECT min(paid_at) FROM payments p WHERE p.customer_id=c.id AND p.status='succeeded') <= now() - make_interval(days => $6)
					THEN 'stable'
				WHEN EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=c.id AND p.status='succeeded') THEN ''
				WHEN c.created_at > now() - make_interval(hours => $4) THEN ''
				WHEN c.last_seen_at > now() - make_interval(days => $1)
					AND EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=c.id) THEN 'hot'
				WHEN c.last_seen_at > now() - make_interval(days => $2) AND c.trial_used THEN 'warm'
				WHEN c.last_seen_at > now() - make_interval(days => $3) THEN 'cool'
				ELSE 'cold'
			END AS new
			FROM customers c WHERE NOT c.is_blocked
		)
		UPDATE customers c SET lead_temperature=t.new, lead_temperature_at=now()
		FROM t WHERE t.id=c.id AND (t.new <> t.old OR t.first)
		RETURNING c.id, t.old, t.new, t.first`, p.HotSeenDays, p.WarmSeenDays, p.CoolSeenDays, p.LeadMinAgeHours, p.StableMinPayments, p.StableMinDays))
}
