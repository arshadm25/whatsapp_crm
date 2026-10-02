-- D9 analytics: usage_daily also counts messages received from customers.

BEGIN;

ALTER TABLE usage_daily ADD COLUMN received integer NOT NULL DEFAULT 0;

COMMIT;
