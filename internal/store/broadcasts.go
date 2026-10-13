package store

import (
	"context"
	"time"
)

type BroadcastAudience struct {
	// all | active | expired | trial | no_sub | never_paid | plan
	Segment string `json:"segment"`
	PlanID  int64  `json:"plan_id,omitempty"`
}

type BroadcastButton struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

type Broadcast struct {
	ID          int64             `db:"id" json:"id"`
	Title       string            `db:"title" json:"title"`
	Body        string            `db:"body" json:"body"`
	Image       string            `db:"image" json:"image"`
	Buttons     []BroadcastButton `db:"buttons" json:"buttons"`
	Audience    BroadcastAudience `db:"audience" json:"audience"`
	Channel     string            `db:"channel" json:"channel"`
	ShowInNews  bool              `db:"show_in_news" json:"show_in_news"`
	Status      string            `db:"status" json:"status"`
	ScheduledAt *time.Time        `db:"scheduled_at" json:"scheduled_at"`
	CursorID    int64             `db:"cursor_id" json:"-"`
	Total       int               `db:"total" json:"total"`
	Sent        int               `db:"sent" json:"sent"`
	Failed      int               `db:"failed" json:"failed"`
	CreatedBy   *int64            `db:"created_by" json:"created_by"`
	CreatedAt   time.Time         `db:"created_at" json:"created_at"`
	StartedAt   *time.Time        `db:"started_at" json:"started_at"`
	FinishedAt  *time.Time        `db:"finished_at" json:"finished_at"`
}

func SaveBroadcast(ctx context.Context, db DB, b *Broadcast) error {
	if b.Buttons == nil {
		b.Buttons = []BroadcastButton{}
	}
	if b.ID == 0 {
		return db.QueryRow(ctx, `INSERT INTO broadcasts (title, body, image, buttons, audience, channel, show_in_news, status, scheduled_at, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id, created_at`,
			b.Title, b.Body, b.Image, b.Buttons, b.Audience, b.Channel, b.ShowInNews, b.Status, b.ScheduledAt, b.CreatedBy).Scan(&b.ID, &b.CreatedAt)
	}
	tag, err := db.Exec(ctx, `UPDATE broadcasts SET title=$2, body=$3, image=$4, buttons=$5, audience=$6, channel=$7, show_in_news=$8,
		status=$9, scheduled_at=$10 WHERE id=$1 AND status IN ('draft','scheduled')`,
		b.ID, b.Title, b.Body, b.Image, b.Buttons, b.Audience, b.Channel, b.ShowInNews, b.Status, b.ScheduledAt)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func GetBroadcast(ctx context.Context, db DB, id int64) (*Broadcast, error) {
	return one[Broadcast](db.Query(ctx, `SELECT * FROM broadcasts WHERE id=$1`, id))
}

func ListBroadcasts(ctx context.Context, db DB, limit int) ([]Broadcast, error) {
	return many[Broadcast](db.Query(ctx, `SELECT * FROM broadcasts ORDER BY created_at DESC LIMIT $1`, limit))
}

func DeleteBroadcast(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `DELETE FROM broadcasts WHERE id=$1`, id)
	return err
}

func SetBroadcastNews(ctx context.Context, db DB, id int64, on bool) error {
	_, err := db.Exec(ctx, `UPDATE broadcasts SET show_in_news=$2 WHERE id=$1`, id, on)
	return err
}

func CancelBroadcast(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `UPDATE broadcasts SET status='canceled', finished_at=now() WHERE id=$1 AND status IN ('scheduled','sending')`, id)
	return err
}

// StartDueBroadcasts moves scheduled broadcasts whose time came to sending.
func StartDueBroadcasts(ctx context.Context, db DB) error {
	_, err := db.Exec(ctx, `UPDATE broadcasts SET status='sending', started_at=now() WHERE status='scheduled' AND COALESCE(scheduled_at, now()) <= now()`)
	return err
}

func SendingBroadcast(ctx context.Context, db DB) (*Broadcast, error) {
	return one[Broadcast](db.Query(ctx, `SELECT * FROM broadcasts WHERE status='sending' ORDER BY started_at LIMIT 1`))
}

func audienceWhere() string {
	return `NOT c.is_blocked AND c.id > $1 AND (
		($2 = 'all') OR
		($2 = 'active' AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND s.status='active' AND s.expires_at > now())) OR
		($2 = 'paid_active' AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND s.kind='paid' AND s.status='active' AND s.expires_at > now())) OR
		($2 = 'trial' AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND s.kind='trial' AND s.status='active' AND s.expires_at > now())) OR
		($2 = 'expired' AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id)
			AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND s.status='active' AND s.expires_at > now())) OR
		($2 = 'no_sub' AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id)) OR
		($2 = 'never_paid' AND NOT EXISTS (SELECT 1 FROM payments p WHERE p.customer_id=c.id AND p.status='succeeded')) OR
		($2 = 'plan' AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND s.plan_id=$3 AND s.status='active' AND s.expires_at > now()))
	) AND (
		($4 = 'telegram' AND c.telegram_id IS NOT NULL AND NOT c.bot_blocked) OR
		($4 = 'email' AND c.email IS NOT NULL) OR
		($4 = 'all' AND ((c.telegram_id IS NOT NULL AND NOT c.bot_blocked) OR c.email IS NOT NULL))
	)`
}

func CountAudience(ctx context.Context, db DB, a BroadcastAudience, channel string) (int, error) {
	var n int
	err := db.QueryRow(ctx, `SELECT count(*) FROM customers c WHERE `+audienceWhere(), 0, a.Segment, a.PlanID, channel).Scan(&n)
	return n, err
}

func AudienceBatch(ctx context.Context, db DB, a BroadcastAudience, channel string, after int64, limit int) ([]Customer, error) {
	return many[Customer](db.Query(ctx, `SELECT `+prefixCols("c.", customerCols)+` FROM customers c WHERE `+audienceWhere()+
		` ORDER BY c.id LIMIT $5`, after, a.Segment, a.PlanID, channel, limit))
}

func BroadcastProgress(ctx context.Context, db DB, id, cursor int64, sent, failed int) error {
	_, err := db.Exec(ctx, `UPDATE broadcasts SET cursor_id=$2, sent=sent+$3, failed=failed+$4 WHERE id=$1`, id, cursor, sent, failed)
	return err
}

func SetBroadcastTotal(ctx context.Context, db DB, id int64, total int) error {
	_, err := db.Exec(ctx, `UPDATE broadcasts SET total=$2 WHERE id=$1`, id, total)
	return err
}

func FinishBroadcast(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `UPDATE broadcasts SET status='done', finished_at=now() WHERE id=$1 AND status='sending'`, id)
	return err
}

// News is the feed of finished broadcasts marked for the cabinet.
func News(ctx context.Context, db DB, limit int) ([]Broadcast, error) {
	return many[Broadcast](db.Query(ctx, `SELECT * FROM broadcasts WHERE status='done' AND show_in_news ORDER BY finished_at DESC LIMIT $1`, limit))
}

func UnreadNews(ctx context.Context, db DB, c *Customer) (int, error) {
	var n int
	err := db.QueryRow(ctx, `SELECT count(*) FROM broadcasts WHERE status='done' AND show_in_news
		AND finished_at > COALESCE($1::timestamptz, $2::timestamptz)`, c.NewsSeenAt, c.CreatedAt).Scan(&n)
	return n, err
}

func MarkNewsSeen(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `UPDATE customers SET news_seen_at=now() WHERE id=$1`, id)
	return err
}
