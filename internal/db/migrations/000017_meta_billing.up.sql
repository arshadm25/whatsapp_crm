-- Daily reconciliation of our usage records against Meta's billing data, and the credit line
-- Ecogo shares with a WABA once it is a Solution Partner.

BEGIN;

-- One row per WABA, day, category and country: what we counted against what Meta reports.
-- Costs are in the WABA's currency, as hundredths (paise, cents).
CREATE TABLE meta_reconciliation (
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    waba_id          text NOT NULL,
    day              date NOT NULL,                -- in India
    category         text NOT NULL,
    country          text NOT NULL,
    our_messages     bigint NOT NULL,
    meta_messages    bigint NOT NULL,
    our_cost_minor   bigint NOT NULL,              -- our messages at the rate card
    meta_cost_minor  bigint NOT NULL,
    currency         text NOT NULL,
    status           text NOT NULL CHECK (status IN ('match', 'mismatch')),
    checked_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, waba_id, day, category, country)
);
CREATE INDEX meta_reconciliation_status_idx ON meta_reconciliation (day DESC) WHERE status = 'mismatch';

ALTER TABLE meta_reconciliation ENABLE ROW LEVEL SECURITY;
ALTER TABLE meta_reconciliation FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON meta_reconciliation USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

CREATE FUNCTION admin_meta_reconciliation(p_status text, p_since date)
    RETURNS SETOF meta_reconciliation
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT * FROM meta_reconciliation
          WHERE day >= p_since AND (p_status IS NULL OR status = p_status)
          ORDER BY day DESC, tenant_id, category, country $$;

-- Credit line sharing: set when Meta confirms the allocation, or the last error to show admins.
ALTER TABLE whatsapp_accounts
    ADD COLUMN credit_line_allocation_id text,
    ADD COLUMN credit_line_attached_at timestamptz,
    ADD COLUMN credit_line_error text;

-- The jobs work across workspaces, so they list the accounts through narrow functions that
-- return only what is needed to pick the workspace; the work itself runs inside it.
CREATE FUNCTION meta_billing_accounts()
    RETURNS SETOF whatsapp_accounts
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT a.* FROM whatsapp_accounts a JOIN tenants t ON t.id = a.tenant_id
          WHERE a.status = 'connected' AND t.status = 'active' ORDER BY a.created_at $$;

CREATE FUNCTION credit_line_candidates()
    RETURNS SETOF whatsapp_accounts
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT a.* FROM whatsapp_accounts a JOIN tenants t ON t.id = a.tenant_id
          WHERE a.status = 'connected' AND t.status = 'active' AND t.meta_payment_mode = 'through_us'
            AND a.credit_line_allocation_id IS NULL ORDER BY a.created_at $$;

CREATE FUNCTION admin_credit_line_problems()
    RETURNS SETOF whatsapp_accounts
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT * FROM whatsapp_accounts WHERE credit_line_error IS NOT NULL ORDER BY updated_at DESC $$;

COMMIT;
