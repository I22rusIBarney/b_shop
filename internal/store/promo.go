package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Promo struct {
	ID               int64      `db:"id" json:"id"`
	Code             string     `db:"code" json:"code"`
	Kind             string     `db:"kind" json:"kind"`
	Value            int        `db:"value" json:"value"`
	Folder           string     `db:"folder" json:"folder"`
	Description      string     `db:"description" json:"description"`
	IsActive         bool       `db:"is_active" json:"is_active"`
	MaxUses          int        `db:"max_uses" json:"max_uses"`
	ValidFrom        *time.Time `db:"valid_from" json:"valid_from"`
	ValidUntil       *time.Time `db:"valid_until" json:"valid_until"`
	PlanIDs          []int64    `db:"plan_ids" json:"plan_ids"`
	NewCustomersOnly bool       `db:"new_customers_only" json:"new_customers_only"`
	CustomerID       *int64     `db:"customer_id" json:"customer_id"`
	ReserveHours     int        `db:"reserve_hours" json:"reserve_hours"`
	CreatedAt        time.Time  `db:"created_at" json:"created_at"`
	Used             int        `db:"used" json:"used"`         // redemptions with status used
	Reserved         int        `db:"reserved" json:"reserved"` // redemptions waiting for payment
}

// AppliesToPlan reports whether a discount code may be used for the plan.
func (p *Promo) AppliesToPlan(planID int64) bool {
	if len(p.PlanIDs) == 0 {
		return true
	}
	for _, id := range p.PlanIDs {
		if id == planID {
			return true
		}
	}
	return false
}

// State is a short status for lists: active, inactive, expired, exhausted, scheduled.
func (p *Promo) State(now time.Time) string {
	switch {
	case !p.IsActive:
		return "inactive"
	case p.ValidUntil != nil && now.After(*p.ValidUntil):
		return "expired"
	case p.MaxUses > 0 && p.Used >= p.MaxUses:
		return "exhausted"
	case p.ValidFrom != nil && now.Before(*p.ValidFrom):
		return "scheduled"
	}
	return "active"
}

const promoSelect = `SELECT p.*,
	(SELECT count(*) FROM promo_redemptions r WHERE r.promo_id=p.id AND r.status='used')::int AS used,
	(SELECT count(*) FROM promo_redemptions r WHERE r.promo_id=p.id AND r.status='reserved')::int AS reserved
	FROM promo_codes p`

func GetPromoByCode(ctx context.Context, db DB, code string) (*Promo, error) {
	return one[Promo](db.Query(ctx, promoSelect+` WHERE p.code=$1`, code))
}

func GetPromo(ctx context.Context, db DB, id int64) (*Promo, error) {
	return one[Promo](db.Query(ctx, promoSelect+` WHERE p.id=$1`, id))
}

// GetPromoForUpdate locks the code row so usage limits are checked race-free.
func GetPromoForUpdate(ctx context.Context, db DB, id int64) (*Promo, error) {
	if _, err := db.Exec(ctx, `SELECT 1 FROM promo_codes WHERE id=$1 FOR UPDATE`, id); err != nil {
		return nil, err
	}
	return GetPromo(ctx, db, id)
}

type PromoFilter struct {
	Search string
	Folder *string
	Kind   string
}

func ListPromos(ctx context.Context, db DB, f PromoFilter) ([]Promo, error) {
	return many[Promo](db.Query(ctx, promoSelect+` WHERE ($1='' OR p.code ILIKE '%'||$1||'%' OR p.description ILIKE '%'||$1||'%')
		AND ($2::text IS NULL OR p.folder=$2) AND ($3='' OR p.kind=$3) ORDER BY p.created_at DESC LIMIT 1000`,
		f.Search, f.Folder, f.Kind))
}

