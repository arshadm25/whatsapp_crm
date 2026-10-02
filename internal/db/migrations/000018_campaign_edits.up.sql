-- Campaign drafts, edits, pause and resume. Every edit, pause or resume bumps run_generation;
-- run jobs carry the generation they were queued for, so a job queued before an edit or a
-- pause does nothing when it runs.

BEGIN;

ALTER TABLE campaigns ADD COLUMN run_generation integer NOT NULL DEFAULT 0;

COMMIT;
