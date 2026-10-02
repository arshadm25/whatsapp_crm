-- D1 team: invite links are opened before the invited person has a tenant selected, so the
-- invite is found by its token hash through a narrow SECURITY DEFINER function.

BEGIN;

CREATE FUNCTION invite_by_token(p_token_hash bytea)
    RETURNS TABLE (id uuid, tenant_id uuid, tenant_name text, email citext, role member_role,
                   invited_by_name text, expires_at timestamptz, accepted_at timestamptz)
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$
        SELECT i.id, i.tenant_id, t.name, i.email, i.role, u.name, i.expires_at, i.accepted_at
        FROM invites i JOIN tenants t ON t.id = i.tenant_id JOIN users u ON u.id = i.invited_by
        WHERE i.token_hash = p_token_hash AND t.status = 'active'
    $$;

-- One open invite per email and workspace; inviting again replaces it.
CREATE UNIQUE INDEX invites_open_idx ON invites (tenant_id, email) WHERE accepted_at IS NULL;

COMMIT;
