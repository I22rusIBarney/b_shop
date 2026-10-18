-- Text the customer must acknowledge before paying for the plan (empty = nothing to acknowledge).
ALTER TABLE plans ADD COLUMN notice text NOT NULL DEFAULT '';
