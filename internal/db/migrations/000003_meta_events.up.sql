-- Meta webhook processing helpers.

BEGIN;

-- Order of outbound message states. Meta can deliver status webhooks out of order, so a
-- status only replaces the current one when it ranks higher; failed is terminal.
CREATE FUNCTION message_status_rank(s message_status) RETURNS integer
    LANGUAGE sql IMMUTABLE
    AS $$
        SELECT CASE s
            WHEN 'queued'    THEN 0
            WHEN 'sent'      THEN 1
            WHEN 'delivered' THEN 2
            WHEN 'read'      THEN 3
            WHEN 'failed'    THEN 4
            ELSE 0
        END
    $$;

-- Webhook routing for a whole account (account_update, template and quality events carry only the WABA).
CREATE INDEX phone_numbers_account_idx ON phone_numbers (whatsapp_account_id);

-- Fix from the v0.1 schema: Postgres regular expressions allow at most 255 repetitions, so
-- '{1,512}' made every template insert fail. Same rule, written so Postgres accepts it.
ALTER TABLE templates DROP CONSTRAINT templates_name_check;
ALTER TABLE templates ADD CONSTRAINT templates_name_check CHECK (name ~ '^[a-z0-9_]+$' AND length(name) <= 512);

COMMIT;
