-- Telegram profile photos for the admin panel, fetched by the bot and cached (empty data = the user has no photo).
CREATE TABLE customer_avatars (
    customer_id bigint      PRIMARY KEY REFERENCES customers (id) ON DELETE CASCADE,
    data        bytea       NOT NULL DEFAULT '',
    fetched_at  timestamptz NOT NULL DEFAULT now()
);
