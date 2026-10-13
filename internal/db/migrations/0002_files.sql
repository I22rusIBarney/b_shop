-- Uploaded media (logos, backgrounds, icons). Stored in the database so backups include them.
CREATE TABLE files (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text        NOT NULL,
    mime       text        NOT NULL,
    size       int         NOT NULL,
    data       bytea       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
