package store

import (
	"context"
	"time"
)

type KBProposal struct {
	ID        int64      `db:"id" json:"id"`
	TicketID  *int64     `db:"ticket_id" json:"ticket_id"`
	Title     string     `db:"title" json:"title"`
	Body      string     `db:"body" json:"body"`
	Status    string     `db:"status" json:"status"`
	ArticleID *int64     `db:"article_id" json:"article_id"`
	CreatedAt time.Time  `db:"created_at" json:"created_at"`
	DecidedAt *time.Time `db:"decided_at" json:"decided_at"`
}

func ListKBProposals(ctx context.Context, db DB, status string) ([]KBProposal, error) {
	return many[KBProposal](db.Query(ctx, `SELECT * FROM kb_proposals WHERE ($1='' OR status=$1) ORDER BY created_at DESC LIMIT 200`, status))
}

func GetKBProposalForUpdate(ctx context.Context, db DB, id int64) (*KBProposal, error) {
	return one[KBProposal](db.Query(ctx, `SELECT * FROM kb_proposals WHERE id=$1 FOR UPDATE`, id))
}

func InsertKBProposal(ctx context.Context, db DB, p *KBProposal) error {
	return db.QueryRow(ctx, `INSERT INTO kb_proposals (ticket_id, title, body) VALUES ($1,$2,$3) RETURNING id, status, created_at`,
		p.TicketID, p.Title, p.Body).Scan(&p.ID, &p.Status, &p.CreatedAt)
}

func DecideKBProposal(ctx context.Context, db DB, id int64, status string, articleID *int64) error {
	_, err := db.Exec(ctx, `UPDATE kb_proposals SET status=$2, article_id=$3, decided_at=now() WHERE id=$1`, id, status, articleID)
	return err
}

// TicketsForKB lists resolved or closed tickets with at least one operator answer that the generator has not read yet.
func TicketsForKB(ctx context.Context, db DB, limit int) ([]int64, error) {
	rows, err := db.Query(ctx, `SELECT t.id FROM tickets t WHERE t.status IN ('resolved', 'closed') AND t.kb_scanned_at IS NULL
		AND EXISTS (SELECT 1 FROM ticket_messages m WHERE m.ticket_id=t.id AND m.author='operator')
		ORDER BY t.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func MarkTicketKBScanned(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `UPDATE tickets SET kb_scanned_at=now() WHERE id=$1`, id)
	return err
}

// RecentPayments lists the customer's last payments for the assistant (newest first).
func RecentPayments(ctx context.Context, db DB, customerID int64, n int) ([]Payment, error) {
	return many[Payment](db.Query(ctx, `SELECT * FROM payments WHERE customer_id=$1 ORDER BY created_at DESC LIMIT $2`, customerID, n))
}
