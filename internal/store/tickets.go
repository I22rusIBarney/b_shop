package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Ticket struct {
	ID              int64      `db:"id" json:"id"`
	CustomerID      int64      `db:"customer_id" json:"customer_id"`
	Subject         string     `db:"subject" json:"subject"`
	Status          string     `db:"status" json:"status"`
	AssigneeID      *int64     `db:"assignee_id" json:"assignee_id"`
	Tag             string     `db:"tag" json:"tag"`
	Source          string     `db:"source" json:"source"`
	AIEnabled       bool       `db:"ai_enabled" json:"ai_enabled"`
	AIDueAt         *time.Time `db:"ai_due_at" json:"-"`
	AIWindowStart   *time.Time `db:"ai_window_start" json:"-"`
	AIWindowCalls   int        `db:"ai_window_calls" json:"-"`
	Rating          *int       `db:"rating" json:"rating"`
	RatingComment   string     `db:"rating_comment" json:"rating_comment"`
	RatingAskedAt   *time.Time `db:"rating_asked_at" json:"-"`
	UnreadAdmin     bool       `db:"unread_admin" json:"unread_admin"`
	UnreadCustomer  bool       `db:"unread_customer" json:"unread_customer"`
	CreatedAt       time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at" json:"updated_at"`
	LastCustomerAt  time.Time  `db:"last_customer_at" json:"last_customer_at"`
	FirstResponseAt *time.Time `db:"first_response_at" json:"first_response_at"`
	ResolvedAt      *time.Time `db:"resolved_at" json:"resolved_at"`
	ClosedAt        *time.Time `db:"closed_at" json:"closed_at"`
	KBScannedAt     *time.Time `db:"kb_scanned_at" json:"-"`
}

type TicketMessage struct {
	ID          int64       `db:"id" json:"id"`
	TicketID    int64       `db:"ticket_id" json:"ticket_id"`
	Author      string      `db:"author" json:"author"`
	AuthorID    *int64      `db:"author_id" json:"author_id"`
	Body        string      `db:"body" json:"body"`
	Attachments []uuid.UUID `db:"attachments" json:"attachments"`
	ReplyTo     *int64      `db:"reply_to" json:"reply_to"`
	Confidence  *float32    `db:"confidence" json:"confidence"`
	DraftStatus string      `db:"draft_status" json:"draft_status"`
	NotifyAt    *time.Time  `db:"notify_at" json:"-"`
	CreatedAt   time.Time   `db:"created_at" json:"created_at"`
}

func CreateTicket(ctx context.Context, db DB, t *Ticket) error {
	return db.QueryRow(ctx, `INSERT INTO tickets (customer_id, subject, source, ai_enabled) VALUES ($1,$2,$3,$4)
		RETURNING id, status, created_at, updated_at, last_customer_at, unread_admin`,
		t.CustomerID, t.Subject, t.Source, t.AIEnabled).Scan(&t.ID, &t.Status, &t.CreatedAt, &t.UpdatedAt, &t.LastCustomerAt, &t.UnreadAdmin)
}

func GetTicket(ctx context.Context, db DB, id int64) (*Ticket, error) {
	return one[Ticket](db.Query(ctx, `SELECT * FROM tickets WHERE id=$1`, id))
}

func GetTicketForUpdate(ctx context.Context, db DB, id int64) (*Ticket, error) {
	return one[Ticket](db.Query(ctx, `SELECT * FROM tickets WHERE id=$1 FOR UPDATE`, id))
}

func CountOpenTickets(ctx context.Context, db DB, customerID int64) (int, error) {
	var n int
	err := db.QueryRow(ctx, `SELECT count(*) FROM tickets WHERE customer_id=$1 AND status IN ('open','waiting')`, customerID).Scan(&n)
	return n, err
}

// LatestOpenTicket is the customer's most recent not-closed ticket (for messages written to the bot).
func LatestOpenTicket(ctx context.Context, db DB, customerID int64) (*Ticket, error) {
	return one[Ticket](db.Query(ctx, `SELECT * FROM tickets WHERE customer_id=$1 AND status <> 'closed' ORDER BY updated_at DESC LIMIT 1`, customerID))
}

