package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Subscription struct {
	ID                int64      `db:"id" json:"id"`
	CustomerID        int64      `db:"customer_id" json:"customer_id"`
	PlanID            *int64     `db:"plan_id" json:"plan_id"`
	Kind              string     `db:"kind" json:"kind"`
	IsPrimary         bool       `db:"is_primary" json:"is_primary"`
	Title             string     `db:"title" json:"title"`
	RWUserID          *int64     `db:"rw_user_id" json:"rw_user_id"`
	RWUUID            *uuid.UUID `db:"rw_uuid" json:"-"`
	RWShortUUID       *string    `db:"rw_short_uuid" json:"rw_short_uuid"`
	RWUsername        *string    `db:"rw_username" json:"rw_username"`
	SubscriptionURL   string     `db:"subscription_url" json:"subscription_url"`
	ExpiresAt         time.Time  `db:"expires_at" json:"expires_at"`
	TrafficLimitBytes int64      `db:"traffic_limit_bytes" json:"traffic_limit_bytes"`
	DeviceLimit       int        `db:"device_limit" json:"device_limit"`
	ExtraDevices      int        `db:"extra_devices" json:"extra_devices"`
	// ExtraTrafficBytes is bought traffic on top of the plan limit for the current period.
	ExtraTrafficBytes int64      `db:"extra_traffic_bytes" json:"extra_traffic_bytes"`
	Status            string     `db:"status" json:"status"`
	CreatedAt         time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at" json:"updated_at"`

	Autopay            bool       `db:"autopay" json:"autopay"`
	AutopayMethodID    *int64     `db:"autopay_method_id" json:"autopay_method_id"`
	AutopayPeriodID    *int64     `db:"autopay_period_id" json:"autopay_period_id"`
	AutopayNotifiedFor *time.Time `db:"autopay_notified_for" json:"-"`
	AutopayLastTry     *time.Time `db:"autopay_last_try" json:"-"`
}

func (s *Subscription) Active() bool { return s.ExpiresAt.After(time.Now()) && s.Status == "active" }

// TotalDevices is the panel device limit including bought extra slots (0 = unlimited).
func (s *Subscription) TotalDevices() int {
	if s.DeviceLimit == 0 {
		return 0
	}
	return s.DeviceLimit + s.ExtraDevices
}

func ListSubscriptions(ctx context.Context, db DB, customerID int64) ([]Subscription, error) {
	return many[Subscription](db.Query(ctx, `SELECT * FROM subscriptions WHERE customer_id=$1
		ORDER BY is_primary DESC, expires_at DESC, id`, customerID))
}

func GetSubscription(ctx context.Context, db DB, id int64) (*Subscription, error) {
	return one[Subscription](db.Query(ctx, `SELECT * FROM subscriptions WHERE id=$1`, id))
}

func GetSubscriptionForUpdate(ctx context.Context, db DB, id int64) (*Subscription, error) {
	return one[Subscription](db.Query(ctx, `SELECT * FROM subscriptions WHERE id=$1 FOR UPDATE`, id))
}

func GetSubscriptionByRWUser(ctx context.Context, db DB, rwUserID int64) (*Subscription, error) {
	return one[Subscription](db.Query(ctx, `SELECT * FROM subscriptions WHERE rw_user_id=$1`, rwUserID))
}

