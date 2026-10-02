-- Sign-in settings and in-app notifications.
--   * tenants.require_two_factor makes every member turn on two-step verification before using the workspace.
--   * sessions.persistent marks "Keep me logged in" sessions, which last 30 days instead of the idle timeout.
--   * notifications are created by triggers, so they appear whichever service made the change:
--     a conversation assigned to someone, a template reviewed by Meta, a number's quality dropping,
--     and a campaign finishing. notification_settings holds each member's in-app and email choices.

BEGIN;

ALTER TABLE tenants ADD COLUMN require_two_factor boolean NOT NULL DEFAULT false;
ALTER TABLE sessions ADD COLUMN persistent boolean NOT NULL DEFAULT false;

CREATE TABLE notifications (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        text NOT NULL,
    title       text NOT NULL,
    body        text NOT NULL DEFAULT '',
    link        text,
    in_app      boolean NOT NULL DEFAULT true,  -- false: sent by email only, not shown in the bell
    email       text NOT NULL DEFAULT 'none' CHECK (email IN ('none', 'pending', 'sent', 'failed')),
    read_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notifications_user_idx ON notifications (tenant_id, user_id, created_at DESC);
CREATE INDEX notifications_email_idx ON notifications (tenant_id) WHERE email = 'pending';

CREATE TABLE notification_settings (
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        text NOT NULL,
    in_app      boolean NOT NULL,
    email       boolean NOT NULL,
    PRIMARY KEY (tenant_id, user_id, kind)
);

ALTER TABLE notifications ENABLE ROW LEVEL SECURITY;
ALTER TABLE notifications FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON notifications USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());
ALTER TABLE notification_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON notification_settings USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

-- The default for each kind when a member has not chosen: {in_app, email}. Keep in step with
-- notifications.Kinds in Go.
CREATE FUNCTION notification_default(p_kind text, p_email boolean) RETURNS boolean
    LANGUAGE sql IMMUTABLE
    AS $$
        SELECT CASE WHEN NOT p_email THEN true
                    ELSE p_kind IN ('template_reviewed', 'number_quality') END
    $$;

-- notify_users adds one notification per user who wants this kind in the app or by email, and
-- tells open dashboards to refresh their bell.
CREATE FUNCTION notify_users(p_tenant uuid, p_users uuid[], p_kind text, p_title text, p_body text, p_link text)
    RETURNS void
    LANGUAGE plpgsql
    AS $$
        DECLARE
            n int;
        BEGIN
            INSERT INTO notifications (tenant_id, user_id, kind, title, body, link, in_app, email)
            SELECT p_tenant, x.u, p_kind, p_title, p_body, p_link, x.in_app, CASE WHEN x.email THEN 'pending' ELSE 'none' END
            FROM (
                SELECT us.u, coalesce(s.in_app, notification_default(p_kind, false)) AS in_app,
                       coalesce(s.email, notification_default(p_kind, true)) AS email
                FROM (SELECT DISTINCT unnest(p_users) AS u) us
                JOIN memberships m ON m.tenant_id = p_tenant AND m.user_id = us.u
                LEFT JOIN notification_settings s ON s.tenant_id = p_tenant AND s.user_id = us.u AND s.kind = p_kind
            ) x
            WHERE x.in_app OR x.email;
            GET DIAGNOSTICS n = ROW_COUNT;
            IF n > 0 THEN
                PERFORM pg_notify('ecogo_events', json_build_object(
                    'tenant_id', p_tenant, 'type', 'notification', 'id', p_tenant)::text);
            END IF;
        END
    $$;

CREATE FUNCTION workspace_managers(p_tenant uuid) RETURNS uuid[]
    LANGUAGE sql STABLE
    AS $$ SELECT coalesce(array_agg(user_id), '{}') FROM memberships WHERE tenant_id = p_tenant AND role IN ('owner', 'admin') $$;

CREATE FUNCTION notify_conversation_assigned() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
        DECLARE
            who text;
        BEGIN
            IF NEW.assignee_user_id IS NOT NULL AND NEW.assignee_user_id IS DISTINCT FROM OLD.assignee_user_id THEN
                SELECT coalesce(c.name, c.profile_name, '+' || c.wa_id) INTO who FROM contacts c WHERE c.id = NEW.contact_id;
                PERFORM notify_users(NEW.tenant_id, ARRAY[NEW.assignee_user_id], 'conversation_assigned',
                    'Conversation assigned to you', coalesce(who, ''), '/inbox/' || NEW.id);
            END IF;
            RETURN NULL;
        END
    $$;
CREATE TRIGGER conversations_assigned_notify AFTER UPDATE OF assignee_user_id ON conversations
    FOR EACH ROW EXECUTE FUNCTION notify_conversation_assigned();

CREATE FUNCTION notify_template_reviewed() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
        BEGIN
            IF NEW.status IN ('approved', 'rejected', 'paused', 'disabled') AND NEW.status IS DISTINCT FROM OLD.status THEN
                PERFORM notify_users(NEW.tenant_id,
                    CASE WHEN NEW.created_by IS NULL THEN workspace_managers(NEW.tenant_id) ELSE ARRAY[NEW.created_by] END,
                    'template_reviewed', 'Template ' || NEW.name || ' is ' || NEW.status::text,
                    coalesce(NEW.rejected_reason, ''), '/templates');
            END IF;
            RETURN NULL;
        END
    $$;
CREATE TRIGGER templates_reviewed_notify AFTER UPDATE OF status ON templates
    FOR EACH ROW EXECUTE FUNCTION notify_template_reviewed();

CREATE FUNCTION notify_number_quality() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
        BEGIN
            IF (OLD.quality_rating = 'green' AND NEW.quality_rating IN ('yellow', 'red'))
               OR (OLD.quality_rating = 'yellow' AND NEW.quality_rating = 'red') THEN
                PERFORM notify_users(NEW.tenant_id, workspace_managers(NEW.tenant_id), 'number_quality',
                    'Quality of ' || coalesce(NEW.display_phone_number, NEW.phone_number_id) || ' dropped to ' || NEW.quality_rating::text,
                    'Meta lowers messaging limits when quality stays low. Check recent campaigns for blocks and reports.', '/numbers');
            END IF;
            RETURN NULL;
        END
    $$;
CREATE TRIGGER phone_numbers_quality_notify AFTER UPDATE OF quality_rating ON phone_numbers
    FOR EACH ROW EXECUTE FUNCTION notify_number_quality();

CREATE FUNCTION notify_campaign_finished() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
        BEGIN
            IF NEW.status IN ('completed', 'failed') AND NEW.status IS DISTINCT FROM OLD.status THEN
                PERFORM notify_users(NEW.tenant_id,
                    CASE WHEN NEW.created_by IS NULL THEN workspace_managers(NEW.tenant_id) ELSE ARRAY[NEW.created_by] END,
                    'campaign_finished', 'Campaign ' || NEW.name || ' ' || NEW.status::text, '', '/campaigns');
            END IF;
            RETURN NULL;
        END
    $$;
CREATE TRIGGER campaigns_finished_notify AFTER UPDATE OF status ON campaigns
    FOR EACH ROW EXECUTE FUNCTION notify_campaign_finished();

-- Workspaces with notification emails waiting; the email job runs across tenants.
CREATE FUNCTION notification_email_tenants() RETURNS SETOF uuid
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$ SELECT DISTINCT tenant_id FROM notifications WHERE email = 'pending' $$;

COMMIT;