func CustomerTickets(ctx context.Context, db DB, customerID int64) ([]Ticket, error) {
	return many[Ticket](db.Query(ctx, `SELECT * FROM tickets WHERE customer_id=$1 ORDER BY (status='closed'), updated_at DESC LIMIT 100`, customerID))
}

type TicketRow struct {
	Ticket
	CustomerName string  `db:"customer_name" json:"customer_name"`
	Username     *string `db:"username" json:"username"`
	TelegramID   *int64  `db:"telegram_id" json:"telegram_id"`
	LastBody     string  `db:"last_body" json:"last_body"`
	LastAuthor   string  `db:"last_author" json:"last_author"`
	PendingDraft bool    `db:"pending_draft" json:"pending_draft"`
}

type TicketFilter struct {
	Status   string // open | waiting | resolved | closed | active (open+waiting+resolved) | all
	Search   string
	Customer int64
	Limit    int
	Offset   int
}

func ListTickets(ctx context.Context, db DB, f TicketFilter) ([]TicketRow, int, error) {
	where := `WHERE ($1 = 'all' OR ($1 = 'active' AND t.status <> 'closed') OR t.status = $1)
		AND ($2 = '' OR t.subject ILIKE '%'||$2||'%' OR t.id::text = $2 OR c.username ILIKE '%'||$2||'%'
			OR c.first_name ILIKE '%'||$2||'%' OR c.email ILIKE '%'||$2||'%' OR c.telegram_id::text = $2)
		AND ($3 = 0 OR t.customer_id = $3)`
	if f.Status == "" {
		f.Status = "active"
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	var total int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM tickets t JOIN customers c ON c.id=t.customer_id `+where,
		f.Status, f.Search, f.Customer).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := many[TicketRow](db.Query(ctx, `SELECT t.*,
			COALESCE(NULLIF(trim(COALESCE(c.first_name,'')||' '||COALESCE(c.last_name,'')),''), c.username, c.email, 'Клиент') AS customer_name,
			c.username, c.telegram_id,
			COALESCE(m.body,'') AS last_body, COALESCE(m.author,'') AS last_author,
			EXISTS (SELECT 1 FROM ticket_messages d WHERE d.ticket_id=t.id AND d.author='draft' AND d.draft_status='pending') AS pending_draft
		FROM tickets t JOIN customers c ON c.id=t.customer_id
		LEFT JOIN LATERAL (SELECT body, author FROM ticket_messages WHERE ticket_id=t.id AND author IN ('customer','operator','ai')
			ORDER BY id DESC LIMIT 1) m ON true
		`+where+` ORDER BY (t.status='open') DESC, t.unread_admin DESC, t.updated_at DESC LIMIT $4 OFFSET $5`,
		f.Status, f.Search, f.Customer, f.Limit, f.Offset))
	return rows, total, err
}

func CountTicketsNeedingReply(ctx context.Context, db DB) (int, error) {
	var n int
	err := db.QueryRow(ctx, `SELECT count(*) FROM tickets WHERE status='open'`).Scan(&n)
	return n, err
}

func InsertTicketMessage(ctx context.Context, db DB, m *TicketMessage) error {
	if m.Attachments == nil {
		m.Attachments = []uuid.UUID{}
	}
	return db.QueryRow(ctx, `INSERT INTO ticket_messages (ticket_id, author, author_id, body, attachments, reply_to, confidence, draft_status, notify_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id, created_at`,
		m.TicketID, m.Author, m.AuthorID, m.Body, m.Attachments, m.ReplyTo, m.Confidence, m.DraftStatus, m.NotifyAt).Scan(&m.ID, &m.CreatedAt)
}

func TicketMessages(ctx context.Context, db DB, ticketID int64, withInternal bool) ([]TicketMessage, error) {
	q := `SELECT * FROM ticket_messages WHERE ticket_id=$1`
	if !withInternal {
		q += ` AND author IN ('customer','operator','ai','system')`
	}
	return many[TicketMessage](db.Query(ctx, q+` ORDER BY id`, ticketID))
}

func GetTicketMessage(ctx context.Context, db DB, id int64) (*TicketMessage, error) {
	return one[TicketMessage](db.Query(ctx, `SELECT * FROM ticket_messages WHERE id=$1`, id))
}

func SetDraftStatus(ctx context.Context, db DB, id int64, status string) error {
	_, err := db.Exec(ctx, `UPDATE ticket_messages SET draft_status=$2 WHERE id=$1 AND author='draft'`, id, status)
	return err
}

// UpdateTicket writes the mutable ticket fields.
func UpdateTicket(ctx context.Context, db DB, t *Ticket) error {
	_, err := db.Exec(ctx, `UPDATE tickets SET subject=$2, status=$3, assignee_id=$4, tag=$5, ai_enabled=$6, ai_due_at=$7,
		ai_window_start=$8, ai_window_calls=$9, rating=$10, rating_comment=$11, rating_asked_at=$12, unread_admin=$13,
		unread_customer=$14, updated_at=now(), last_customer_at=$15, first_response_at=$16, resolved_at=$17, closed_at=$18 WHERE id=$1`,
		t.ID, t.Subject, t.Status, t.AssigneeID, t.Tag, t.AIEnabled, t.AIDueAt, t.AIWindowStart, t.AIWindowCalls, t.Rating,
		t.RatingComment, t.RatingAskedAt, t.UnreadAdmin, t.UnreadCustomer, t.LastCustomerAt, t.FirstResponseAt, t.ResolvedAt, t.ClosedAt)
	return err
}

func TicketsAIDue(ctx context.Context, db DB, limit int) ([]int64, error) {
	rows, err := db.Query(ctx, `UPDATE tickets SET ai_due_at=NULL WHERE id IN (
		SELECT id FROM tickets WHERE ai_due_at <= now() AND ai_enabled AND status IN ('open','waiting') ORDER BY ai_due_at LIMIT $1
		FOR UPDATE SKIP LOCKED) RETURNING id`, limit)
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

// PendingReplyNotices returns replies whose notice is due, unless the customer wrote after them.
type ReplyNotice struct {
	MessageID  int64  `db:"id"`
	TicketID   int64  `db:"ticket_id"`
	CustomerID int64  `db:"customer_id"`
	Body       string `db:"body"`
	Stale      bool   `db:"stale"`
}

func DueReplyNotices(ctx context.Context, db DB, limit int) ([]ReplyNotice, error) {
	return many[ReplyNotice](db.Query(ctx, `UPDATE ticket_messages m SET notify_at=NULL FROM tickets t
		WHERE m.id IN (SELECT id FROM ticket_messages WHERE notify_at <= now() ORDER BY notify_at LIMIT $1 FOR UPDATE SKIP LOCKED)
		AND t.id=m.ticket_id
		RETURNING m.id, m.ticket_id, t.customer_id, m.body, (t.last_customer_at > m.created_at) AS stale`, limit))
}

// AutocloseTickets closes waiting/resolved tickets after the configured idle time and returns them.
func AutocloseTickets(ctx context.Context, db DB, waiting, resolved time.Duration) ([]Ticket, error) {
	return many[Ticket](db.Query(ctx, `UPDATE tickets SET status='closed', closed_at=now(), updated_at=now(), ai_due_at=NULL
		WHERE ($1 > 0 AND status='waiting' AND updated_at < now() - make_interval(secs => $1))
		   OR ($2 > 0 AND status='resolved' AND COALESCE(resolved_at, updated_at) < now() - make_interval(secs => $2))
		RETURNING *`, waiting.Seconds(), resolved.Seconds()))
}

func TicketsToRate(ctx context.Context, db DB, delay time.Duration) ([]Ticket, error) {
	return many[Ticket](db.Query(ctx, `UPDATE tickets SET rating_asked_at=now() WHERE id IN (
		SELECT id FROM tickets WHERE status='closed' AND rating IS NULL AND rating_asked_at IS NULL AND first_response_at IS NOT NULL
		AND closed_at < now() - make_interval(secs => $1) AND closed_at > now() - interval '3 days' LIMIT 50)
		RETURNING *`, delay.Seconds()))
}

// ---- templates, FAQ, knowledge ----

type SupportTemplate struct {
	ID       int64  `db:"id" json:"id"`
	Category string `db:"category" json:"category"`
	Title    string `db:"title" json:"title"`
	Body     string `db:"body" json:"body"`
	Sort     int    `db:"sort" json:"sort"`
}

func ListTemplates(ctx context.Context, db DB) ([]SupportTemplate, error) {
	return many[SupportTemplate](db.Query(ctx, `SELECT * FROM support_templates ORDER BY category, sort, id`))
}

func SaveTemplate(ctx context.Context, db DB, t *SupportTemplate) error {
	if t.ID == 0 {
		return db.QueryRow(ctx, `INSERT INTO support_templates (category, title, body, sort) VALUES ($1,$2,$3,$4) RETURNING id`,
			t.Category, t.Title, t.Body, t.Sort).Scan(&t.ID)
	}
	_, err := db.Exec(ctx, `UPDATE support_templates SET category=$2, title=$3, body=$4, sort=$5 WHERE id=$1`, t.ID, t.Category, t.Title, t.Body, t.Sort)
	return err
}

func DeleteTemplate(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `DELETE FROM support_templates WHERE id=$1`, id)
	return err
}

type FAQItem struct {
	ID       int64  `db:"id" json:"id"`
	Section  string `db:"section" json:"section"`
	Question string `db:"question" json:"question"`
	Answer   string `db:"answer" json:"answer"`
	IsActive bool   `db:"is_active" json:"is_active"`
	Sort     int    `db:"sort" json:"sort"`
}

func ListFAQ(ctx context.Context, db DB, onlyActive bool) ([]FAQItem, error) {
	return many[FAQItem](db.Query(ctx, `SELECT * FROM faq_items WHERE (NOT $1 OR is_active) ORDER BY sort, id`, onlyActive))
}

func SaveFAQ(ctx context.Context, db DB, f *FAQItem) error {
	if f.ID == 0 {
		return db.QueryRow(ctx, `INSERT INTO faq_items (section, question, answer, is_active, sort)
			VALUES ($1,$2,$3,$4,COALESCE((SELECT max(sort)+1 FROM faq_items),0)) RETURNING id, sort`,
			f.Section, f.Question, f.Answer, f.IsActive).Scan(&f.ID, &f.Sort)
	}
	_, err := db.Exec(ctx, `UPDATE faq_items SET section=$2, question=$3, answer=$4, is_active=$5, sort=$6 WHERE id=$1`,
		f.ID, f.Section, f.Question, f.Answer, f.IsActive, f.Sort)
	return err
}

func DeleteFAQ(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `DELETE FROM faq_items WHERE id=$1`, id)
	return err
}

type KBArticle struct {
	ID        int64     `db:"id" json:"id"`
	Title     string    `db:"title" json:"title"`
	Body      string    `db:"body" json:"body"`
	IsActive  bool      `db:"is_active" json:"is_active"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

func ListKB(ctx context.Context, db DB, onlyActive bool) ([]KBArticle, error) {
	return many[KBArticle](db.Query(ctx, `SELECT * FROM kb_articles WHERE (NOT $1 OR is_active) ORDER BY title, id`, onlyActive))
}

func SaveKB(ctx context.Context, db DB, a *KBArticle) error {
	if a.ID == 0 {
		return db.QueryRow(ctx, `INSERT INTO kb_articles (title, body, is_active) VALUES ($1,$2,$3) RETURNING id, updated_at`,
			a.Title, a.Body, a.IsActive).Scan(&a.ID, &a.UpdatedAt)
	}
	_, err := db.Exec(ctx, `UPDATE kb_articles SET title=$2, body=$3, is_active=$4, updated_at=now() WHERE id=$1`, a.ID, a.Title, a.Body, a.IsActive)
	return err
}

func DeleteKB(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `DELETE FROM kb_articles WHERE id=$1`, id)
	return err
}

// PutOwnedFile stores a customer upload.
func PutOwnedFile(ctx context.Context, db DB, owner int64, name, mime string, data []byte) (*File, error) {
	return one[File](db.Query(ctx, `INSERT INTO files (name, mime, size, data, owner_id) VALUES ($1,$2,$3,$4,$5)
		RETURNING id, name, mime, size, ''::bytea AS data, created_at`, name, mime, len(data), data, owner))
}

// OwnedFiles keeps only the ids of files uploaded by this customer.
func OwnedFiles(ctx context.Context, db DB, owner int64, ids []uuid.UUID) ([]uuid.UUID, error) {
	rows, err := db.Query(ctx, `SELECT id FROM files WHERE owner_id=$1 AND id = ANY($2)`, owner, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
