-- Promo codes that open hidden plans: kind 'unlock', the plans to open are in plan_ids.
ALTER TABLE promo_codes DROP CONSTRAINT IF EXISTS promo_codes_kind_check;
ALTER TABLE promo_codes ADD CONSTRAINT promo_codes_kind_check CHECK (kind IN ('discount', 'days', 'unlock'));

-- Hidden plans a customer may see and buy.
CREATE TABLE unlocked_plans (
    customer_id bigint      NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    plan_id     bigint      NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
    promo_id    bigint REFERENCES promo_codes (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, plan_id)
);
