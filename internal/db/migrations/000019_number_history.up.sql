-- Quality history for numbers, so the dashboard can say a rating dropped, and the time each
-- onboarding step was reached, for the Connect WhatsApp checklist.

BEGIN;

ALTER TABLE phone_numbers
    ADD COLUMN previous_quality_rating quality_rating,
    ADD COLUMN quality_changed_at timestamptz;

CREATE FUNCTION phone_quality_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.previous_quality_rating := OLD.quality_rating;
    NEW.quality_changed_at := now();
    RETURN NEW;
END $$;

CREATE TRIGGER phone_quality_history BEFORE UPDATE OF quality_rating ON phone_numbers
    FOR EACH ROW WHEN (OLD.quality_rating IS DISTINCT FROM NEW.quality_rating)
    EXECUTE FUNCTION phone_quality_history();

ALTER TABLE onboarding_sessions ADD COLUMN step_times jsonb NOT NULL DEFAULT '{}'::jsonb;

-- step_times maps each step to when the session first reached it.
CREATE FUNCTION onboarding_step_times() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT NEW.step_times ? NEW.step::text THEN
        NEW.step_times := NEW.step_times || jsonb_build_object(NEW.step::text, now());
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER onboarding_step_times BEFORE INSERT OR UPDATE OF step ON onboarding_sessions
    FOR EACH ROW EXECUTE FUNCTION onboarding_step_times();

COMMIT;
