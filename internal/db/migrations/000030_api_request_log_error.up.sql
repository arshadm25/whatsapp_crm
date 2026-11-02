-- Why a public API call failed: the error code, message and any Meta error code it answered
-- with, so the API logs page can show the cause of a 4xx or 5xx, not only its status.

BEGIN;

ALTER TABLE api_request_log
    ADD COLUMN error_code      text NOT NULL DEFAULT '',
    ADD COLUMN error_message   text NOT NULL DEFAULT '',
    ADD COLUMN meta_error_code integer NOT NULL DEFAULT 0;

COMMIT;
