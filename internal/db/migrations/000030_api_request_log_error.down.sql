BEGIN;

ALTER TABLE api_request_log
    DROP COLUMN error_code,
    DROP COLUMN error_message,
    DROP COLUMN meta_error_code;

COMMIT;
