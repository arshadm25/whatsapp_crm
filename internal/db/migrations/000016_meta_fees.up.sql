-- Solution Partner groundwork (BRD Phase 2): what Meta charges for each workspace's messages,
-- and monthly statements for workspaces that pay Meta's fees through Ecogo.

BEGIN;

-- Who pays Meta for a workspace's messages: the workspace itself ('direct', the default while
-- Ecogo is a Tech Provider) or Ecogo, which re-bills the fees ('through_us').
ALTER TABLE tenants
    ADD COLUMN meta_payment_mode text NOT NULL DEFAULT 'direct'
        CHECK (meta_payment_mode IN ('direct', 'through_us')),
    ADD COLUMN meta_payment_mode_since timestamptz;

-- Meta's rate card, in hundredths of a paisa per billable message (7846 is Rs 0.7846). A rate
-- applies from its effective date until a later one replaces it; country '*' is the fallback
-- for countries without their own rate. Service messages are free and have no rate.
CREATE TABLE meta_rate_card (
    id               uuid PRIMARY KEY,
    category         text NOT NULL CHECK (category IN ('marketing', 'utility', 'authentication')),
    country          text NOT NULL CHECK (country = '*' OR country ~ '^[A-Z]{2}$'),
    rate_hundredths  bigint NOT NULL CHECK (rate_hundredths >= 0),
    effective_from   date NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (category, country, effective_from)
);

INSERT INTO meta_rate_card (id, category, country, rate_hundredths, effective_from) VALUES
    (gen_random_uuid(), 'marketing',      'IN', 7846, '2000-01-01'),
    (gen_random_uuid(), 'utility',        'IN', 1150, '2000-01-01'),
    (gen_random_uuid(), 'authentication', 'IN', 1150, '2000-01-01');

-- One statement per workspace and month: Meta's fees at the rate card, Ecogo's markup and GST,
-- and the subscription invoices of the same month for reference. It is the monthly invoice for
-- a workspace that pays through Ecogo.
CREATE TABLE meta_fee_statements (
    id                 uuid PRIMARY KEY,
    tenant_id          uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    month              date NOT NULL,                 -- first day of the month in India
    number             text NOT NULL UNIQUE,          -- ECO/MF/2026-27/000001
    lines              jsonb NOT NULL,                -- category, country, messages, rate, amount
    subscription       jsonb NOT NULL DEFAULT '[]',   -- the month's subscription invoices: number, total
    fee_minor          bigint NOT NULL,               -- Meta's fees
    markup_bp          integer NOT NULL DEFAULT 0,
    markup_minor       bigint NOT NULL DEFAULT 0,
    taxable_minor      bigint NOT NULL,               -- fee + markup
    gst_rate_bp        integer NOT NULL,
    cgst_minor         bigint NOT NULL DEFAULT 0,
    sgst_minor         bigint NOT NULL DEFAULT 0,
    igst_minor         bigint NOT NULL DEFAULT 0,
    total_minor        bigint NOT NULL,               -- amount due, GST added
    place_of_supply    text NOT NULL,
    seller             jsonb NOT NULL,
    buyer              jsonb NOT NULL,
    status             text NOT NULL DEFAULT 'due' CHECK (status IN ('due', 'paid', 'void')),
    paid_at            timestamptz,
    payment_reference  text,
    issued_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, month),
    CHECK (taxable_minor + cgst_minor + sgst_minor + igst_minor = total_minor)
);
CREATE INDEX meta_fee_statements_tenant_idx ON meta_fee_statements (tenant_id, month DESC);

ALTER TABLE meta_fee_statements ENABLE ROW LEVEL SECURITY;
ALTER TABLE meta_fee_statements FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON meta_fee_statements USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

-- Platform admins list statements across workspaces through narrow functions.
CREATE FUNCTION admin_meta_fee_statements(p_tenant uuid, p_status text)
    RETURNS SETOF meta_fee_statements
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT * FROM meta_fee_statements
          WHERE (p_tenant IS NULL OR tenant_id = p_tenant)
            AND (p_status IS NULL OR status = p_status)
          ORDER BY month DESC, number DESC $$;

CREATE FUNCTION admin_meta_fee_statement(p_id uuid)
    RETURNS SETOF meta_fee_statements
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT * FROM meta_fee_statements WHERE id = p_id $$;

COMMIT;
