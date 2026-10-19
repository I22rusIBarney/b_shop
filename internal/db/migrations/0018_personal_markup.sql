-- Personal markup (%) on a customer's prices; the payment keeps the markup and the cashier commission it was
-- created with, so later changes to the customer or the settings never change an existing order.
ALTER TABLE customers ADD COLUMN personal_markup int NOT NULL DEFAULT 0 CHECK (personal_markup BETWEEN 0 AND 300);
ALTER TABLE payments ADD COLUMN markup_percent int NOT NULL DEFAULT 0;
ALTER TABLE payments ADD COLUMN commission_amount bigint NOT NULL DEFAULT 0;
