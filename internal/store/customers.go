// Package store holds all SQL. Business rules live in package service.
package store

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

// DB is satisfied by *pgxpool.Pool and pgx.Tx so store functions work inside transactions.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Store struct {
	Pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

// Tx runs fn in a transaction.
func (s *Store) Tx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.Pool, fn)
}

func one[T any](rows pgx.Rows, err error) (*T, error) {
	if err != nil {
		return nil, err
	}
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByNameLax[T])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

func many[T any](rows pgx.Rows, err error) ([]T, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByNameLax[T])
}

type Customer struct {
	ID               int64      `db:"id" json:"id"`
	TelegramID       *int64     `db:"telegram_id" json:"telegram_id"`
	Email            *string    `db:"email" json:"email"`
	Username         *string    `db:"username" json:"username"`
	FirstName        *string    `db:"first_name" json:"first_name"`
	LastName         *string    `db:"last_name" json:"last_name"`
	Language         string     `db:"language" json:"language"`
	IsAdmin          bool       `db:"is_admin" json:"is_admin"`
	IsBlocked        bool       `db:"is_blocked" json:"is_blocked"`
	BotBlocked       bool       `db:"bot_blocked" json:"bot_blocked"`
	PersonalDiscount int        `db:"personal_discount" json:"personal_discount"`
	Tag              string     `db:"tag" json:"tag"`
	Notes            string     `db:"notes" json:"notes"`
	RefCode          *string    `db:"ref_code" json:"ref_code"`
	ReferrerID       *int64     `db:"referrer_id" json:"referrer_id"`
	TrialUsed        bool       `db:"trial_used" json:"trial_used"`
	Source           string     `db:"source" json:"source"`
	CreatedAt        time.Time  `db:"created_at" json:"created_at"`
	LastSeenAt       *time.Time `db:"last_seen_at" json:"last_seen_at"`
}

// DisplayName is the best human-readable name for notifications and the admin UI.
func (c *Customer) DisplayName() string {
	var parts []string
	if c.FirstName != nil && *c.FirstName != "" {
		parts = append(parts, *c.FirstName)
	}
	if c.LastName != nil && *c.LastName != "" {
		parts = append(parts, *c.LastName)
	}
	if len(parts) > 0 {
		return strings.Join(parts, " ")
	}
	if c.Username != nil && *c.Username != "" {
		return "@" + *c.Username
	}
	if c.Email != nil {
		return *c.Email
	}
	return "Клиент"
}

const customerCols = `id, telegram_id, email, username, first_name, last_name, language, is_admin, is_blocked,
	bot_blocked, personal_discount, tag, notes, ref_code, referrer_id, trial_used, source, created_at, last_seen_at`

func GetCustomer(ctx context.Context, db DB, id int64) (*Customer, error) {
	return one[Customer](db.Query(ctx, `SELECT `+customerCols+` FROM customers WHERE id=$1`, id))
}

func GetCustomerForUpdate(ctx context.Context, db DB, id int64) (*Customer, error) {
	return one[Customer](db.Query(ctx, `SELECT `+customerCols+` FROM customers WHERE id=$1 FOR UPDATE`, id))
}

func GetCustomerByTelegram(ctx context.Context, db DB, tgID int64) (*Customer, error) {
	return one[Customer](db.Query(ctx, `SELECT `+customerCols+` FROM customers WHERE telegram_id=$1`, tgID))
}

func GetCustomerByEmail(ctx context.Context, db DB, email string) (*Customer, error) {
	return one[Customer](db.Query(ctx, `SELECT `+customerCols+` FROM customers WHERE email=$1`, email))
}

func GetCustomerByRefCode(ctx context.Context, db DB, code string) (*Customer, error) {
	return one[Customer](db.Query(ctx, `SELECT `+customerCols+` FROM customers WHERE ref_code=$1`, code))
}

type NewCustomer struct {
	TelegramID *int64
	Email      *string
	Username   *string
	FirstName  *string
	LastName   *string
	Language   string
	ReferrerID *int64
	Source     string
	IsAdmin    bool
}

func CreateCustomer(ctx context.Context, db DB, in NewCustomer) (*Customer, error) {
	if in.Language == "" {
		in.Language = "ru"
	}
	for attempt := 0; ; attempt++ {
		code := RandomCode(8)
		c, err := one[Customer](db.Query(ctx, `INSERT INTO customers
			(telegram_id, email, username, first_name, last_name, language, referrer_id, source, is_admin, ref_code, last_seen_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, now()) RETURNING `+customerCols,
			in.TelegramID, in.Email, in.Username, in.FirstName, in.LastName, in.Language, in.ReferrerID, in.Source, in.IsAdmin, code))
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "customers_ref_code_key" && attempt < 5 {
			continue
		}
		return c, err
	}
}

// UpdateTelegramProfile refreshes names coming from Telegram.
func UpdateTelegramProfile(ctx context.Context, db DB, id int64, username, first, last *string, lang string) error {
	_, err := db.Exec(ctx, `UPDATE customers SET username=$2, first_name=$3, last_name=$4,
		language=COALESCE(NULLIF($5,''), language), last_seen_at=now(), bot_blocked=false WHERE id=$1`,
		id, username, first, last, lang)
	return err
}

func TouchCustomer(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `UPDATE customers SET last_seen_at=now() WHERE id=$1 AND (last_seen_at IS NULL OR last_seen_at < now() - interval '5 minutes')`, id)
	return err
}