func CountSubscriptions(ctx context.Context, db DB, customerID int64, planID *int64) (int, error) {
	var n int
	err := db.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE customer_id=$1 AND ($2::bigint IS NULL OR plan_id=$2)
		AND kind <> 'trial'`, customerID, planID).Scan(&n)
	return n, err
}

func InsertSubscription(ctx context.Context, db DB, s *Subscription) error {
	return db.QueryRow(ctx, `INSERT INTO subscriptions (customer_id, plan_id, kind, is_primary, title, rw_user_id, rw_uuid,
		rw_short_uuid, rw_username, subscription_url, expires_at, traffic_limit_bytes, device_limit, extra_devices, status)
		VALUES ($1,$2,$3, NOT EXISTS (SELECT 1 FROM subscriptions WHERE customer_id=$1), $4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'active')
		RETURNING id, is_primary, created_at, updated_at`,
		s.CustomerID, s.PlanID, s.Kind, s.Title, s.RWUserID, s.RWUUID, s.RWShortUUID, s.RWUsername, s.SubscriptionURL,
		s.ExpiresAt, s.TrafficLimitBytes, s.DeviceLimit, s.ExtraDevices).Scan(&s.ID, &s.IsPrimary, &s.CreatedAt, &s.UpdatedAt)
}

func UpdateSubscription(ctx context.Context, db DB, s *Subscription) error {
	_, err := db.Exec(ctx, `UPDATE subscriptions SET plan_id=$2, kind=$3, title=$4, rw_user_id=$5, rw_uuid=$6,
		rw_short_uuid=$7, rw_username=$8, subscription_url=$9, expires_at=$10, traffic_limit_bytes=$11, device_limit=$12,
		extra_devices=$13, status=$14, extra_traffic_bytes=$15, updated_at=now() WHERE id=$1`,
		s.ID, s.PlanID, s.Kind, s.Title, s.RWUserID, s.RWUUID, s.RWShortUUID, s.RWUsername, s.SubscriptionURL,
		s.ExpiresAt, s.TrafficLimitBytes, s.DeviceLimit, s.ExtraDevices, s.Status, s.ExtraTrafficBytes)
	return err
}

func SetPrimarySubscription(ctx context.Context, db DB, customerID, id int64) error {
	_, err := db.Exec(ctx, `UPDATE subscriptions SET is_primary = (id=$2) WHERE customer_id=$1`, customerID, id)
	return err
}

func DeleteSubscription(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `DELETE FROM subscriptions WHERE id=$1`, id)
	return err
}

// SetAutopay turns autopay on (with a method and renewal period) or off.
func SetAutopay(ctx context.Context, db DB, id int64, on bool, methodID, periodID *int64) error {
	_, err := db.Exec(ctx, `UPDATE subscriptions SET autopay=$2,
		autopay_method_id=COALESCE($3, autopay_method_id), autopay_period_id=COALESCE($4, autopay_period_id) WHERE id=$1`,
		id, on, methodID, periodID)
	return err
}

// AutopayDue lists paid subscriptions with autopay on that expire before the given time.
func AutopayDue(ctx context.Context, db DB, before time.Time) ([]Subscription, error) {
	return many[Subscription](db.Query(ctx, `SELECT * FROM subscriptions WHERE autopay AND kind='paid'
		AND autopay_method_id IS NOT NULL AND expires_at < $1 AND expires_at > now() - interval '3 days'
		AND (autopay_last_try IS NULL OR autopay_last_try < now() - interval '20 hours') ORDER BY expires_at LIMIT 50`, before))
}

// AutopayToNotify lists autopay subscriptions expiring before the given time that were not warned for this expiry yet.
func AutopayToNotify(ctx context.Context, db DB, before time.Time) ([]Subscription, error) {
	return many[Subscription](db.Query(ctx, `SELECT * FROM subscriptions WHERE autopay AND kind='paid'
		AND autopay_method_id IS NOT NULL AND expires_at < $1 AND expires_at > now()
		AND (autopay_notified_for IS NULL OR autopay_notified_for <> expires_at) LIMIT 100`, before))
}

func MarkAutopayNotified(ctx context.Context, db DB, id int64, expires time.Time) error {
	_, err := db.Exec(ctx, `UPDATE subscriptions SET autopay_notified_for=$2 WHERE id=$1`, id, expires)
	return err
}

func MarkAutopayTry(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `UPDATE subscriptions SET autopay_last_try=now() WHERE id=$1`, id)
	return err
}
