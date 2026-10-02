-- Application access: functions the app needs before app.tenant_id is known, and grants.
--
-- Roles
--   * The migration role owns every table and runs these migrations. Because RLS is FORCEd,
--     the owner is subject to the tenant policies too, so the owner role needs BYPASSRLS
--     (or superuser) for the SECURITY DEFINER functions below to see across tenants.
--   * ecogo_app is the role the api, ingest and worker pods log in as. It never has BYPASSRLS.
--     It is created by the cluster (CloudNativePG managed roles) or deploy/dev/initdb.sql locally.

BEGIN;

-- Tenants a user belongs to; used at login and on the tenant switcher, before a tenant is selected.
CREATE FUNCTION user_memberships(p_user_id uuid)
    RETURNS TABLE (tenant_id uuid, tenant_name text, tenant_slug citext, tenant_status tenant_status, role member_role)
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$
        SELECT t.id, t.name, t.slug, t.status, m.role
        FROM memberships m JOIN tenants t ON t.id = m.tenant_id
        WHERE m.user_id = p_user_id
        ORDER BY m.created_at
    $$;

-- Which tenant (if any) already owns a WABA; used by Embedded Signup to refuse a WABA connected elsewhere.
CREATE FUNCTION waba_owner(p_waba_id text)
    RETURNS uuid
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT tenant_id FROM whatsapp_accounts WHERE waba_id = p_waba_id $$;

-- Which tenant (if any) already owns a Meta phone number ID.
CREATE FUNCTION phone_number_owner(p_phone_number_id text)
    RETURNS uuid
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT tenant_id FROM phone_numbers WHERE phone_number_id = p_phone_number_id $$;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ecogo_app') THEN
        GRANT USAGE ON SCHEMA public TO ecogo_app;
        GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO ecogo_app;
        REVOKE UPDATE, DELETE ON audit_log, consent_events FROM ecogo_app;
        GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO ecogo_app;
        -- Tables created by later migrations (including River's) get the same grants.
        ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO ecogo_app;
        ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO ecogo_app;
    ELSE
        RAISE NOTICE 'role ecogo_app does not exist; skipping grants';
    END IF;
END $$;

COMMIT;
