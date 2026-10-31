-- Razorpay cannot change the plan of a subscription paid by UPI Autopay. Such a plan change
-- starts a second subscription for the new plan; once it is paid it replaces the first, which
-- is cancelled. replace_now: the new plan starts at once (an upgrade); otherwise it starts
-- when the paid period ends.

BEGIN;

ALTER TABLE subscriptions
    ADD COLUMN pending_provider_subscription_id text UNIQUE,
    ADD COLUMN pending_replace_now boolean NOT NULL DEFAULT false;

CREATE OR REPLACE FUNCTION subscription_tenant(p_provider_subscription_id text)
    RETURNS uuid
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT tenant_id FROM subscriptions
          WHERE provider_subscription_id = p_provider_subscription_id
             OR seat_provider_subscription_id = p_provider_subscription_id
             OR pending_provider_subscription_id = p_provider_subscription_id $$;

COMMIT;
