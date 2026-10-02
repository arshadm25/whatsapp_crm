-- D7 campaigns: keyset paging for the list, and recipient statuses that follow their messages.

BEGIN;

CREATE INDEX campaigns_created_idx ON campaigns (tenant_id, created_at DESC, id DESC);
CREATE INDEX campaign_recipients_message_idx ON campaign_recipients (message_id) WHERE message_id IS NOT NULL;

-- A campaign message's status changes (sent, delivered, read, failed) are copied to its
-- recipient row, so a campaign's report is one count over campaign_recipients.
CREATE FUNCTION sync_campaign_recipient() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status IN ('queued', 'sent', 'delivered', 'read', 'failed') THEN
        UPDATE campaign_recipients
        SET status = NEW.status::text::recipient_status, error_code = NEW.error_code, updated_at = now()
        WHERE message_id = NEW.id AND campaign_id = NEW.campaign_id;
    END IF;
    RETURN NULL;
END $$;

CREATE TRIGGER messages_campaign_recipient AFTER UPDATE OF status ON messages
    FOR EACH ROW WHEN (NEW.campaign_id IS NOT NULL AND NEW.status IS DISTINCT FROM OLD.status)
    EXECUTE FUNCTION sync_campaign_recipient();

COMMIT;
