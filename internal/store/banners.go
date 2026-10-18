package store

import (
	"context"
	"time"
)

type Banner struct {
	ID          int64      `db:"id" json:"id"`
	Kind        string     `db:"kind" json:"kind"`
	Title       string     `db:"title" json:"title"`
	Subtitle    string     `db:"subtitle" json:"subtitle"`
	Icon        string     `db:"icon" json:"icon"`
	Segment     string     `db:"segment" json:"segment"`
	ButtonText  string     `db:"button_text" json:"button_text"`
	ButtonURL   string     `db:"button_url" json:"button_url"`
	StartsAt    *time.Time `db:"starts_at" json:"starts_at"`
	EndsAt      *time.Time `db:"ends_at" json:"ends_at"`
	IsActive    bool       `db:"is_active" json:"is_active"`
	Dismissible bool       `db:"dismissible" json:"dismissible"`
	Sort        int        `db:"sort" json:"sort"`
	CreatedAt   time.Time  `db:"created_at" json:"created_at"`
}

func ListBanners(ctx context.Context, db DB) ([]Banner, error) {
	return many[Banner](db.Query(ctx, `SELECT * FROM banners ORDER BY sort, id`))
}

func GetBanner(ctx context.Context, db DB, id int64) (*Banner, error) {
	return one[Banner](db.Query(ctx, `SELECT * FROM banners WHERE id=$1`, id))
}

// ActiveBannersFor lists banners live now that the customer has not dismissed.
func ActiveBannersFor(ctx context.Context, db DB, customerID int64) ([]Banner, error) {
	return many[Banner](db.Query(ctx, `SELECT b.* FROM banners b
		WHERE b.is_active AND (b.starts_at IS NULL OR b.starts_at <= now()) AND (b.ends_at IS NULL OR b.ends_at > now())
		AND NOT EXISTS (SELECT 1 FROM banner_dismissals d WHERE d.banner_id=b.id AND d.customer_id=$1)
		ORDER BY b.sort, b.id`, customerID))
}

func SaveBanner(ctx context.Context, db DB, b *Banner) error {
	if b.ID == 0 {
		return db.QueryRow(ctx, `INSERT INTO banners (title, subtitle, icon, segment, button_text, button_url, starts_at, ends_at,
			is_active, dismissible, sort) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,
			COALESCE((SELECT max(sort)+1 FROM banners), 0)) RETURNING id, kind, sort, created_at`,
			b.Title, b.Subtitle, b.Icon, b.Segment, b.ButtonText, b.ButtonURL, b.StartsAt, b.EndsAt, b.IsActive, b.Dismissible).
			Scan(&b.ID, &b.Kind, &b.Sort, &b.CreatedAt)
	}
	tag, err := db.Exec(ctx, `UPDATE banners SET title=$2, subtitle=$3, icon=$4, segment=$5, button_text=$6, button_url=$7,
		starts_at=$8, ends_at=$9, is_active=$10, dismissible=$11 WHERE id=$1`,
		b.ID, b.Title, b.Subtitle, b.Icon, b.Segment, b.ButtonText, b.ButtonURL, b.StartsAt, b.EndsAt, b.IsActive, b.Dismissible)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteBanner removes a custom banner; system banners can only be switched off.
func DeleteBanner(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `DELETE FROM banners WHERE id=$1 AND kind=''`, id)
	return err
}

func ReorderBanners(ctx context.Context, db DB, ids []int64) error {
	_, err := db.Exec(ctx, `UPDATE banners b SET sort=x.ord FROM unnest($1::bigint[]) WITH ORDINALITY AS x(id, ord) WHERE b.id=x.id`, ids)
	return err
}

func DismissBanner(ctx context.Context, db DB, bannerID, customerID int64) error {
	_, err := db.Exec(ctx, `INSERT INTO banner_dismissals (banner_id, customer_id) SELECT id, $2 FROM banners
		WHERE id=$1 AND dismissible ON CONFLICT DO NOTHING`, bannerID, customerID)
	return err
}
