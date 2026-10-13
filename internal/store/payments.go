package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Payment struct {
	ID              uuid.UUID  `db:"id" json:"id"`
	CustomerID      int64      `db:"customer_id" json:"customer_id"`
	SubscriptionID  *int64     `db:"subscription_id" json:"subscription_id"`
	PlanID          *int64     `db:"plan_id" json:"plan_id"`
	PeriodID        *int64     `db:"period_id" json:"period_id"`
	Operation       string     `db:"operation" json:"operation"`
	Title           string     `db:"title" json:"title"`
	Days            int        `db:"days" json:"days"`
	Months          int        `db:"months" json:"months"`
	ExtraDevices    int        `db:"extra_devices" json:"extra_devices"`
	Amount          int64      `db:"amount" json:"amount"`
	BaseAmount      int64      `db:"base_amount" json:"base_amount"`
	DiscountPercent int        `db:"discount_percent" json:"discount_percent"`
	PromoCode       string     `db:"promo_code" json:"promo_code"`
	Provider        string     `db:"provider" json:"provider"`
	ProviderRef     *string    `db:"provider_ref" json:"provider_ref"`
	Method          string     `db:"method" json:"method"`
	SaveMethod      bool       `db:"save_method" json:"save_method"`
	Status          string     `db:"status" json:"status"`
	ConfirmationURL string     `db:"confirmation_url" json:"confirmation_url,omitempty"`
	CancelReason    string     `db:"cancel_reason" json:"cancel_reason"`
	CreatedAt       time.Time  `db:"created_at" json:"created_at"`
	PaidAt          *time.Time `db:"paid_at" json:"paid_at"`
	AppliedAt       *time.Time `db:"applied_at" json:"applied_at"`
	ReceiptStatus   string     `db:"receipt_status" json:"receipt_status"`
	ReceiptUUID     string     `db:"receipt_uuid" json:"receipt_uuid"`
	ReceiptError    string     `db:"receipt_error" json:"receipt_error"`
	ReceiptAttempts int        `db:"receipt_attempts" json:"receipt_attempts"`
	ReceiptNextTry  *time.Time `db:"receipt_next_try" json:"-"`
	ReferralDone    bool       `db:"referral_done" json:"-"`
	FromPlanID      *int64     `db:"from_plan_id" json:"from_plan_id"`
	Autopay         bool       `db:"autopay" json:"autopay"`
	PaymentMethodID *int64     `db:"payment_method_id" json:"-"`
}

func SetPaymentMethodID(ctx context.Context, db DB, id uuid.UUID, methodID int64) error {
	_, err := db.Exec(ctx, `UPDATE payments SET payment_method_id=$2 WHERE id=$1`, id, methodID)
	return err
}

// LastPaidPeriod returns the period of the latest paid buy/renew for a subscription (for autopay).
func LastPaidPeriod(ctx context.Context, db DB, subID int64) (*int64, error) {
	var id *int64
	err := db.QueryRow(ctx, `SELECT period_id FROM payments WHERE subscription_id=$1 AND status='succeeded'
		AND operation IN ('buy','renew','autopay') AND period_id IS NOT NULL ORDER BY paid_at DESC LIMIT 1`, subID).Scan(&id)
	if err != nil {
		return nil, nil
	}
	return id, nil
}

func InsertPayment(ctx context.Context, db DB, p *Payment) error {
	return db.QueryRow(ctx, `INSERT INTO payments (customer_id, subscription_id, plan_id, period_id, operation, title, days,
		months, extra_devices, amount, base_amount, discount_percent, promo_code, provider, method, save_method, from_plan_id, autopay)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18) RETURNING id, status, created_at`,
		p.CustomerID, p.SubscriptionID, p.PlanID, p.PeriodID, p.Operation, p.Title, p.Days, p.Months, p.ExtraDevices,
		p.Amount, p.BaseAmount, p.DiscountPercent, p.PromoCode, p.Provider, p.Method, p.SaveMethod, p.FromPlanID, p.Autopay).
		Scan(&p.ID, &p.Status, &p.CreatedAt)
}

func SetPaymentProvider(ctx context.Context, db DB, id uuid.UUID, ref, confirmationURL string) error {
	_, err := db.Exec(ctx, `UPDATE payments SET provider_ref=$2, confirmation_url=$3 WHERE id=$1`, id, ref, confirmationURL)
	return err
}

func GetPayment(ctx context.Context, db DB, id uuid.UUID) (*Payment, error) {
	return one[Payment](db.Query(ctx, `SELECT * FROM payments WHERE id=$1`, id))
}

func GetPaymentForUpdate(ctx context.Context, db DB, id uuid.UUID) (*Payment, error) {
	return one[Payment](db.Query(ctx, `SELECT * FROM payments WHERE id=$1 FOR UPDATE`, id))
}

