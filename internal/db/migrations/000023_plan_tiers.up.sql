-- Plan tiers: a free plan that needs no payment, contact and monthly message limits, a yearly
-- price next to the monthly one, and the text the plan cards show. The four standard plans
-- (Free, Starter, Growth, Scale) are added here; admins still set each paid plan's Razorpay
-- plan IDs in the console before customers can pay for it.

BEGIN;

ALTER TABLE plans
    -- NULL: no limit. Existing plans keep no limit until an admin sets one.
    ADD COLUMN contacts_limit          integer CHECK (contacts_limit >= 0),
    ADD COLUMN messages_per_month      integer CHECK (messages_per_month >= 0),
    -- NULL: the plan is sold monthly only. Paise, for the whole year.
    ADD COLUMN yearly_price_minor      bigint CHECK (yearly_price_minor >= 0),
    ADD COLUMN yearly_razorpay_plan_id text UNIQUE,
    ADD COLUMN tagline                 text NOT NULL DEFAULT '' CHECK (length(tagline) <= 200),
    ADD COLUMN highlights              text[] NOT NULL DEFAULT '{}',
    ADD COLUMN is_popular              boolean NOT NULL DEFAULT false;

-- Which of the plan's prices the workspace pays.
ALTER TABLE subscriptions
    ADD COLUMN billing_interval text NOT NULL DEFAULT 'month' CHECK (billing_interval IN ('month', 'year'));

-- Prices are GST-inclusive, in paise, and exclude Meta's own WhatsApp message fees, which Meta bills. Plans already set up under these codes are left as they are.
INSERT INTO plans (code, name, price_minor, included_numbers, included_seats, extra_seat_minor,
                   contacts_limit, messages_per_month, tagline, highlights, is_popular, sort_order, is_active)
VALUES
    ('free', 'Free', 0, 1, 1, 0, 25, 50,
     'Try Ecogo Connect on the official WhatsApp API, no payment needed.',
     ARRAY['Official WhatsApp Cloud API number', 'WhatsApp inbox', 'Approved templates synced from Meta',
           'Opt-in and opt-out tracking', 'Message status and API logs', 'Email support'],
     false, 10, true),
    ('starter', 'Starter', 99900, 1, 2, 0, 2500, 2500,
     'For small teams starting out on the WhatsApp Business API.',
     ARRAY['Everything in Free', 'Contacts with CSV import', 'Create templates and submit them to Meta',
           'Templates with media headers and buttons', 'Campaigns, sent now or scheduled',
           'Chatbots and WhatsApp Flows'],
     false, 20, true),
    ('growth', 'Growth', 249900, 3, 8, 0, 15000, 15000,
     'For sales and support teams that are growing.',
     ARRAY['Everything in Starter', 'Up to 3 WhatsApp numbers in one inbox',
           'Assign conversations across a team of 8', 'Campaign and delivery analytics', 'Priority support'],
     true, 30, true),
    ('scale', 'Scale', 499900, 10, 25, 0, 75000, 75000,
     'For agencies and high-volume teams running many numbers.',
     ARRAY['Everything in Growth', 'Up to 10 WhatsApp numbers across accounts', 'Team of up to 25',
           'API keys scoped to a number, with usage logs', 'Priority support and guided onboarding'],
     false, 40, true)
ON CONFLICT (code) DO NOTHING;

COMMIT;