func SetCustomerTelegram(ctx context.Context, db DB, id, tgID int64) error {
	_, err := db.Exec(ctx, `UPDATE customers SET telegram_id=$2 WHERE id=$1`, id, tgID)
	return err
}

func SetCustomerEmail(ctx context.Context, db DB, id int64, email string) error {
	_, err := db.Exec(ctx, `UPDATE customers SET email=$2 WHERE id=$1`, id, email)
	return err
}

func SetBotBlocked(ctx context.Context, db DB, tgID int64, blocked bool) error {
	_, err := db.Exec(ctx, `UPDATE customers SET bot_blocked=$2 WHERE telegram_id=$1`, tgID, blocked)
	return err
}

func SetTrialUsed(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `UPDATE customers SET trial_used=true WHERE id=$1`, id)
	return err
}

type CustomerAdminPatch struct {
	Tag              *string `json:"tag"`
	Notes            *string `json:"notes"`
	PersonalDiscount *int    `json:"personal_discount"`
	IsBlocked        *bool   `json:"is_blocked"`
	IsAdmin          *bool   `json:"is_admin"`
}

func PatchCustomer(ctx context.Context, db DB, id int64, p CustomerAdminPatch) error {
	_, err := db.Exec(ctx, `UPDATE customers SET
		tag=COALESCE($2, tag), notes=COALESCE($3, notes), personal_discount=COALESCE($4, personal_discount),
		is_blocked=COALESCE($5, is_blocked), is_admin=COALESCE($6, is_admin) WHERE id=$1`,
		id, p.Tag, p.Notes, p.PersonalDiscount, p.IsBlocked, p.IsAdmin)
	return err
}

type CustomerFilter struct {
	Query  string
	Status string // active | expired | none | blocked | bot_blocked | trial
	PlanID int64
	Tag    string
	Sort   string // created | last_seen | expires
	Limit  int
	Offset int
}

type CustomerRow struct {
	Customer
	SubsCount     int        `db:"subs_count" json:"subs_count"`
	PaymentsCount int        `db:"payments_count" json:"payments_count"`
	PaidTotal     int64      `db:"paid_total" json:"paid_total"`
	RefCount      int        `db:"ref_count" json:"ref_count"`
	ExpiresAt     *time.Time `db:"expires_at" json:"expires_at"`
}

func ListCustomers(ctx context.Context, db DB, f CustomerFilter) ([]CustomerRow, int, error) {
	where := []string{"true"}
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+itoa(len(args))))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		q = strings.TrimPrefix(q, "@")
		add(`(c.username ILIKE '%'||?||'%' OR c.email::text ILIKE '%'||?||'%' OR c.first_name ILIKE '%'||?||'%'
			OR c.telegram_id::text = ? OR c.id::text = ? OR c.ref_code = ?
			OR EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND (s.rw_short_uuid=? OR s.rw_username=?)))`, q)
	}
	switch f.Status {
	case "active":
		where = append(where, `EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND s.expires_at > now())`)
	case "expired":
		where = append(where, `EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id) AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND s.expires_at > now())`)
	case "none":
		where = append(where, `NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id)`)
	case "trial":
		where = append(where, `EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND s.kind='trial' AND s.expires_at > now())`)
	case "blocked":
		where = append(where, `c.is_blocked`)
	case "bot_blocked":
		where = append(where, `c.bot_blocked`)
	}
	if f.PlanID > 0 {
		add(`EXISTS (SELECT 1 FROM subscriptions s WHERE s.customer_id=c.id AND s.plan_id=? AND s.expires_at > now())`, f.PlanID)
	}
	if f.Tag != "" {
		add(`c.tag = ?`, f.Tag)
	}
	order := "c.created_at DESC"
	switch f.Sort {
	case "last_seen":
		order = "c.last_seen_at DESC NULLS LAST"
	case "expires":
		order = "expires_at ASC NULLS LAST"
	case "paid":
		order = "paid_total DESC"
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM customers c WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := many[CustomerRow](db.Query(ctx, `SELECT `+prefixCols("c.", customerCols)+`,
		(SELECT count(*) FROM subscriptions s WHERE s.customer_id=c.id) AS subs_count,
		(SELECT count(*) FROM payments p WHERE p.customer_id=c.id AND p.status='succeeded') AS payments_count,
		(SELECT COALESCE(sum(amount),0) FROM payments p WHERE p.customer_id=c.id AND p.status='succeeded') AS paid_total,
		(SELECT count(*) FROM customers r WHERE r.referrer_id=c.id) AS ref_count,
		(SELECT max(expires_at) FROM subscriptions s WHERE s.customer_id=c.id) AS expires_at
		FROM customers c WHERE `+cond+` ORDER BY `+order+` LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)), args...))
	return rows, total, err
}

func prefixCols(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = p + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O, 1/I/L

// RandomCode returns an unambiguous uppercase code.
func RandomCode(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(b)
}

// AddEvent appends to the customer timeline.
func AddEvent(ctx context.Context, db DB, customerID int64, kind string, data any, actorID *int64) error {
	if data == nil {
		data = map[string]any{}
	}
	_, err := db.Exec(ctx, `INSERT INTO customer_events (customer_id, kind, data, actor_id) VALUES ($1,$2,$3,$4)`,
		customerID, kind, data, actorID)
	return err
}
