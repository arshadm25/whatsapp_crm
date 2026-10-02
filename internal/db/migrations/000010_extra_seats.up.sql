-- Extra seats are bought on top of a plan's included seats. Each plan names the Razorpay plan
-- that charges one seat per month; the workspace buys N of them as a second Razorpay
-- subscription with quantity N.

BEGIN;

ALTER TABLE plans
    ADD COLUMN extra_seat_razorpay_plan_id text;

ALTER TABLE subscriptions
    ADD COLUMN extra_seats integer NOT NULL DEFAULT 0 CHECK (extra_seats >= 0),
    ADD COLUMN seat_provider_subscription_id text UNIQUE;

-- Razorpay webhooks name a subscription: the plan's or the seats'.
CREATE OR REPLACE FUNCTION subscription_tenant(p_provider_subscription_id text)
    RETURNS uuid
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT tenant_id FROM subscriptions
          WHERE provider_subscription_id = p_provider_subscription_id
             OR seat_provider_subscription_id = p_provider_subscription_id $$;

COMMIT;
