-- Stage 4: support tickets with the AI first line, broadcasts (news feed), automations.

-- ---- support ----

CREATE TABLE tickets (
    id                bigserial PRIMARY KEY,
    customer_id       bigint      NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    subject           text        NOT NULL DEFAULT '',
    -- open: waiting for support; waiting: waiting for the customer; resolved: solved, closes by timer; closed.
    status            text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'waiting', 'resolved', 'closed')),
    assignee_id       bigint REFERENCES customers (id) ON DELETE SET NULL,
    tag               text        NOT NULL DEFAULT '',
    source            text        NOT NULL DEFAULT 'web', -- web | telegram
    -- AI: off for this ticket after escalation or when an operator takes over.
    ai_enabled        boolean     NOT NULL DEFAULT true,
    ai_due_at         timestamptz,          -- debounce: run the assistant at this time
    ai_window_start   timestamptz,
    ai_window_calls   int         NOT NULL DEFAULT 0,
    rating            int CHECK (rating BETWEEN 1 AND 5),
    rating_comment    text        NOT NULL DEFAULT '',
    rating_asked_at   timestamptz,
    unread_admin      boolean     NOT NULL DEFAULT true,
    unread_customer   boolean     NOT NULL DEFAULT false,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    last_customer_at  timestamptz NOT NULL DEFAULT now(),
    first_response_at timestamptz,
    resolved_at       timestamptz,
    closed_at         timestamptz
);
CREATE INDEX tickets_customer_idx ON tickets (customer_id, updated_at DESC);
CREATE INDEX tickets_status_idx ON tickets (status, updated_at DESC);
CREATE INDEX tickets_ai_due_idx ON tickets (ai_due_at) WHERE ai_due_at IS NOT NULL;

