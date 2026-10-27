-- +goose Up
-- Location stops being a gate for web check-in. A venue is a named room; its
-- coordinates are optional metadata that flag a suspicious check-in for the
-- lecturer but never refuse one. Students on cheap or indoor-bound handsets
-- were being denied attendance for a hardware limit they cannot fix.
ALTER TABLE venues
    ALTER COLUMN latitude DROP NOT NULL,
    ALTER COLUMN longitude DROP NOT NULL,
    ALTER COLUMN radius_meters DROP NOT NULL;

-- A session snapshots the venue's settings at start, so it inherits the same
-- optionality.
ALTER TABLE lecture_sessions
    ALTER COLUMN latitude DROP NOT NULL,
    ALTER COLUMN longitude DROP NOT NULL,
    ALTER COLUMN radius_meters DROP NOT NULL;

-- A check-in may now arrive with no position at all: the browser can refuse
-- permission, or the venue may have no centre to measure against.
ALTER TABLE web_checkins
    ALTER COLUMN latitude DROP NOT NULL,
    ALTER COLUMN longitude DROP NOT NULL,
    ALTER COLUMN accuracy_meters DROP NOT NULL,
    ALTER COLUMN distance_meters DROP NOT NULL;

-- Device binding replaces the geofence as the anti-proxy control: one device
-- may record at most one student per session. This index serves that lookup.
CREATE INDEX web_checkins_session_device_idx
    ON web_checkins(institution_id, session_id, device_hash)
    WHERE device_hash IS NOT NULL;

-- +goose Down
-- Restoring NOT NULL fails if any row has since been written without a
-- position. That is intentional: back-filling a placeholder coordinate would
-- invent a location the student was never at, and every report derives
-- location trust from these columns.
DROP INDEX IF EXISTS web_checkins_session_device_idx;

ALTER TABLE web_checkins
    ALTER COLUMN distance_meters SET NOT NULL,
    ALTER COLUMN accuracy_meters SET NOT NULL,
    ALTER COLUMN longitude SET NOT NULL,
    ALTER COLUMN latitude SET NOT NULL;

ALTER TABLE lecture_sessions
    ALTER COLUMN radius_meters SET NOT NULL,
    ALTER COLUMN longitude SET NOT NULL,
    ALTER COLUMN latitude SET NOT NULL;

ALTER TABLE venues
    ALTER COLUMN radius_meters SET NOT NULL,
    ALTER COLUMN longitude SET NOT NULL,
    ALTER COLUMN latitude SET NOT NULL;
