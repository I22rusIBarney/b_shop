-- Custom names that clients give to their devices (HWID) in the cabinet.
CREATE TABLE device_names (
    subscription_id BIGINT NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    hwid            TEXT   NOT NULL,
    name            TEXT   NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (subscription_id, hwid)
);
