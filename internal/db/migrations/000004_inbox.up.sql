-- Inbox: live updates and conversation listing.

BEGIN;

-- Every change to a message, conversation or template is announced on the ecogo_events
-- channel when its transaction commits. The api's SSE endpoint LISTENs once per pod and
-- forwards each event to the dashboards of that tenant, so the inbox updates without refresh
-- whichever service made the change (api, worker or a webhook).
CREATE FUNCTION notify_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
        DECLARE
            conv uuid;
        BEGIN
            -- Each branch touches only columns its table has.
            IF TG_TABLE_NAME = 'messages' THEN
                conv := NEW.conversation_id;
            ELSIF TG_TABLE_NAME = 'conversations' THEN
                conv := NEW.id;
            END IF;
            PERFORM pg_notify('ecogo_events', json_build_object(
                'tenant_id', NEW.tenant_id, 'type', TG_ARGV[0], 'id', NEW.id, 'conversation_id', conv
            )::text);
            RETURN NULL;
        END
    $$;

CREATE TRIGGER messages_notify AFTER INSERT OR UPDATE ON messages
    FOR EACH ROW EXECUTE FUNCTION notify_change('message');
CREATE TRIGGER conversations_notify AFTER INSERT OR UPDATE ON conversations
    FOR EACH ROW EXECUTE FUNCTION notify_change('conversation');
CREATE TRIGGER templates_notify AFTER INSERT OR UPDATE ON templates
    FOR EACH ROW EXECUTE FUNCTION notify_change('template');

-- The inbox lists conversations by latest activity across all numbers.
CREATE INDEX conversations_activity_idx ON conversations (tenant_id, (coalesce(last_message_at, created_at)) DESC, id DESC);

COMMIT;
