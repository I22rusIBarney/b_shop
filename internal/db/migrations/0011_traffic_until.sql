-- Bought traffic ends at the next traffic reset of the plan (e.g. the 1st of the month) or, without resets,
-- at the end of the paid period.
ALTER TABLE subscriptions ADD COLUMN extra_traffic_until timestamptz;