func GetPaymentByProviderRef(ctx context.Context, db DB, provider, ref string) (*Payment, error) {
	return one[Payment](db.Query(ctx, `SELECT * FROM payments WHERE provider=$1 AND provider_ref=$2`, provider, ref))
}

// MarkPaid flips a pending payment to succeeded; returns false if it was not pending.
func MarkPaid(ctx context.Context, db DB, id uuid.UUID, method string) (bool, error) {
	tag, err := db.Exec(ctx, `UPDATE payments SET status='succeeded', paid_at=now(), method=COALESCE(NULLIF($2,''), method)
		WHERE id=$1 AND status IN ('pending','canceled')`, id, method)
	return tag.RowsAffected() == 1, err
}

func MarkApplied(ctx context.Context, db DB, id uuid.UUID, subID int64) error {
	_, err := db.Exec(ctx, `UPDATE payments SET applied_at=now(), subscription_id=$2 WHERE id=$1`, id, subID)
	return err
}

func MarkCanceled(ctx context.Context, db DB, id uuid.UUID, reason string) error {
	_, err := db.Exec(ctx, `UPDATE payments SET status='canceled', cancel_reason=$2 WHERE id=$1 AND status='pending'`, id, reason)
	return err
}

func MarkRefunded(ctx context.Context, db DB, id uuid.UUID) error {
	_, err := db.Exec(ctx, `UPDATE payments SET status='refunded' WHERE id=$1 AND status='succeeded'`, id)
	return err
}

// ExpireStalePayments cancels pending payments older than the given age.
func ExpireStalePayments(ctx context.Context, db DB, age time.Duration) ([]Payment, error) {
	return many[Payment](db.Query(ctx, `UPDATE payments SET status='canceled', cancel_reason='expired'
		WHERE status='pending' AND created_at < $1 RETURNING *`, time.Now().Add(-age)))
}

// PendingPaymentsToCheck returns recent pending payments to re-verify with the provider.
func PendingPaymentsToCheck(ctx context.Context, db DB) ([]Payment, error) {
	return many[Payment](db.Query(ctx, `SELECT * FROM payments WHERE status='pending' AND provider_ref IS NOT NULL
		AND created_at > now() - interval '2 hours' AND created_at < now() - interval '2 minutes' ORDER BY created_at LIMIT 50`))
}

// PaidNotApplied finds payments that succeeded but whose subscription change failed (retry).
func PaidNotApplied(ctx context.Context, db DB) ([]Payment, error) {
	return many[Payment](db.Query(ctx, `SELECT * FROM payments WHERE status='succeeded' AND applied_at IS NULL
		AND paid_at < now() - interval '1 minute' ORDER BY paid_at LIMIT 20`))
}

// ---- receipts (Мой налог) ----

func QueueReceipt(ctx context.Context, db DB, id uuid.UUID) error {
	_, err := db.Exec(ctx, `UPDATE payments SET receipt_status='pending', receipt_next_try=now() WHERE id=$1 AND receipt_status='none'`, id)
	return err
}

func ReceiptsDue(ctx context.Context, db DB, limit int) ([]Payment, error) {
	return many[Payment](db.Query(ctx, `SELECT * FROM payments WHERE status IN ('succeeded','refunded')
		AND receipt_status IN ('pending','failed','cancel_pending') AND receipt_next_try <= now() AND receipt_attempts < 20
		ORDER BY receipt_next_try LIMIT $1`, limit))
}

func ReceiptDone(ctx context.Context, db DB, id uuid.UUID, receiptUUID string) error {
	_, err := db.Exec(ctx, `UPDATE payments SET receipt_status='done', receipt_uuid=$2, receipt_error='',
		receipt_attempts=receipt_attempts+1, receipt_next_try=NULL WHERE id=$1`, id, receiptUUID)
	return err
}

// ReceiptRetry records a failed attempt; status is 'failed' for issuing and 'cancel_pending' for annulment.
func ReceiptRetry(ctx context.Context, db DB, id uuid.UUID, status, errText string, next time.Time) error {
	if len(errText) > 500 {
		errText = errText[:500]
	}
	_, err := db.Exec(ctx, `UPDATE payments SET receipt_status=$4, receipt_error=$2,
		receipt_attempts=receipt_attempts+1, receipt_next_try=$3 WHERE id=$1`, id, errText, next, status)
	return err
}

// QueueReceiptCancel schedules annulment of an issued receipt (refund); unissued receipts are dropped.
func QueueReceiptCancel(ctx context.Context, db DB, id uuid.UUID) error {
	_, err := db.Exec(ctx, `UPDATE payments SET
		receipt_status = CASE WHEN receipt_status='done' THEN 'cancel_pending' ELSE 'canceled' END,
		receipt_next_try = now(), receipt_attempts = 0 WHERE id=$1 AND receipt_status <> 'canceled'`, id)
	return err
}

