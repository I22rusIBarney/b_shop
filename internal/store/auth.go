package store

import (
	"context"
	"crypto/sha256"
	"time"
)

type Identity struct {
	ID         int64      `db:"id" json:"id"`
	CustomerID int64      `db:"customer_id" json:"-"`
	Provider   string     `db:"provider" json:"provider"`
	Subject    string     `db:"subject" json:"-"`
	Display    string     `db:"display" json:"display"`
	CreatedAt  time.Time  `db:"created_at" json:"created_at"`
	LastLogin  *time.Time `db:"last_login" json:"last_login"`
}

func FindIdentity(ctx context.Context, db DB, provider, subject string) (*Identity, error) {
	return one[Identity](db.Query(ctx, `SELECT * FROM auth_identities WHERE provider=$1 AND subject=$2`, provider, subject))
}

func ListIdentities(ctx context.Context, db DB, customerID int64) ([]Identity, error) {
	return many[Identity](db.Query(ctx, `SELECT * FROM auth_identities WHERE customer_id=$1 ORDER BY id`, customerID))
}

func AddIdentity(ctx context.Context, db DB, customerID int64, provider, subject, display string) error {
	_, err := db.Exec(ctx, `INSERT INTO auth_identities (customer_id, provider, subject, display, last_login)
		VALUES ($1,$2,$3,$4, now())
		ON CONFLICT (provider, subject) DO UPDATE SET display=EXCLUDED.display, last_login=now()`,
		customerID, provider, subject, display)
	return err
}

func TouchIdentity(ctx context.Context, db DB, id int64, display string) error {
	_, err := db.Exec(ctx, `UPDATE auth_identities SET last_login=now(), display=COALESCE(NULLIF($2,''), display) WHERE id=$1`, id, display)
	return err
}

func DeleteIdentity(ctx context.Context, db DB, customerID, id int64) (int64, error) {
	tag, err := db.Exec(ctx, `DELETE FROM auth_identities WHERE id=$1 AND customer_id=$2`, id, customerID)
	return tag.RowsAffected(), err
}

// MoveIdentities reassigns logins and data of `from` to `to` (account merge).
func MoveIdentities(ctx context.Context, db DB, from, to int64) error {
	stmts := []string{
		`UPDATE auth_identities SET customer_id=$2 WHERE customer_id=$1`,
		`UPDATE subscriptions SET customer_id=$2, is_primary=false WHERE customer_id=$1`,
		`UPDATE payments SET customer_id=$2 WHERE customer_id=$1`,
		`UPDATE payment_methods SET customer_id=$2 WHERE customer_id=$1`,
		`UPDATE customer_events SET customer_id=$2 WHERE customer_id=$1`,
		`UPDATE customers SET referrer_id=$2 WHERE referrer_id=$1 AND id<>$2`,
		`DELETE FROM sessions WHERE customer_id=$1`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(ctx, q, from, to); err != nil {
			return err
		}
	}
	return nil
}

func HashToken(tok string) []byte {
	h := sha256.Sum256([]byte(tok))
	return h[:]
}

func CreateSession(ctx context.Context, db DB, token string, customerID int64, provider, ua, ip string, ttl time.Duration) error {
	if len(ua) > 300 {
		ua = ua[:300]
	}
	_, err := db.Exec(ctx, `INSERT INTO sessions (token_hash, customer_id, provider, user_agent, ip, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, HashToken(token), customerID, provider, ua, ip, time.Now().Add(ttl))
	return err
}

// SessionCustomer returns the customer for a valid session token.
func SessionCustomer(ctx context.Context, db DB, token string) (*Customer, error) {
	return one[Customer](db.Query(ctx, `SELECT `+prefixCols("c.", customerCols)+` FROM sessions s
		JOIN customers c ON c.id = s.customer_id WHERE s.token_hash=$1 AND s.expires_at > now()`, HashToken(token)))
}

func DeleteSession(ctx context.Context, db DB, token string) error {
	_, err := db.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, HashToken(token))
	return err
}

func PurgeExpired(ctx context.Context, db DB) error {
	_, err := db.Exec(ctx, `
		DELETE FROM sessions WHERE expires_at < now();
		DELETE FROM email_codes WHERE expires_at < now() - interval '1 day';
		DELETE FROM oauth_states WHERE created_at < now() - interval '1 hour';`)
	return err
}

type EmailCode struct {
	Email     string    `db:"email"`
	CodeHash  []byte    `db:"code_hash"`
	Attempts  int       `db:"attempts"`
	ExpiresAt time.Time `db:"expires_at"`
	SentAt    time.Time `db:"sent_at"`
}

func GetEmailCode(ctx context.Context, db DB, email string) (*EmailCode, error) {
	return one[EmailCode](db.Query(ctx, `SELECT * FROM email_codes WHERE email=$1`, email))
}

func PutEmailCode(ctx context.Context, db DB, email string, hash []byte, ttl time.Duration) error {
	_, err := db.Exec(ctx, `INSERT INTO email_codes (email, code_hash, attempts, expires_at, sent_at)
		VALUES ($1,$2,0,$3,now()) ON CONFLICT (email) DO UPDATE
		SET code_hash=EXCLUDED.code_hash, attempts=0, expires_at=EXCLUDED.expires_at, sent_at=now()`,
		email, hash, time.Now().Add(ttl))
	return err
}

func BumpEmailAttempts(ctx context.Context, db DB, email string) error {
	_, err := db.Exec(ctx, `UPDATE email_codes SET attempts=attempts+1 WHERE email=$1`, email)
	return err
}

func DeleteEmailCode(ctx context.Context, db DB, email string) error {
	_, err := db.Exec(ctx, `DELETE FROM email_codes WHERE email=$1`, email)
	return err
}

type OAuthState struct {
	State     string    `db:"state"`
	Provider  string    `db:"provider"`
	Verifier  string    `db:"verifier"`
	LinkTo    *int64    `db:"link_to"`
	ReturnTo  string    `db:"return_to"`
	CreatedAt time.Time `db:"created_at"`
}

func PutOAuthState(ctx context.Context, db DB, st OAuthState) error {
	_, err := db.Exec(ctx, `INSERT INTO oauth_states (state, provider, verifier, link_to, return_to) VALUES ($1,$2,$3,$4,$5)`,
		st.State, st.Provider, st.Verifier, st.LinkTo, st.ReturnTo)
	return err
}

// TakeOAuthState returns and deletes a state created within the last 15 minutes.
func TakeOAuthState(ctx context.Context, db DB, state string) (*OAuthState, error) {
	return one[OAuthState](db.Query(ctx, `DELETE FROM oauth_states WHERE state=$1 AND created_at > now() - interval '15 minutes' RETURNING *`, state))
}
