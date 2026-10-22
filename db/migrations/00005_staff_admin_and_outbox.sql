-- +goose Up
ALTER TABLE venues
    ADD COLUMN department_id uuid,
    ADD COLUMN active boolean NOT NULL DEFAULT true,
    ADD CONSTRAINT venues_department_fk
        FOREIGN KEY (institution_id, department_id)
        REFERENCES departments(institution_id, id) ON DELETE RESTRICT;

ALTER TABLE signup_requests
    ADD COLUMN department_id uuid,
    ADD CONSTRAINT signup_requests_department_fk
        FOREIGN KEY (institution_id, department_id)
        REFERENCES departments(institution_id, id) ON DELETE RESTRICT;
CREATE INDEX signup_requests_pending_department_idx
    ON signup_requests(institution_id, department_id, created_at)
    WHERE status = 'pending';
CREATE UNIQUE INDEX signup_requests_pending_identity_idx
    ON signup_requests(institution_id, lower(matric_no), lower(email), department_id)
    WHERE status = 'pending' AND department_id IS NOT NULL;

ALTER TABLE password_resets
    ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0);

ALTER TABLE email_outbox
    ADD COLUMN locked_at timestamptz;
CREATE INDEX email_outbox_sending_idx ON email_outbox(locked_at) WHERE status = 'sending';

-- +goose Down
DROP INDEX email_outbox_sending_idx;
ALTER TABLE email_outbox DROP COLUMN locked_at;
ALTER TABLE password_resets DROP COLUMN attempts;
DROP INDEX signup_requests_pending_department_idx;
DROP INDEX signup_requests_pending_identity_idx;
ALTER TABLE signup_requests DROP CONSTRAINT signup_requests_department_fk;
ALTER TABLE signup_requests DROP COLUMN department_id;
ALTER TABLE venues DROP CONSTRAINT venues_department_fk;
ALTER TABLE venues DROP COLUMN active, DROP COLUMN department_id;