func PromoFolders(ctx context.Context, db DB) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT DISTINCT folder FROM promo_codes WHERE folder<>'' ORDER BY folder`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func SavePromo(ctx context.Context, db DB, p *Promo) error {
	if p.PlanIDs == nil {
		p.PlanIDs = []int64{}
	}
	if p.ID == 0 {
		return db.QueryRow(ctx, `INSERT INTO promo_codes (code, kind, value, folder, description, is_active, max_uses,
			valid_from, valid_until, plan_ids, new_customers_only, customer_id, reserve_hours)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id, created_at`,
			p.Code, p.Kind, p.Value, p.Folder, p.Description, p.IsActive, p.MaxUses, p.ValidFrom, p.ValidUntil, p.PlanIDs,
			p.NewCustomersOnly, p.CustomerID, p.ReserveHours).Scan(&p.ID, &p.CreatedAt)
	}
	_, err := db.Exec(ctx, `UPDATE promo_codes SET code=$2, kind=$3, value=$4, folder=$5, description=$6, is_active=$7,
		max_uses=$8, valid_from=$9, valid_until=$10, plan_ids=$11, new_customers_only=$12, customer_id=$13, reserve_hours=$14
		WHERE id=$1`, p.ID, p.Code, p.Kind, p.Value, p.Folder, p.Description, p.IsActive, p.MaxUses, p.ValidFrom,
		p.ValidUntil, p.PlanIDs, p.NewCustomersOnly, p.CustomerID, p.ReserveHours)
	return err
}

func DeletePromo(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `DELETE FROM promo_codes WHERE id=$1`, id)
	return err
}

type Redemption struct {
	ID             int64      `db:"id" json:"id"`
	PromoID        int64      `db:"promo_id" json:"promo_id"`
	CustomerID     int64      `db:"customer_id" json:"customer_id"`
	Status         string     `db:"status" json:"status"`
	PaymentID      *uuid.UUID `db:"payment_id" json:"payment_id"`
	SubscriptionID *int64     `db:"subscription_id" json:"subscription_id"`
	Days           int        `db:"days" json:"days"`
	CreatedAt      time.Time  `db:"created_at" json:"created_at"`
	UsedAt         *time.Time `db:"used_at" json:"used_at"`
	// joined
	Code     string  `db:"code" json:"code"`
	Kind     string  `db:"kind" json:"kind"`
	Value    int     `db:"value" json:"value"`
	Customer string  `db:"customer" json:"customer,omitempty"`
	PlanIDs  []int64 `db:"plan_ids" json:"plan_ids"`
}

func GetRedemption(ctx context.Context, db DB, promoID, customerID int64) (*Redemption, error) {
	return one[Redemption](db.Query(ctx, `SELECT r.*, p.code, p.kind, p.value FROM promo_redemptions r
		JOIN promo_codes p ON p.id=r.promo_id WHERE r.promo_id=$1 AND r.customer_id=$2`, promoID, customerID))
}

// ActiveDiscounts returns the customer's reserved discount codes (best first), dropping expired reservations.
func ActiveDiscounts(ctx context.Context, db DB, customerID int64) ([]Redemption, error) {
	_, _ = db.Exec(ctx, `DELETE FROM promo_redemptions r USING promo_codes p WHERE p.id=r.promo_id AND r.customer_id=$1
		AND r.status='reserved' AND (p.reserve_hours > 0 AND r.created_at < now() - make_interval(hours => p.reserve_hours)
		OR (p.valid_until IS NOT NULL AND p.valid_until < now()) OR NOT p.is_active)`, customerID)
	return many[Redemption](db.Query(ctx, `SELECT r.*, p.code, p.kind, p.value, p.plan_ids FROM promo_redemptions r
		JOIN promo_codes p ON p.id=r.promo_id WHERE r.customer_id=$1 AND r.status='reserved' AND p.kind='discount'
		ORDER BY p.value DESC, r.id`, customerID))
}

func InsertRedemption(ctx context.Context, db DB, r *Redemption) error {
	return db.QueryRow(ctx, `INSERT INTO promo_redemptions (promo_id, customer_id, status, payment_id, subscription_id, days, used_at)
		VALUES ($1,$2,$3,$4,$5,$6, CASE WHEN $3='used' THEN now() END) RETURNING id, created_at`,
		r.PromoID, r.CustomerID, r.Status, r.PaymentID, r.SubscriptionID, r.Days).Scan(&r.ID, &r.CreatedAt)
}

// UseReservedDiscount marks the customer's reserved discount code as used by a payment.
func UseReservedDiscount(ctx context.Context, db DB, customerID int64, code string, paymentID uuid.UUID, subID int64) error {
	_, err := db.Exec(ctx, `UPDATE promo_redemptions r SET status='used', used_at=now(), payment_id=$3, subscription_id=$4
		FROM promo_codes p WHERE p.id=r.promo_id AND r.customer_id=$1 AND p.code=$2 AND r.status='reserved'`,
		customerID, code, paymentID, subID)
	return err
}

func DeleteRedemption(ctx context.Context, db DB, id, customerID int64) error {
	_, err := db.Exec(ctx, `DELETE FROM promo_redemptions WHERE id=$1 AND customer_id=$2 AND status='reserved'`, id, customerID)
	return err
}

// ListRedemptions returns redemptions of a promo code (admin) or of a customer.
func ListRedemptions(ctx context.Context, db DB, promoID, customerID int64) ([]Redemption, error) {
	return many[Redemption](db.Query(ctx, `SELECT r.*, p.code, p.kind, p.value,
		COALESCE(NULLIF(c.username,''), c.email::text, c.telegram_id::text, c.id::text) AS customer
		FROM promo_redemptions r JOIN promo_codes p ON p.id=r.promo_id JOIN customers c ON c.id=r.customer_id
		WHERE ($1=0 OR r.promo_id=$1) AND ($2=0 OR r.customer_id=$2) ORDER BY r.created_at DESC LIMIT 500`, promoID, customerID))
}

// ---- referral bonuses ----

type ReferralBonus struct {
	ID             int64      `db:"id" json:"id"`
	RecipientID    int64      `db:"recipient_id" json:"recipient_id"`
	ReferralID     *int64     `db:"referral_id" json:"referral_id"`
	PaymentID      *uuid.UUID `db:"payment_id" json:"payment_id"`
	Kind           string     `db:"kind" json:"kind"`
	Days           int        `db:"days" json:"days"`
	SubscriptionID *int64     `db:"subscription_id" json:"subscription_id"`
	Pending        bool       `db:"pending" json:"pending"`
	ActorID        *int64     `db:"actor_id" json:"-"`
	CreatedAt      time.Time  `db:"created_at" json:"created_at"`
	ReferralName   string     `db:"referral_name" json:"referral_name"`
}

// InsertReferralBonus records a bonus; returns false if this payment already produced it.
func InsertReferralBonus(ctx context.Context, db DB, b *ReferralBonus) (bool, error) {
	tag, err := db.Exec(ctx, `INSERT INTO referral_bonuses (recipient_id, referral_id, payment_id, kind, days, subscription_id, pending, actor_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`,
		b.RecipientID, b.ReferralID, b.PaymentID, b.Kind, b.Days, b.SubscriptionID, b.Pending, b.ActorID)
	return tag.RowsAffected() == 1, err
}

func ListReferralBonuses(ctx context.Context, db DB, recipientID int64, limit int) ([]ReferralBonus, error) {
	return many[ReferralBonus](db.Query(ctx, `SELECT b.*, COALESCE(c.first_name, NULLIF(c.username,''), 'Друг') AS referral_name
		FROM referral_bonuses b LEFT JOIN customers c ON c.id=b.referral_id
		WHERE ($1=0 OR b.recipient_id=$1) ORDER BY b.created_at DESC LIMIT $2`, recipientID, limit))
}

type ReferralStats struct {
	Invited int `json:"invited"`
	Paid    int `json:"paid"`
	Days    int `json:"days"`
}

func GetReferralStats(ctx context.Context, db DB, customerID int64) (ReferralStats, error) {
	var s ReferralStats
	err := db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM customers WHERE referrer_id=$1)::int,
		(SELECT count(DISTINCT c.id) FROM customers c JOIN payments p ON p.customer_id=c.id
			WHERE c.referrer_id=$1 AND p.status='succeeded' AND p.provider<>'free' AND p.applied_at IS NOT NULL)::int,
		(SELECT COALESCE(sum(days),0) FROM referral_bonuses WHERE recipient_id=$1 AND kind<>'invitee')::int`,
		customerID).Scan(&s.Invited, &s.Paid, &s.Days)
	return s, err
}

