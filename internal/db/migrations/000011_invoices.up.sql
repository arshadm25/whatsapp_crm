-- GST invoices for what customers pay Ecogo. An invoice is issued when Razorpay confirms a
-- charge and keeps a snapshot of both parties, so later edits never change an issued invoice.

BEGIN;

-- What goes on a customer's invoices. Optional: without it the invoice has no buyer GSTIN and
-- takes the seller's state as the place of supply.
CREATE TABLE billing_profiles (
    tenant_id    uuid PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    legal_name   text NOT NULL,
    gstin        text CHECK (gstin IS NULL OR gstin ~ '^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][1-9A-Z]Z[0-9A-Z]$'),
    state_code   text NOT NULL CHECK (state_code ~ '^[0-9]{2}$'),
    address      text NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- Invoice numbers run without gaps within each Indian financial year (April to March).
CREATE TABLE invoice_counters (
    fy    text PRIMARY KEY,                -- 2026-27
    last  integer NOT NULL
);

CREATE TABLE invoices (
    id                   uuid PRIMARY KEY,
    tenant_id            uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    number               text NOT NULL UNIQUE,         -- ECO/2026-27/000001
    razorpay_payment_id  text NOT NULL UNIQUE,         -- one invoice per payment
    description          text NOT NULL,
    period_start         timestamptz,
    period_end           timestamptz,
    sac                  text,
    total_minor          bigint NOT NULL CHECK (total_minor > 0),      -- what the customer paid, GST included
    taxable_minor        bigint NOT NULL,
    gst_rate_bp          integer NOT NULL,                              -- 1800 = 18%
    cgst_minor           bigint NOT NULL DEFAULT 0,
    sgst_minor           bigint NOT NULL DEFAULT 0,
    igst_minor           bigint NOT NULL DEFAULT 0,
    place_of_supply      text NOT NULL,                                 -- state code
    seller               jsonb NOT NULL,                                -- name, gstin, address, state_code
    buyer                jsonb NOT NULL,                                -- legal_name, gstin, address, state_code
    issued_at            timestamptz NOT NULL DEFAULT now(),
    CHECK (taxable_minor + cgst_minor + sgst_minor + igst_minor = total_minor)
);
CREATE INDEX invoices_tenant_idx ON invoices (tenant_id, issued_at DESC);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['billing_profiles', 'invoices'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I USING (tenant_id = current_tenant_id()) '
            'WITH CHECK (tenant_id = current_tenant_id())', t);
    END LOOP;
END $$;

COMMIT;
