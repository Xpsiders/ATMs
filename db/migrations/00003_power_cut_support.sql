-- +goose Up
ALTER TABLE devices ADD COLUMN last_seen_at timestamptz;

CREATE TABLE device_heartbeats (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL,
    device_id uuid NOT NULL,
    heartbeat_at timestamptz NOT NULL,
    FOREIGN KEY (institution_id, device_id) REFERENCES devices(institution_id, id) ON DELETE CASCADE
);
CREATE INDEX device_heartbeats_device_time_idx
    ON device_heartbeats(institution_id, device_id, heartbeat_at);

ALTER TABLE lecture_sessions
    DROP CONSTRAINT lecture_sessions_check1;
ALTER TABLE lecture_sessions
    ADD CONSTRAINT lecture_sessions_code_storage_check
    CHECK (
      (rotating_code AND static_code IS NULL AND code_expires_at IS NULL)
      OR (NOT rotating_code AND static_code IS NOT NULL)
    );

CREATE TABLE lecture_session_scanner_status (
    institution_id uuid NOT NULL,
    session_id uuid NOT NULL,
    state text NOT NULL CHECK (state = 'unavailable'),
    note text NOT NULL,
    changed_by_user_id uuid NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (institution_id, session_id),
    FOREIGN KEY (institution_id, session_id) REFERENCES lecture_sessions(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, changed_by_user_id) REFERENCES users(institution_id, id) ON DELETE RESTRICT
);

ALTER TABLE attendance_overrides
    ADD COLUMN reason_code text NOT NULL DEFAULT 'other'
        CHECK (reason_code IN ('power_outage', 'device_failure', 'official_duty', 'medical', 'other')),
    ADD COLUMN note text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE attendance_overrides DROP COLUMN note, DROP COLUMN reason_code;
DROP TABLE lecture_session_scanner_status;
ALTER TABLE lecture_sessions DROP CONSTRAINT lecture_sessions_code_storage_check;
UPDATE lecture_sessions
SET code_expires_at = coalesce(ended_at, now())
WHERE NOT rotating_code AND code_expires_at IS NULL;
ALTER TABLE lecture_sessions
    ADD CONSTRAINT lecture_sessions_check1
    CHECK ((rotating_code AND static_code IS NULL AND code_expires_at IS NULL)
        OR (NOT rotating_code AND static_code IS NOT NULL AND code_expires_at IS NOT NULL));
DROP INDEX device_heartbeats_device_time_idx;
DROP TABLE device_heartbeats;
ALTER TABLE devices DROP COLUMN last_seen_at;
