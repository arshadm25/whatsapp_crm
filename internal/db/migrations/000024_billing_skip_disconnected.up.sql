-- The billing jobs only read accounts that still have a number in use. When every number of a
-- WhatsApp account is disconnected (the business removed our access in Meta), Meta refuses reads
-- of the account (error 100/33), so the daily reconciliation and the credit-line sweep would
-- fail on it every day. Template sync already skips these accounts (see ListSyncableAccounts).
CREATE OR REPLACE FUNCTION meta_billing_accounts()
    RETURNS SETOF whatsapp_accounts
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT a.* FROM whatsapp_accounts a JOIN tenants t ON t.id = a.tenant_id
          WHERE a.status = 'connected' AND t.status = 'active'
            AND EXISTS (SELECT 1 FROM phone_numbers p WHERE p.whatsapp_account_id = a.id AND p.status <> 'disconnected')
          ORDER BY a.created_at $$;

CREATE OR REPLACE FUNCTION credit_line_candidates()
    RETURNS SETOF whatsapp_accounts
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT a.* FROM whatsapp_accounts a JOIN tenants t ON t.id = a.tenant_id
          WHERE a.status = 'connected' AND t.status = 'active' AND t.meta_payment_mode = 'through_us'
            AND a.credit_line_allocation_id IS NULL
            AND EXISTS (SELECT 1 FROM phone_numbers p WHERE p.whatsapp_account_id = a.id AND p.status <> 'disconnected')
          ORDER BY a.created_at $$;
