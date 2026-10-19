package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// CachedAvatar returns the stored photo (nil = the user has none) and when it was fetched; ok=false if never fetched.
func CachedAvatar(ctx context.Context, db DB, customerID int64) (data []byte, fetched time.Time, ok bool, err error) {
	err = db.QueryRow(ctx, `SELECT data, fetched_at FROM customer_avatars WHERE customer_id=$1`, customerID).Scan(&data, &fetched)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	return data, fetched, err == nil, err
}

func SaveAvatar(ctx context.Context, db DB, customerID int64, data []byte) error {
	if data == nil {
		data = []byte{}
	}
	_, err := db.Exec(ctx, `INSERT INTO customer_avatars (customer_id, data, fetched_at) VALUES ($1, $2, now())
		ON CONFLICT (customer_id) DO UPDATE SET data=EXCLUDED.data, fetched_at=now()`, customerID, data)
	return err
}