CREATE TABLE ticket_messages (
    id           bigserial PRIMARY KEY,
    ticket_id    bigint      NOT NULL REFERENCES tickets (id) ON DELETE CASCADE,
    -- customer | operator | ai | system | note (internal) | draft (AI draft waiting for an operator)
    author       text        NOT NULL CHECK (author IN ('customer', 'operator', 'ai', 'system', 'note', 'draft')),
    author_id    bigint REFERENCES customers (id) ON DELETE SET NULL,
    body         text        NOT NULL DEFAULT '',
    attachments  uuid[]      NOT NULL DEFAULT '{}',
    reply_to     bigint REFERENCES ticket_messages (id) ON DELETE SET NULL,
    confidence   real,
    draft_status text        NOT NULL DEFAULT '' CHECK (draft_status IN ('', 'pending', 'sent', 'rejected')),
    -- When to tell the customer about this reply (Telegram right away, email after a delay); null = nothing to send.
    notify_at    timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ticket_messages_ticket_idx ON ticket_messages (ticket_id, id);
CREATE INDEX ticket_messages_notify_idx ON ticket_messages (notify_at) WHERE notify_at IS NOT NULL;

CREATE TABLE support_templates (
    id         bigserial PRIMARY KEY,
    category   text   NOT NULL DEFAULT '',
    title      text   NOT NULL,
    body       text   NOT NULL,
    sort       int    NOT NULL DEFAULT 0
);

-- FAQ shown to customers before they open a ticket; also given to the assistant.
CREATE TABLE faq_items (
    id        bigserial PRIMARY KEY,
    section   text    NOT NULL DEFAULT '',
    question  text    NOT NULL,
    answer    text    NOT NULL,
    is_active boolean NOT NULL DEFAULT true,
    sort      int     NOT NULL DEFAULT 0
);

-- Knowledge base for the assistant only (not shown to customers).
CREATE TABLE kb_articles (
    id         bigserial PRIMARY KEY,
    title      text        NOT NULL,
    body       text        NOT NULL,
    is_active  boolean     NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Files uploaded by customers to tickets belong to them; admins' uploads have no owner.
ALTER TABLE files ADD COLUMN owner_id bigint REFERENCES customers (id) ON DELETE CASCADE;

-- ---- broadcasts / news ----

CREATE TABLE broadcasts (
    id            bigserial PRIMARY KEY,
    title         text        NOT NULL DEFAULT '',
    body          text        NOT NULL DEFAULT '',     -- Telegram HTML subset
    image         text        NOT NULL DEFAULT '',     -- URL (/files/... or external)
    buttons       jsonb       NOT NULL DEFAULT '[]',  -- [{text,url}]
    audience      jsonb       NOT NULL DEFAULT '{}',  -- {segment, plan_id}
    channel       text        NOT NULL DEFAULT 'telegram' CHECK (channel IN ('telegram', 'email', 'all')),
    show_in_news  boolean     NOT NULL DEFAULT true,
    status        text        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'scheduled', 'sending', 'done', 'canceled')),
    scheduled_at  timestamptz,
    cursor_id     bigint      NOT NULL DEFAULT 0,      -- last customer id processed (resumable)
    total         int         NOT NULL DEFAULT 0,
    sent          int         NOT NULL DEFAULT 0,
    failed        int         NOT NULL DEFAULT 0,
    created_by    bigint REFERENCES customers (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    started_at    timestamptz,
    finished_at   timestamptz
);
CREATE INDEX broadcasts_news_idx ON broadcasts (finished_at DESC) WHERE status = 'done' AND show_in_news;

ALTER TABLE customers ADD COLUMN news_seen_at timestamptz;

-- ---- automations ----

CREATE TABLE automations (
    id                bigserial PRIMARY KEY,
    name              text        NOT NULL,
    trigger_type      text        NOT NULL CHECK (trigger_type IN ('event', 'cron')),
    trigger_factor    text        NOT NULL,
    is_active         boolean     NOT NULL DEFAULT true,
    cooldown_hours    int         NOT NULL DEFAULT 0,
    skip_if_active_in text[]      NOT NULL DEFAULT '{}', -- names of other automations
    schedule_mode     text        NOT NULL DEFAULT 'relative',
    loop_count        int         NOT NULL DEFAULT 1,
    shuffle_steps     boolean     NOT NULL DEFAULT false,
    steps             jsonb       NOT NULL DEFAULT '[]',
    sort              int         NOT NULL DEFAULT 0,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE automation_runs (
    id            bigserial PRIMARY KEY,
    automation_id bigint      NOT NULL REFERENCES automations (id) ON DELETE CASCADE,
    customer_id   bigint      NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    status        text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'done', 'stopped')),
    stop_reason   text        NOT NULL DEFAULT '',  -- purchase | admin | condition | error
    position      int         NOT NULL DEFAULT 0,   -- index into the planned sequence
    plan          jsonb       NOT NULL DEFAULT '[]', -- [step index, ...] incl. repeats, loops, shuffle
    context       jsonb       NOT NULL DEFAULT '{}', -- plan_name, subscription_id, payment_id...
    next_at       timestamptz,
    sent          int         NOT NULL DEFAULT 0,
    converted     boolean     NOT NULL DEFAULT false,
    started_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz
);
CREATE UNIQUE INDEX automation_runs_one_active_idx ON automation_runs (automation_id, customer_id) WHERE status = 'active';
CREATE INDEX automation_runs_due_idx ON automation_runs (next_at) WHERE status = 'active';
CREATE INDEX automation_runs_customer_idx ON automation_runs (customer_id, started_at DESC);
CREATE INDEX automation_runs_automation_idx ON automation_runs (automation_id, started_at DESC);

-- Lead temperature (hot/warm/cool/cold/stable) recomputed by the automations worker; a change fires lead_temperature_* scenarios.
ALTER TABLE customers
    ADD COLUMN lead_temperature    text NOT NULL DEFAULT '',
    ADD COLUMN lead_temperature_at timestamptz;
