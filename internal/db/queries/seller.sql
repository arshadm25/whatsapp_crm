-- name: GetSellerSettings :one
SELECT * FROM seller_settings WHERE id;

-- name: UpsertSellerSettings :one
INSERT INTO seller_settings (id, name, gstin, address, sac, updated_by, updated_at)
VALUES (true, @name, @gstin, @address, @sac, sqlc.narg('updated_by'), now())
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, gstin = EXCLUDED.gstin, address = EXCLUDED.address,
    sac = EXCLUDED.sac, updated_by = EXCLUDED.updated_by, updated_at = now()
RETURNING *;
