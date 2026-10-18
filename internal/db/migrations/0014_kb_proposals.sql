-- Knowledge-base articles proposed by the assistant from closed tickets (customer question + operator answers).
-- An admin approves (→ kb_articles) or rejects them.
CREATE TABLE kb_proposals (
    id          bigserial PRIMARY KEY,
    ticket_id   bigint      REFERENCES tickets (id) ON DELETE SET NULL,
    title       text        NOT NULL,
    body        text        NOT NULL,
    status      text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    article_id  bigint      REFERENCES kb_articles (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    decided_at  timestamptz
);
CREATE INDEX kb_proposals_status_idx ON kb_proposals (status, created_at DESC);

-- Tickets already read by the proposal generator (whether or not they produced an article).
ALTER TABLE tickets ADD COLUMN kb_scanned_at timestamptz;
