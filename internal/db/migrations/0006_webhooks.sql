-- Incoming Remnawave webhooks: journal for the admin page and de-duplication of retries.
CREATE TABLE webhook_events (
    id          bigserial PRIMARY KEY,
    source      text        NOT NULL DEFAULT 'remnawave',
    event       text        NOT NULL,
    body_hash   bytea       NOT NULL,
    customer_id bigint REFERENCES customers (id) ON DELETE SET NULL,
    summary     text        NOT NULL DEFAULT '',
    result      text        NOT NULL DEFAULT '',  -- what we did: "клиенту", "админам", "выключено", "клиент не найден"…
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX webhook_events_dedup_idx ON webhook_events (source, body_hash);
CREATE INDEX webhook_events_created_idx ON webhook_events (created_at DESC);
