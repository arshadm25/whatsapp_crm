-- API calls per key and hour, for "API calls today" and the error rate on the Developers screen.

BEGIN;

CREATE TABLE api_usage_hourly (
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    api_key_id  uuid NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    hour        timestamptz NOT NULL,
    calls       integer NOT NULL DEFAULT 0,
    errors      integer NOT NULL DEFAULT 0,    -- responses with status 400 or above
    PRIMARY KEY (api_key_id, hour)
);
CREATE INDEX api_usage_hourly_tenant_idx ON api_usage_hourly (tenant_id, hour DESC);

ALTER TABLE api_usage_hourly ENABLE ROW LEVEL SECURITY;
ALTER TABLE api_usage_hourly FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON api_usage_hourly USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

COMMIT;
