-- Automatic extra-traffic purchase with a saved payment method when 90% of the limit is used.
ALTER TABLE subscriptions
    ADD COLUMN auto_topup           boolean NOT NULL DEFAULT false,
    ADD COLUMN auto_topup_package   int     NOT NULL DEFAULT 0,
    ADD COLUMN auto_topup_method_id bigint  REFERENCES payment_methods (id) ON DELETE SET NULL,
    ADD COLUMN auto_topup_last_try  timestamptz;
