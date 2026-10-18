package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Gift struct {
	ID             int64      `db:"id" json:"id"`
	Code           string     `db:"code" json:"code"`
	BuyerID        int64      `db:"buyer_id" json:"-"`
	PaymentID      *uuid.UUID `db:"payment_id" json:"-"`
	PlanID         *int64     `db:"plan_id" json:"plan_id"`
	Title          string     `db:"title" json:"title"`
	Days           int        `db:"days" json:"days"`
	Status         string     `db:"status" json:"status"`
	RedeemedBy     *int64     `db:"redeemed_by" json:"-"`
	SubscriptionID *int64     `db:"subscription_id" json:"-"`
	CreatedAt      time.Time  `db:"created_at" json:"created_at"`
	RedeemedAt     *time.Time `db:"redeemed_at" json:"redeemed_at"`
}

func InsertGift(ctx context.Context, db DB, g *Gift) error {
	return db.QueryRow(ctx, `INSERT INTO gifts (code, buyer_id, payment_id, plan_id, title, days)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, status, created_at`,
		g.Code, g.BuyerID, g.PaymentID, g.PlanID, g.Title, g.Days).Scan(&g.ID, &g.Status, &g.CreatedAt)
}

func GetGiftByCodeForUpdate(ctx context.Context, db DB, code string) (*Gift, error) {
	return one[Gift](db.Query(ctx, `SELECT * FROM gifts WHERE code=$1 FOR UPDATE`, strings.ToUpper(strings.TrimSpace(code))))
}

func MarkGiftRedeemed(ctx context.Context, db DB, id, customerID, subID int64) error {
	_, err := db.Exec(ctx, `UPDATE gifts SET status='redeemed', redeemed_by=$2, subscription_id=$3, redeemed_at=now() WHERE id=$1`,
		id, customerID, subID)
	return err
}

func ListGiftsByBuyer(ctx context.Context, db DB, buyerID int64) ([]Gift, error) {
	return many[Gift](db.Query(ctx, `SELECT * FROM gifts WHERE buyer_id=$1 ORDER BY created_at DESC LIMIT 100`, buyerID))
}

// FindCustomerExact finds a customer by Telegram ID, @username or email — exact match only.
func FindCustomerExact(ctx context.Context, db DB, q string) (*Customer, error) {
	q = strings.TrimSpace(q)
	return one[Customer](db.Query(ctx, `SELECT `+customerCols+` FROM customers WHERE NOT is_blocked AND (
		($1 ~ '^[0-9]{3,15}$' AND telegram_id = $1::bigint)
		OR (left($1,1) = '@' AND lower(username) = lower(substr($1,2)))
		OR (position('@' in $1) > 1 AND email = $1)
		OR ($1 ~ '^[A-Za-z0-9_]{4,32}$' AND $1 !~ '^[0-9]+$' AND lower(username) = lower($1))
	) LIMIT 1`, q))
}
