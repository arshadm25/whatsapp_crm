-- Platform settings edited by Ecogo staff in the admin console (Admin > Settings). One row at
-- most. A NULL column means "use the deployment setting": ECOGO_GST_RATE_BP, ECOGO_META_MARKUP_BP
-- and ECOGO_AI_MODEL. Documents already issued keep the rates they were issued with.
CREATE TABLE platform_settings (
    id              boolean PRIMARY KEY DEFAULT true CHECK (id),
    gst_rate_bp     integer CHECK (gst_rate_bp BETWEEN 0 AND 10000),
    meta_markup_bp  integer CHECK (meta_markup_bp BETWEEN 0 AND 10000),
    ai_model        text CHECK (ai_model ~ '^[A-Za-z0-9][A-Za-z0-9._:@/-]{0,99}$'),
    updated_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- A workspace's own markup on Meta's fees, replacing the platform one when set.
ALTER TABLE tenants
    ADD COLUMN meta_markup_bp integer CHECK (meta_markup_bp BETWEEN 0 AND 10000);
