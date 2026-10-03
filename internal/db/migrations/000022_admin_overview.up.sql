-- The admin console's tenant list shows each workspace's WABA, plan, numbers, messages in the
-- last 30 days and worst quality rating. Those rows are tenant-scoped, so one SECURITY DEFINER
-- function reads them for a page of workspaces at once instead of one request per row.

BEGIN;

CREATE FUNCTION admin_tenant_overview(p_ids uuid[])
    RETURNS TABLE (tenant_id uuid, waba_id text, waba_count int, plan_code text, subscription_status text,
                   numbers int, messages_30d int, worst_quality text)
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$
        SELECT t.id,
               (SELECT w.waba_id FROM whatsapp_accounts w WHERE w.tenant_id = t.id ORDER BY w.created_at LIMIT 1),
               (SELECT count(*) FROM whatsapp_accounts w WHERE w.tenant_id = t.id)::int,
               s.plan_code,
               s.status::text,
               (SELECT count(*) FROM phone_numbers p WHERE p.tenant_id = t.id)::int,
               (SELECT coalesce(sum(u.sent), 0) FROM usage_daily u
                 WHERE u.tenant_id = t.id AND u.day > current_date - 30)::int,
               (SELECT p.quality_rating::text FROM phone_numbers p WHERE p.tenant_id = t.id AND p.quality_rating <> 'unknown'
                 ORDER BY CASE p.quality_rating WHEN 'red' THEN 0 WHEN 'yellow' THEN 1 ELSE 2 END LIMIT 1)
        FROM unnest(p_ids) AS ids(id)
        JOIN tenants t ON t.id = ids.id
        LEFT JOIN subscriptions s ON s.tenant_id = t.id
    $$;

CREATE INDEX meta_api_errors_tenant_time_idx ON meta_api_errors (tenant_id, occurred_at DESC);

COMMIT;
