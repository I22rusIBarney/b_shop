-- Banners above the subscription card in the cabinet. System banners (kind <> '') have fixed logic and can only be
-- switched on/off and reworded; custom ones are created in the admin panel.
CREATE TABLE banners (
    id          bigserial PRIMARY KEY,
    kind        text        NOT NULL DEFAULT '' CHECK (kind IN ('', 'link_email', 'link_telegram')),
    title       text        NOT NULL,
    subtitle    text        NOT NULL DEFAULT '',
    icon        text        NOT NULL DEFAULT 'info',
    segment     text        NOT NULL DEFAULT 'all' CHECK (segment IN ('all', 'active', 'trial', 'expired', 'no_sub')),
    button_text text        NOT NULL DEFAULT '',
    button_url  text        NOT NULL DEFAULT '',
    starts_at   timestamptz,
    ends_at     timestamptz,
    is_active   boolean     NOT NULL DEFAULT true,
    dismissible boolean     NOT NULL DEFAULT true,
    sort        int         NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX banners_kind_uniq ON banners (kind) WHERE kind <> '';

CREATE TABLE banner_dismissals (
    banner_id   bigint      NOT NULL REFERENCES banners (id) ON DELETE CASCADE,
    customer_id bigint      NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (banner_id, customer_id)
);

INSERT INTO banners (kind, title, subtitle, icon, button_text, sort) VALUES
    ('link_email', 'Привяжите email', 'Чтобы войти в кабинет, если Telegram недоступен, и получать чеки на почту', 'mail', 'Привязать', 0),
    ('link_telegram', 'Привяжите Telegram', 'Уведомления о подписке и поддержка прямо в боте', 'telegram', 'Привязать', 1);
