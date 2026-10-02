-- Invoices are emailed to the workspace owners once, and platform admins can list them across
-- workspaces (for the accountant) through narrow SECURITY DEFINER functions.

BEGIN;

ALTER TABLE invoices ADD COLUMN emailed_at timestamptz;

CREATE FUNCTION admin_invoices(p_from timestamptz, p_to timestamptz, p_tenant uuid)
    RETURNS SETOF invoices
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT * FROM invoices
          WHERE (p_from IS NULL OR issued_at >= p_from)
            AND (p_to IS NULL OR issued_at < p_to)
            AND (p_tenant IS NULL OR tenant_id = p_tenant)
          ORDER BY issued_at DESC, number DESC $$;

CREATE FUNCTION admin_invoice(p_id uuid)
    RETURNS SETOF invoices
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT * FROM invoices WHERE id = p_id $$;

COMMIT;
