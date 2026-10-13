-- Geography of subscription requests: every time a client's app fetches the subscription link the panel
-- records the IP; the shop copies those records and resolves country/city once, on arrival.
CREATE TABLE sub_requests (
    panel_id        bigint      PRIMARY KEY,                 -- id of the record in the panel's request history
    customer_id     bigint      NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    subscription_id bigint      REFERENCES subscriptions (id) ON DELETE SET NULL,
    ip              inet        NOT NULL,
    country         text        NOT NULL DEFAULT '',         -- ISO alpha-2, '' when unknown
    region          text        NOT NULL DEFAULT '',
    city            text        NOT NULL DEFAULT '',
    lat             real,
    lon             real,
    user_agent      text        NOT NULL DEFAULT '',
    requested_at    timestamptz NOT NULL
);
CREATE INDEX sub_requests_time_idx ON sub_requests (requested_at DESC);
CREATE INDEX sub_requests_customer_idx ON sub_requests (customer_id, requested_at DESC);