// PaidPurchasesBefore counts the customer's applied paid purchases other than the given payment.
func PaidPurchasesBefore(ctx context.Context, db DB, customerID int64, except uuid.UUID) (int, error) {
	var n int
	err := db.QueryRow(ctx, `SELECT count(*) FROM payments WHERE customer_id=$1 AND id<>$2 AND status IN ('succeeded','refunded')
		AND provider<>'free' AND applied_at IS NOT NULL AND operation IN ('buy','renew','autopay')`, customerID, except).Scan(&n)
	return n, err
}

func AddBonusDays(ctx context.Context, db DB, customerID int64, days int) error {
	_, err := db.Exec(ctx, `UPDATE customers SET bonus_days=GREATEST(bonus_days+$2, 0) WHERE id=$1`, customerID, days)
	return err
}

// ---- plan changes ----

type PlanChange struct {
	ID             int64      `db:"id" json:"id"`
	CustomerID     int64      `db:"customer_id" json:"customer_id"`
	SubscriptionID *int64     `db:"subscription_id" json:"subscription_id"`
	FromPlanID     *int64     `db:"from_plan_id" json:"from_plan_id"`
	ToPlanID       *int64     `db:"to_plan_id" json:"to_plan_id"`
	Mode           string     `db:"mode" json:"mode"`
	DaysBefore     int        `db:"days_before" json:"days_before"`
	DaysAfter      int        `db:"days_after" json:"days_after"`
	PaymentID      *uuid.UUID `db:"payment_id" json:"payment_id"`
	CreatedAt      time.Time  `db:"created_at" json:"created_at"`
	FromName       string     `db:"from_name" json:"from_name"`
	ToName         string     `db:"to_name" json:"to_name"`
}

