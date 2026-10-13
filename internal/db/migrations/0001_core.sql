-- Core schema: customers, auth, plans, subscriptions, payments, settings.
-- Money is stored in kopecks (bigint). Times are timestamptz (UTC).

CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE settings (
    key        text PRIMARY KEY,
    value      jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE customers (
    id                bigserial PRIMARY KEY,
    telegram_id       bigint UNIQUE,
    email             citext UNIQUE,
    username          text,
    first_name        text,
    last_name         text,
    language          text        NOT NULL DEFAULT 'ru',
    is_admin          boolean     NOT NULL DEFAULT false,
    is_blocked        boolean     NOT NULL DEFAULT false,
    bot_blocked       boolean     NOT NULL DEFAULT false,
    personal_discount int         NOT NULL DEFAULT 0 CHECK (personal_discount BETWEEN 0 AND 100),
    tag               text        NOT NULL DEFAULT '',
    notes             text        NOT NULL DEFAULT '',
    ref_code          text UNIQUE,
    referrer_id       bigint REFERENCES customers (id) ON DELETE SET NULL,
    trial_used        boolean     NOT NULL DEFAULT false,
    source            text        NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    last_seen_at      timestamptz
);
CREATE INDEX customers_referrer_idx ON customers (referrer_id);
CREATE INDEX customers_created_idx ON customers (created_at);

-- One row per login method linked to a customer (telegram, email, yandex, vk).
CREATE TABLE auth_identities (
    id          bigserial PRIMARY KEY,
    customer_id bigint      NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    provider    text        NOT NULL,
    subject     text        NOT NULL,
    display     text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    last_login  timestamptz,
    UNIQUE (provider, subject)
);
CREATE INDEX auth_identities_customer_idx ON auth_identities (customer_id);

CREATE TABLE sessions (
    token_hash  bytea PRIMARY KEY,
    customer_id bigint      NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    provider    text        NOT NULL,
    user_agent  text        NOT NULL DEFAULT '',
    ip          text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);
CREATE INDEX sessions_customer_idx ON sessions (customer_id);

CREATE TABLE email_codes (
    email      citext PRIMARY KEY,
    code_hash  bytea       NOT NULL,
    attempts   int         NOT NULL DEFAULT 0,
    expires_at timestamptz NOT NULL,
    sent_at    timestamptz NOT NULL DEFAULT now()
);

-- Short-lived state for OAuth redirects (yandex, vk) and account linking.
CREATE TABLE oauth_states (
    state       text PRIMARY KEY,
    provider    text        NOT NULL,
    verifier    text        NOT NULL DEFAULT '',
    link_to     bigint REFERENCES customers (id) ON DELETE CASCADE,
    return_to   text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE plans (
    id                     bigserial PRIMARY KEY,
    name                   text        NOT NULL,
    kind                   text        NOT NULL DEFAULT 'period' CHECK (kind IN ('period', 'traffic')),
    description            text        NOT NULL DEFAULT '',
    features               text[]      NOT NULL DEFAULT '{}',
    sort                   int         NOT NULL DEFAULT 0,
    is_active              boolean     NOT NULL DEFAULT true,
    is_hidden              boolean     NOT NULL DEFAULT false,
    is_default             boolean     NOT NULL DEFAULT false,
    is_trial               boolean     NOT NULL DEFAULT false,
    renewal_disabled       boolean     NOT NULL DEFAULT false,
    change_from_disabled   boolean     NOT NULL DEFAULT false,
    change_to_disabled     boolean     NOT NULL DEFAULT false,
    conversion_disabled    boolean     NOT NULL DEFAULT false,
    required_plan_id       bigint REFERENCES plans (id) ON DELETE SET NULL,
    max_per_customer       int         NOT NULL DEFAULT 0,
    traffic_limit_gb       int         NOT NULL DEFAULT 0,
    device_limit           int         NOT NULL DEFAULT 0,
    extra_devices_allowed  boolean     NOT NULL DEFAULT false,
    extra_device_price     bigint      NOT NULL DEFAULT 0,
    max_extra_devices      int         NOT NULL DEFAULT 0,
    rw_tag                 text        NOT NULL DEFAULT '',
    rw_internal_squads     uuid[]      NOT NULL DEFAULT '{}',
    rw_external_squad      uuid,
    traffic_reset_strategy text        NOT NULL DEFAULT 'MONTH',
    reset_traffic_on_buy   boolean     NOT NULL DEFAULT false,
    trial_days             int         NOT NULL DEFAULT 0,
    created_at             timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE plan_periods (
    id       bigserial PRIMARY KEY,
    plan_id  bigint  NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
    unit     text    NOT NULL DEFAULT 'month' CHECK (unit IN ('day', 'week', 'month')),
    amount   int     NOT NULL CHECK (amount > 0),
    price    bigint  NOT NULL CHECK (price >= 0),
    is_best  boolean NOT NULL DEFAULT false,
    sort     int     NOT NULL DEFAULT 0
);
CREATE INDEX plan_periods_plan_idx ON plan_periods (plan_id);

CREATE TABLE subscriptions (
    id                  bigserial PRIMARY KEY,
    customer_id         bigint      NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    plan_id             bigint REFERENCES plans (id) ON DELETE SET NULL,
    kind                text        NOT NULL DEFAULT 'paid' CHECK (kind IN ('trial', 'paid', 'free')),
    is_primary          boolean     NOT NULL DEFAULT false,
    title               text        NOT NULL DEFAULT '',
    rw_user_id          bigint,
    rw_uuid             uuid,
    rw_short_uuid       text,
    rw_username         text,
    subscription_url    text        NOT NULL DEFAULT '',
    expires_at          timestamptz NOT NULL,
    traffic_limit_bytes bigint      NOT NULL DEFAULT 0,
    device_limit        int         NOT NULL DEFAULT 0,
    extra_devices       int         NOT NULL DEFAULT 0,
    status              text        NOT NULL DEFAULT 'active',
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX subscriptions_customer_idx ON subscriptions (customer_id);
CREATE INDEX subscriptions_expires_idx ON subscriptions (expires_at);
CREATE UNIQUE INDEX subscriptions_rw_user_idx ON subscriptions (rw_user_id) WHERE rw_user_id IS NOT NULL;

-- Saved payment methods for autopay (YooKassa payment_method.id).
CREATE TABLE payment_methods (
    id              bigserial PRIMARY KEY,
    customer_id     bigint      NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    provider        text        NOT NULL,
    provider_ref    text        NOT NULL,
    title           text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_ref)
);

CREATE TABLE payments (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id         bigint      NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    subscription_id     bigint REFERENCES subscriptions (id) ON DELETE SET NULL,
    plan_id             bigint REFERENCES plans (id) ON DELETE SET NULL,
    period_id           bigint REFERENCES plan_periods (id) ON DELETE SET NULL,
    operation           text        NOT NULL CHECK (operation IN ('buy', 'renew', 'change', 'devices', 'traffic', 'autopay')),
    title               text        NOT NULL DEFAULT '',
    days                int         NOT NULL DEFAULT 0,
    months              int         NOT NULL DEFAULT 0,
    extra_devices       int         NOT NULL DEFAULT 0,
    amount              bigint      NOT NULL,
    base_amount         bigint      NOT NULL DEFAULT 0,
    discount_percent    int         NOT NULL DEFAULT 0,
    promo_code          text        NOT NULL DEFAULT '',
    provider            text        NOT NULL,
    provider_ref        text,
    method              text        NOT NULL DEFAULT '',
    save_method         boolean     NOT NULL DEFAULT false,
    status              text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'succeeded', 'canceled', 'refunded')),
    confirmation_url    text        NOT NULL DEFAULT '',
    cancel_reason       text        NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now(),
    paid_at             timestamptz,
    applied_at          timestamptz,
    receipt_status      text        NOT NULL DEFAULT 'none' CHECK (receipt_status IN ('none', 'pending', 'done', 'failed', 'cancel_pending', 'canceled')),
    receipt_uuid        text        NOT NULL DEFAULT '',
    receipt_error       text        NOT NULL DEFAULT '',
    receipt_attempts    int         NOT NULL DEFAULT 0,
    receipt_next_try    timestamptz
);
CREATE INDEX payments_customer_idx ON payments (customer_id, created_at DESC);
CREATE INDEX payments_status_idx ON payments (status, created_at);
CREATE UNIQUE INDEX payments_provider_ref_idx ON payments (provider, provider_ref) WHERE provider_ref IS NOT NULL;
CREATE INDEX payments_receipt_idx ON payments (receipt_status, receipt_next_try) WHERE receipt_status IN ('pending', 'failed', 'cancel_pending');

-- Timeline of customer events (purchases, logins, admin actions...).
CREATE TABLE customer_events (
    id          bigserial PRIMARY KEY,
    customer_id bigint      NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    kind        text        NOT NULL,
    data        jsonb       NOT NULL DEFAULT '{}',
    actor_id    bigint,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX customer_events_customer_idx ON customer_events (customer_id, created_at DESC);
CREATE INDEX customer_events_kind_idx ON customer_events (kind, created_at);
