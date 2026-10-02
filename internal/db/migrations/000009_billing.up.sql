-- D9 billing: our own subscription plans, paid through Razorpay. Prices are set by platform
-- admins in the console, so plans start empty. Every workspace starts on a 14-day trial.

BEGIN;

ALTER TABLE plans
    ADD COLUMN razorpay_plan_id text UNIQUE,            -- plan_... created in the Razorpay dashboard
    ADD COLUMN sort_order       integer NOT NULL DEFAULT 0,
    ADD COLUMN created_at       timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN updated_at       timestamptz NOT NULL DEFAULT now(),
    ADD CONSTRAINT plans_code_check CHECK (code ~ '^[a-z][a-z0-9_]{1,31}$'),
    ADD CONSTRAINT plans_price_check CHECK (price_minor >= 0 AND extra_seat_minor >= 0
                                            AND included_numbers >= 1 AND included_seats >= 1);

-- A trial has no plan yet.
ALTER TABLE subscriptions
    ALTER COLUMN plan_code DROP NOT NULL,
    ADD COLUMN cancel_at_period_end boolean NOT NULL DEFAULT false;

CREATE FUNCTION start_trial() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path = public
    AS $$
    BEGIN
        INSERT INTO subscriptions (tenant_id, status, current_period_start, current_period_end)
        VALUES (NEW.id, 'trialing', NEW.created_at, NEW.created_at + interval '14 days');
        RETURN NEW;
    END
    $$;

CREATE TRIGGER tenants_start_trial AFTER INSERT ON tenants
    FOR EACH ROW EXECUTE FUNCTION start_trial();

-- Workspaces that already exist get their trial from today.
INSERT INTO subscriptions (tenant_id, status, current_period_start, current_period_end)
SELECT id, 'trialing', now(), now() + interval '14 days' FROM tenants
ON CONFLICT (tenant_id) DO NOTHING;

-- Razorpay webhooks name a subscription, not a workspace.
CREATE FUNCTION subscription_tenant(p_provider_subscription_id text)
    RETURNS uuid
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT tenant_id FROM subscriptions WHERE provider_subscription_id = p_provider_subscription_id $$;

-- Razorpay retries webhooks; each event is applied once.
CREATE TABLE razorpay_events (
    id           text PRIMARY KEY,                -- x-razorpay-event-id
    event        text NOT NULL,
    received_at  timestamptz NOT NULL DEFAULT now()
);

COMMIT;
