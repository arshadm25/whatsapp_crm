-- AI agent (Phase 2): a knowledge base per workspace, a log of every AI answer, and the number
-- of AI replies each plan includes per billing month.

BEGIN;

-- 0 means the plan has no AI replies. Platform admins set the number per plan.
ALTER TABLE plans ADD COLUMN ai_replies_per_month integer NOT NULL DEFAULT 0 CHECK (ai_replies_per_month >= 0);

CREATE TABLE kb_sources (
    id           uuid PRIMARY KEY,
    tenant_id    uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind         text NOT NULL CHECK (kind IN ('faq', 'text', 'document', 'website')),
    title        text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    url          text,
    status       text NOT NULL DEFAULT 'ready' CHECK (status IN ('pending', 'ready', 'failed')),
    error        text,
    chunk_count  integer NOT NULL DEFAULT 0,
    created_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX kb_sources_tenant_idx ON kb_sources (tenant_id, created_at DESC);

CREATE TABLE kb_chunks (
    id          uuid PRIMARY KEY,
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    source_id   uuid NOT NULL REFERENCES kb_sources(id) ON DELETE CASCADE,
    ord         integer NOT NULL,
    content     text NOT NULL,
    tsv         tsvector GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED
);
CREATE INDEX kb_chunks_source_idx ON kb_chunks (source_id, ord);
CREATE INDEX kb_chunks_tsv_idx ON kb_chunks USING gin (tsv);

-- Every AI answer attempt, for review and for counting a plan's AI replies.
CREATE TABLE ai_logs (
    id               uuid PRIMARY KEY,
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    bot_id           uuid REFERENCES bots(id) ON DELETE SET NULL,
    conversation_id  uuid REFERENCES conversations(id) ON DELETE SET NULL,
    question         text NOT NULL,
    answer           text,
    confidence       real,
    outcome          text NOT NULL CHECK (outcome IN ('answered', 'low_confidence', 'no_knowledge', 'limit', 'error', 'test')),
    source_ids       uuid[] NOT NULL DEFAULT '{}',
    input_tokens     integer NOT NULL DEFAULT 0,
    output_tokens    integer NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ai_logs_tenant_idx ON ai_logs (tenant_id, created_at DESC);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['kb_sources', 'kb_chunks', 'ai_logs'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = current_tenant_id()) WITH CHECK (tenant_id = current_tenant_id())', t);
    END LOOP;
END $$;

COMMIT;
