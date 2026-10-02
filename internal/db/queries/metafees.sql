-- name: ListMetaRates :many
SELECT * FROM meta_rate_card ORDER BY category, country, effective_from DESC;

-- name: UpsertMetaRate :one
INSERT INTO meta_rate_card (id, category, country, rate_hundredths, effective_from)
VALUES (@id, @category, @country, @rate_hundredths, @effective_from)
ON CONFLICT (category, country, effective_from) DO UPDATE SET rate_hundredths = EXCLUDED.rate_hundredths
RETURNING *;

-- name: DeleteMetaRate :execrows
DELETE FROM meta_rate_card WHERE id = $1;

-- name: ListMetaFeeTenants :many
SELECT id, timezone, coalesce(meta_payment_mode_since, created_at)::timestamptz AS since
FROM tenants WHERE meta_payment_mode = 'through_us' AND status <> 'closed' ORDER BY id;

-- name: SetMetaPaymentMode :one
UPDATE tenants SET meta_payment_mode = @mode, meta_payment_mode_since = now(), updated_at = now()
WHERE id = @id RETURNING *;

-- name: MetaFeeUsage :many
-- Billable messages of one workspace between two dates (inclusive), by day, category and country.
SELECT day, pricing_category, recipient_country, (sum(billable))::bigint AS billable
FROM usage_daily
WHERE day BETWEEN @from_day AND @to_day AND billable > 0 AND pricing_category <> 'service'
GROUP BY day, pricing_category, recipient_country
ORDER BY day;

-- name: SubscriptionInvoicesBetween :many
SELECT number, total_minor, issued_at FROM invoices
WHERE issued_at >= @start AND issued_at < @end_at ORDER BY issued_at;

-- name: InsertMetaFeeStatement :execrows
INSERT INTO meta_fee_statements (id, tenant_id, month, number, lines, subscription, fee_minor, markup_bp, markup_minor,
                                 taxable_minor, gst_rate_bp, cgst_minor, sgst_minor, igst_minor, total_minor,
                                 place_of_supply, seller, buyer, issued_at)
VALUES (@id, @tenant_id, @month, @number, @lines, @subscription, @fee_minor, @markup_bp, @markup_minor,
        @taxable_minor, @gst_rate_bp, @cgst_minor, @sgst_minor, @igst_minor, @total_minor,
        @place_of_supply, @seller, @buyer, @issued_at)
ON CONFLICT (tenant_id, month) DO NOTHING;

-- name: MetaFeeStatementExists :one
SELECT EXISTS (SELECT 1 FROM meta_fee_statements WHERE month = $1)::boolean;

-- name: ListMetaFeeStatements :many
SELECT * FROM meta_fee_statements ORDER BY month DESC;

-- name: GetMetaFeeStatement :one
SELECT * FROM meta_fee_statements WHERE id = $1;

-- name: SetMetaFeeStatementPaid :one
UPDATE meta_fee_statements
SET status = @status, paid_at = CASE WHEN @status = 'paid' THEN now() END, payment_reference = sqlc.narg(payment_reference)
WHERE id = @id RETURNING *;

-- name: AdminListMetaFeeStatements :many
SELECT s.*, t.name AS tenant_name
FROM admin_meta_fee_statements(sqlc.narg(tenant_id)::uuid, sqlc.narg(status)::text) s
JOIN tenants t ON t.id = s.tenant_id
LIMIT @lim;

-- name: AdminGetMetaFeeStatement :one
SELECT * FROM admin_meta_fee_statement(@id::uuid);
