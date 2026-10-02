-- Chatbots (Phase 2). A bot is a flow of nodes that answers inbound messages on one number (or
-- every number), hands the conversation to a person, and logs every message it sends.

BEGIN;

CREATE TABLE bots (
    id               uuid PRIMARY KEY,
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name             text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    status           text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'paused')),
    phone_number_id  uuid REFERENCES phone_numbers(id) ON DELETE SET NULL,   -- NULL: every number
    flow             jsonb NOT NULL,
    created_by       uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX bots_tenant_idx ON bots (tenant_id, created_at);
CREATE INDEX bots_active_idx ON bots (tenant_id, phone_number_id) WHERE status = 'active';

-- One run of a bot in a conversation. Only one session per conversation is active at a time.
CREATE TABLE bot_sessions (
    id               uuid PRIMARY KEY,
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    bot_id           uuid NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    conversation_id  uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    contact_id       uuid NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    node_id          text,                        -- the node waiting for the customer's answer
    vars             jsonb NOT NULL DEFAULT '{}',
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'completed', 'handed_off', 'stopped', 'expired', 'failed')),
    end_reason       text,
    started_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    ended_at         timestamptz
);
CREATE UNIQUE INDEX bot_sessions_active_idx ON bot_sessions (conversation_id) WHERE status = 'active';
CREATE INDEX bot_sessions_bot_idx ON bot_sessions (bot_id, started_at DESC);

ALTER TABLE messages ADD COLUMN bot_id uuid REFERENCES bots(id) ON DELETE SET NULL;
CREATE INDEX messages_bot_idx ON messages (bot_id) WHERE bot_id IS NOT NULL;

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['bots', 'bot_sessions'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = current_tenant_id()) WITH CHECK (tenant_id = current_tenant_id())', t);
    END LOOP;
END $$;

COMMIT;
