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
	Status            string     `db:"status" json:"status"`
	CreatedAt         time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at" json:"updated_at"`
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
		extra_devices=$13, status=$14, updated_at=now() WHERE id=$1`,
		s.ID, s.PlanID, s.Kind, s.Title, s.RWUserID, s.RWUUID, s.RWShortUUID, s.RWUsername, s.SubscriptionURL,
		s.ExpiresAt, s.TrafficLimitBytes, s.DeviceLimit, s.ExtraDevices, s.Status)
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
