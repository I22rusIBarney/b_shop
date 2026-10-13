-- Stage 3: promo codes, referral bonuses, plan change / extra devices bookkeeping, autopay.

CREATE TABLE promo_codes (
    id                bigserial PRIMARY KEY,
    code              citext      NOT NULL UNIQUE,
    -- discount: percent off the next purchase/renewal; days: bonus days to a subscription right away.
    kind              text        NOT NULL CHECK (kind IN ('discount', 'days')),
    value             int         NOT NULL CHECK (value > 0),
    folder            text        NOT NULL DEFAULT '',
    description       text        NOT NULL DEFAULT '',
    is_active         boolean     NOT NULL DEFAULT true,
    max_uses          int         NOT NULL DEFAULT 0,      -- 0 = unlimited
    valid_from        timestamptz,
    valid_until       timestamptz,
    plan_ids          bigint[]    NOT NULL DEFAULT '{}',   -- empty = any plan
    new_customers_only boolean    NOT NULL DEFAULT false,  -- only before the first paid purchase
    customer_id       bigint REFERENCES customers (id) ON DELETE CASCADE, -- personal code
    -- A reserved discount is dropped if not used within this many hours (0 = never).
    reserve_hours     int         NOT NULL DEFAULT 0,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX promo_codes_folder_idx ON promo_codes (folder);

-- One redemption per customer per code. A discount is 'reserved' until the payment it was used in succeeds.
CREATE TABLE promo_redemptions (
    id              bigserial PRIMARY KEY,
    promo_id        bigint      NOT NULL REFERENCES promo_codes (id) ON DELETE CASCADE,
    customer_id     bigint      NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    status          text        NOT NULL CHECK (status IN ('reserved', 'used')),
    payment_id      uuid REFERENCES payments (id) ON DELETE SET NULL,
    subscription_id bigint REFERENCES subscriptions (id) ON DELETE SET NULL,
    days            int         NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    used_at         timestamptz,
    UNIQUE (promo_id, customer_id)
);
CREATE INDEX promo_redemptions_customer_idx ON promo_redemptions (customer_id, status);

-- Bonus days the customer could not receive yet (no subscription); added to the next subscription they get.
ALTER TABLE customers ADD COLUMN bonus_days int NOT NULL DEFAULT 0;

CREATE TABLE referral_bonuses (
    id              bigserial PRIMARY KEY,
    recipient_id    bigint      NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    referral_id     bigint REFERENCES customers (id) ON DELETE SET NULL,
    payment_id      uuid REFERENCES payments (id) ON DELETE SET NULL,
    kind            text        NOT NULL CHECK (kind IN ('first', 'recurring', 'invitee', 'manual')),
    days            int         NOT NULL,
    subscription_id bigint REFERENCES subscriptions (id) ON DELETE SET NULL,
    pending         boolean     NOT NULL DEFAULT false, -- went to bonus_days balance
    actor_id        bigint,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX referral_bonuses_once_idx ON referral_bonuses (payment_id, recipient_id, kind) WHERE payment_id IS NOT NULL;
CREATE INDEX referral_bonuses_recipient_idx ON referral_bonuses (recipient_id, created_at DESC);
CREATE INDEX referral_bonuses_created_idx ON referral_bonuses (created_at);

ALTER TABLE payments
    ADD COLUMN referral_done boolean NOT NULL DEFAULT true,
    ADD COLUMN from_plan_id  bigint REFERENCES plans (id) ON DELETE SET NULL,
    ADD COLUMN autopay       boolean NOT NULL DEFAULT false,
    ADD COLUMN payment_method_id bigint REFERENCES payment_methods (id) ON DELETE SET NULL; -- method saved by this payment
-- New payments go through referral processing after they are applied.
ALTER TABLE payments ALTER COLUMN referral_done SET DEFAULT false;
CREATE INDEX payments_referral_idx ON payments (applied_at) WHERE referral_done = false AND applied_at IS NOT NULL;

ALTER TABLE subscriptions
    ADD COLUMN autopay            boolean NOT NULL DEFAULT false,
    ADD COLUMN autopay_method_id  bigint REFERENCES payment_methods (id) ON DELETE SET NULL,
    ADD COLUMN autopay_period_id  bigint REFERENCES plan_periods (id) ON DELETE SET NULL,
    ADD COLUMN autopay_notified_for timestamptz, -- expires_at value the "will charge soon" notice was sent for
    ADD COLUMN autopay_last_try   timestamptz;

CREATE TABLE plan_changes (
    id              bigserial PRIMARY KEY,
    customer_id     bigint      NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    subscription_id bigint REFERENCES subscriptions (id) ON DELETE SET NULL,
    from_plan_id    bigint REFERENCES plans (id) ON DELETE SET NULL,
    to_plan_id      bigint REFERENCES plans (id) ON DELETE SET NULL,
    mode            text        NOT NULL CHECK (mode IN ('upgrade', 'convert', 'downgrade', 'admin')),
    days_before     int         NOT NULL,
    days_after      int         NOT NULL,
    payment_id      uuid REFERENCES payments (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX plan_changes_customer_idx ON plan_changes (customer_id, created_at DESC);
