-- WhatsApp Flows (Phase 2): the forms a customer fills in inside WhatsApp. We keep the Flow JSON
-- and its status in step with Meta, and every submission against the contact who sent it.

BEGIN;

CREATE TABLE flows (
    id                  uuid PRIMARY KEY,
    tenant_id           uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    whatsapp_account_id uuid NOT NULL REFERENCES whatsapp_accounts(id) ON DELETE CASCADE,
    meta_flow_id        text NOT NULL UNIQUE,
    name                text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    categories          text[] NOT NULL,
    status              text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'deprecated', 'blocked', 'throttled')),
    flow_json           jsonb NOT NULL,
    validation_errors   jsonb NOT NULL DEFAULT '[]',
    preview_url         text,
    preview_expires_at  timestamptz,
    created_by          uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    published_at        timestamptz
);
CREATE INDEX flows_tenant_idx ON flows (tenant_id, created_at DESC);

CREATE TABLE flow_submissions (
    id               uuid PRIMARY KEY,
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    flow_id          uuid REFERENCES flows(id) ON DELETE SET NULL,
    meta_flow_id     text,
    contact_id       uuid NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    conversation_id  uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    message_id       uuid NOT NULL UNIQUE REFERENCES messages(id) ON DELETE CASCADE,
    flow_token       text,
    response         jsonb NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX flow_submissions_flow_idx ON flow_submissions (tenant_id, flow_id, created_at DESC);
CREATE INDEX flow_submissions_contact_idx ON flow_submissions (contact_id, created_at DESC);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['flows', 'flow_submissions'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = current_tenant_id()) WITH CHECK (tenant_id = current_tenant_id())', t);
    END LOOP;
END $$;

COMMIT;
