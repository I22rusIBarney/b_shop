-- Off-site backups sent to the administrators in Telegram (service/backups.go).
CREATE TABLE backup_runs (
    id          bigserial PRIMARY KEY,
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    manual      boolean NOT NULL DEFAULT false,
    ok          boolean NOT NULL DEFAULT false,
    size        bigint NOT NULL DEFAULT 0,
    sent_to     int NOT NULL DEFAULT 0,
    error       text NOT NULL DEFAULT ''
);
CREATE INDEX backup_runs_started ON backup_runs (started_at DESC);