func InsertPlanChange(ctx context.Context, db DB, c *PlanChange) error {
	return db.QueryRow(ctx, `INSERT INTO plan_changes (customer_id, subscription_id, from_plan_id, to_plan_id, mode,
		days_before, days_after, payment_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, created_at`,
		c.CustomerID, c.SubscriptionID, c.FromPlanID, c.ToPlanID, c.Mode, c.DaysBefore, c.DaysAfter, c.PaymentID).
		Scan(&c.ID, &c.CreatedAt)
}

func ListPlanChanges(ctx context.Context, db DB, customerID int64) ([]PlanChange, error) {
	return many[PlanChange](db.Query(ctx, `SELECT pc.*, COALESCE(f.name,'—') AS from_name, COALESCE(t.name,'—') AS to_name
		FROM plan_changes pc LEFT JOIN plans f ON f.id=pc.from_plan_id LEFT JOIN plans t ON t.id=pc.to_plan_id
		WHERE pc.customer_id=$1 ORDER BY pc.created_at DESC LIMIT 100`, customerID))
}

// UnlockedPlans returns the hidden plans a customer has opened with a promo code.
func UnlockedPlans(ctx context.Context, db DB, customerID int64) (map[int64]bool, error) {
	rows, err := db.Query(ctx, `SELECT plan_id FROM unlocked_plans WHERE customer_id=$1`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// UnlockPlans opens plans for a customer; returns how many were not open before.
func UnlockPlans(ctx context.Context, db DB, customerID int64, planIDs []int64, promoID *int64) (int, error) {
	tag, err := db.Exec(ctx, `INSERT INTO unlocked_plans (customer_id, plan_id, promo_id)
		SELECT $1, id, $3 FROM plans WHERE id = ANY($2) ON CONFLICT DO NOTHING`, customerID, planIDs, promoID)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// UnlockedPlanNames lists the customer's opened plans for the admin card.
func UnlockedPlanNames(ctx context.Context, db DB, customerID int64) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT p.name FROM unlocked_plans u JOIN plans p ON p.id=u.plan_id WHERE u.customer_id=$1 ORDER BY u.created_at`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
