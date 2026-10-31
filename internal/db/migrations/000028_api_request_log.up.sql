-- One row per public API request, for the API logs page (filters, status codes, latency).
-- Kept 30 days: purge_api_request_log runs from the daily retention job. The per-key hourly
-- counts in api_usage_hourly are kept for longer and feed the API keys page.

BEGIN;

CREATE TABLE api_request_log (
    id          bigserial PRIMARY KEY,
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    api_key_id  uuid NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    method      text NOT NULL,
    path        text NOT NULL,                -- the request path, no query string
    route       text NOT NULL DEFAULT '',     -- the matched route pattern, e.g. /v1/messages/{id}
    status      smallint NOT NULL,
    duration_ms integer NOT NULL,
    request_id  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX api_request_log_tenant_idx ON api_request_log (tenant_id, id DESC);
CREATE INDEX api_request_log_created_idx ON api_request_log (created_at);

-- Not FORCE: the purge function below runs as the table owner across all workspaces.
ALTER TABLE api_request_log ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON api_request_log USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

-- Deletes up to p_batch log rows older than p_cutoff, in every workspace; returns how many.
CREATE FUNCTION purge_api_request_log(p_cutoff timestamptz, p_batch integer)
    RETURNS bigint
    LANGUAGE sql SECURITY DEFINER
    SET search_path = public
    AS $$
        WITH gone AS (
            DELETE FROM api_request_log
            WHERE id IN (SELECT id FROM api_request_log WHERE created_at < p_cutoff ORDER BY id LIMIT p_batch)
            RETURNING 1
        )
        SELECT count(*) FROM gone
    $$;

COMMIT;
