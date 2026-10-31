-- The seller printed on GST invoices and Meta fee statements, edited by Ecogo staff in the admin
-- console. One row at most. Until it exists the ECOGO_SELLER_* settings are used, and nothing is
-- invoiced while the GSTIN is empty.
CREATE TABLE seller_settings (
    id          boolean PRIMARY KEY DEFAULT true CHECK (id),
    name        text NOT NULL,
    gstin       text NOT NULL,
    address     text NOT NULL,
    sac         text NOT NULL,
    updated_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at  timestamptz NOT NULL DEFAULT now()
);
