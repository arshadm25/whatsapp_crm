-- A plan Ecogo staff put a workspace on from the admin console, without Razorpay: a pilot, a
-- partner or a goodwill extension. It runs until current_period_end when cancel_at_period_end
-- is set, otherwise until the owner pays for a plan. Paying through Razorpay clears it.
ALTER TABLE subscriptions ADD COLUMN admin_granted boolean NOT NULL DEFAULT false;
