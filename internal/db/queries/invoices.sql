-- name: GetBillingProfile :one
SELECT * FROM billing_profiles LIMIT 1;

-- name: UpsertBillingProfile :one
INSERT INTO billing_profiles (tenant_id, legal_name, gstin, state_code, address)
VALUES (@tenant_id, @legal_name, sqlc.narg(gstin), @state_code, @address)
ON CONFLICT (tenant_id) DO UPDATE
SET legal_name = EXCLUDED.legal_name, gstin = EXCLUDED.gstin, state_code = EXCLUDED.state_code,
    address = EXCLUDED.address, updated_at = now()
RETURNING *;

-- name: NextInvoiceNumber :one
-- Locks the year's counter row until the invoice commits, so numbers stay gapless.
INSERT INTO invoice_counters (fy, last) VALUES (@fy, 1)
ON CONFLICT (fy) DO UPDATE SET last = invoice_counters.last + 1
RETURNING last;

-- name: InsertInvoice :execrows
INSERT INTO invoices (id, tenant_id, number, razorpay_payment_id, description, period_start, period_end, sac,
                      total_minor, taxable_minor, gst_rate_bp, cgst_minor, sgst_minor, igst_minor,
                      place_of_supply, seller, buyer, issued_at)
VALUES (@id, @tenant_id, @number, @razorpay_payment_id, @description, sqlc.narg(period_start), sqlc.narg(period_end),
        sqlc.narg(sac), @total_minor, @taxable_minor, @gst_rate_bp, @cgst_minor, @sgst_minor, @igst_minor,
        @place_of_supply, @seller, @buyer, @issued_at)
ON CONFLICT (razorpay_payment_id) DO NOTHING;

-- name: ListInvoices :many
SELECT * FROM invoices ORDER BY issued_at DESC, number DESC;

-- name: GetInvoice :one
SELECT * FROM invoices WHERE id = $1;

-- name: PaymentInvoiced :one
SELECT EXISTS (SELECT 1 FROM invoices WHERE razorpay_payment_id = $1)::boolean;

-- name: MarkInvoiceEmailed :exec
UPDATE invoices SET emailed_at = now() WHERE id = $1;

-- name: OwnerEmails :many
SELECT u.email::text FROM memberships m JOIN users u ON u.id = m.user_id WHERE m.role = 'owner' ORDER BY u.email;

-- name: AdminListInvoices :many
SELECT i.*, t.name AS tenant_name
FROM admin_invoices(sqlc.narg(from_at)::timestamptz, sqlc.narg(to_at)::timestamptz, sqlc.narg(tenant_id)::uuid) i
JOIN tenants t ON t.id = i.tenant_id
LIMIT @lim;

-- name: AdminGetInvoice :one
SELECT * FROM admin_invoice(@id::uuid);