func ReceiptCanceled(ctx context.Context, db DB, id uuid.UUID) error {
	_, err := db.Exec(ctx, `UPDATE payments SET receipt_status='canceled' WHERE id=$1`, id)
	return err
}

// ---- saved methods ----

type PaymentMethod struct {
	ID          int64     `db:"id" json:"id"`
	CustomerID  int64     `db:"customer_id" json:"-"`
	Provider    string    `db:"provider" json:"provider"`
	ProviderRef string    `db:"provider_ref" json:"-"`
	Title       string    `db:"title" json:"title"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
}

func SavePaymentMethod(ctx context.Context, db DB, customerID int64, provider, ref, title string) (int64, error) {
	var id int64
	err := db.QueryRow(ctx, `INSERT INTO payment_methods (customer_id, provider, provider_ref, title) VALUES ($1,$2,$3,$4)
		ON CONFLICT (provider, provider_ref) DO UPDATE SET title=EXCLUDED.title, customer_id=EXCLUDED.customer_id
		RETURNING id`, customerID, provider, ref, title).Scan(&id)
	return id, err
}

func GetPaymentMethod(ctx context.Context, db DB, id int64) (*PaymentMethod, error) {
	return one[PaymentMethod](db.Query(ctx, `SELECT * FROM payment_methods WHERE id=$1`, id))
}

// MarkReferralDone flags a payment's referral bonuses as processed.
func MarkReferralDone(ctx context.Context, db DB, id uuid.UUID) error {
	_, err := db.Exec(ctx, `UPDATE payments SET referral_done=true WHERE id=$1`, id)
	return err
}

// ReferralPending lists applied payments whose referral bonuses are not processed yet.
func ReferralPending(ctx context.Context, db DB) ([]Payment, error) {
	return many[Payment](db.Query(ctx, `SELECT * FROM payments WHERE referral_done=false AND applied_at IS NOT NULL
		AND applied_at < now() - interval '30 seconds' ORDER BY applied_at LIMIT 20`))
}

func ListPaymentMethods(ctx context.Context, db DB, customerID int64) ([]PaymentMethod, error) {
	return many[PaymentMethod](db.Query(ctx, `SELECT * FROM payment_methods WHERE customer_id=$1 ORDER BY id DESC`, customerID))
}

func DeletePaymentMethod(ctx context.Context, db DB, customerID, id int64) error {
	_, err := db.Exec(ctx, `DELETE FROM payment_methods WHERE id=$1 AND customer_id=$2`, id, customerID)
	return err
}

// ---- listing for admin / cabinet ----

type PaymentFilter struct {
	CustomerID int64
	Status     string
	Query      string
	From, To   *time.Time
	PlanID     int64
	Limit      int
	Offset     int
}

type PaymentRow struct {
	Payment
	CustomerName *string `db:"customer_name" json:"customer_name"`
	PlanName     *string `db:"plan_name" json:"plan_name"`
}

func ListPayments(ctx context.Context, db DB, f PaymentFilter) ([]PaymentRow, int, error) {
	where := []string{"true"}
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+itoa(len(args))))
	}
	if f.CustomerID > 0 {
		add("p.customer_id = ?", f.CustomerID)
	}
	if f.Status != "" {
		add("p.status = ?", f.Status)
	}
	if f.PlanID > 0 {
		add("p.plan_id = ?", f.PlanID)
	}
	if f.From != nil {
		add("p.created_at >= ?", *f.From)
	}
	if f.To != nil {
		add("p.created_at < ?", *f.To)
	}
	if q := strings.TrimSpace(strings.TrimPrefix(f.Query, "@")); q != "" {
		add(`(p.provider_ref = ? OR p.id::text = ? OR c.username ILIKE '%'||?||'%' OR c.email::text ILIKE '%'||?||'%'
			OR c.telegram_id::text = ?)`, q)
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM payments p JOIN customers c ON c.id=p.customer_id WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := many[PaymentRow](db.Query(ctx, `SELECT p.*,
		COALESCE(NULLIF(trim(concat_ws(' ', c.first_name, c.last_name)),''), '@'||c.username, c.email::text) AS customer_name,
		pl.name AS plan_name
		FROM payments p JOIN customers c ON c.id=p.customer_id LEFT JOIN plans pl ON pl.id=p.plan_id
		WHERE `+cond+` ORDER BY p.created_at DESC LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)), args...))
	return rows, total, err
}

// PaidPurchases counts successful payments of a customer (used for "first purchase" logic).
func PaidPurchases(ctx context.Context, db DB, customerID int64) (int, error) {
	var n int
	err := db.QueryRow(ctx, `SELECT count(*) FROM payments WHERE customer_id=$1 AND status='succeeded' AND applied_at IS NOT NULL`, customerID).Scan(&n)
	return n, err
}
