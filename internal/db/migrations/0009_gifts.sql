-- Gift subscriptions (paid for by one customer, activated by anyone with the code)
-- and purchases made for another customer (recipient gets the subscription).
CREATE TABLE gifts (
    id              bigserial PRIMARY KEY,
    code            text        NOT NULL UNIQUE,
    buyer_id        bigint      NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    payment_id      uuid        UNIQUE REFERENCES payments (id) ON DELETE SET NULL,
    plan_id         bigint      REFERENCES plans (id) ON DELETE SET NULL,
    title           text        NOT NULL DEFAULT '',
    days            int         NOT NULL,
    status          text        NOT NULL DEFAULT 'ready' CHECK (status IN ('ready', 'redeemed')),
    redeemed_by     bigint      REFERENCES customers (id) ON DELETE SET NULL,
    subscription_id bigint      REFERENCES subscriptions (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    redeemed_at     timestamptz
);
CREATE INDEX gifts_buyer_idx ON gifts (buyer_id, created_at DESC);

ALTER TABLE payments ADD COLUMN recipient_id bigint REFERENCES customers (id) ON DELETE SET NULL;
ALTER TABLE payments DROP CONSTRAINT payments_operation_check;
ALTER TABLE payments ADD CONSTRAINT payments_operation_check
    CHECK (operation IN ('buy', 'renew', 'change', 'devices', 'traffic', 'autopay', 'gift', 'buy_for'));
