-- Per-plan extra traffic and paid traffic reset (both optional, set in the plan editor).
ALTER TABLE plans
    ADD COLUMN traffic_topup_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN traffic_packages      jsonb   NOT NULL DEFAULT '[]',   -- [{"gb":50,"price":9900}], price in kopecks
    ADD COLUMN traffic_reset_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN traffic_reset_price   bigint  NOT NULL DEFAULT 0;      -- 0 = free

-- Bought on top of the plan limit; kept when the plan is re-applied, cleared on renewal or plan change.
ALTER TABLE subscriptions ADD COLUMN extra_traffic_bytes bigint NOT NULL DEFAULT 0;

ALTER TABLE payments ADD COLUMN traffic_gb int NOT NULL DEFAULT 0;
ALTER TABLE payments DROP CONSTRAINT payments_operation_check;
ALTER TABLE payments ADD CONSTRAINT payments_operation_check
    CHECK (operation IN ('buy', 'renew', 'change', 'devices', 'traffic', 'traffic_reset', 'autopay', 'gift', 'buy_for'));
