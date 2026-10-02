-- name: ListMetaBillingAccounts :many
-- Connected WABAs of active workspaces, for the reconciliation job.
SELECT * FROM meta_billing_accounts();

-- name: ListCreditLineCandidates :many
-- Connected WABAs of workspaces paid through Ecogo that have no credit line yet.
SELECT * FROM credit_line_candidates();

-- name: SetCreditLineResult :exec
UPDATE whatsapp_accounts
SET credit_line_allocation_id = sqlc.narg(allocation_id),
    credit_line_attached_at = CASE WHEN sqlc.narg(allocation_id)::text IS NOT NULL THEN now() END,
    credit_line_error = sqlc.narg(error), updated_at = now()
WHERE id = @id;

-- name: UsageForReconciliation :many
SELECT day, pricing_category, recipient_country, (sum(billable))::bigint AS billable
FROM usage_daily
WHERE day BETWEEN @from_day AND @to_day AND pricing_category <> 'service'
GROUP BY day, pricing_category, recipient_country
HAVING sum(billable) > 0;

-- name: UpsertReconciliation :exec
INSERT INTO meta_reconciliation (tenant_id, waba_id, day, category, country, our_messages, meta_messages,
                                 our_cost_minor, meta_cost_minor, currency, status, checked_at)
VALUES (@tenant_id, @waba_id, @day, @category, @country, @our_messages, @meta_messages,
        @our_cost_minor, @meta_cost_minor, @currency, @status, now())
ON CONFLICT (tenant_id, waba_id, day, category, country) DO UPDATE
SET our_messages = EXCLUDED.our_messages, meta_messages = EXCLUDED.meta_messages,
    our_cost_minor = EXCLUDED.our_cost_minor, meta_cost_minor = EXCLUDED.meta_cost_minor,
    currency = EXCLUDED.currency, status = EXCLUDED.status, checked_at = now();

-- name: AdminListReconciliation :many
SELECT r.*, t.name AS tenant_name
FROM admin_meta_reconciliation(sqlc.narg(status)::text, @since::date) r
JOIN tenants t ON t.id = r.tenant_id
LIMIT @lim;

-- name: AdminCreditLineProblems :many
SELECT a.*, t.name AS tenant_name FROM admin_credit_line_problems() a JOIN tenants t ON t.id = a.tenant_id;
