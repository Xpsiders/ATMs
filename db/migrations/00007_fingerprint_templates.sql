-- +goose Up
-- Central fingerprint templates so a student enrolled on one scanner can be
-- recognised on any scanner serving a session they are registered for
-- (Option B / multi-venue). The template is the sensor-exported blob; the
-- backend stores and redistributes it but never interprets it.
CREATE TABLE student_fingerprints (
    institution_id uuid NOT NULL,
    student_record_id uuid NOT NULL,
    template bytea NOT NULL,
    enrolled_by_device_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (institution_id, student_record_id),
    FOREIGN KEY (institution_id, student_record_id) REFERENCES student_records(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, enrolled_by_device_id) REFERENCES devices(institution_id, id) ON DELETE SET NULL
);

-- One device slot per student per device, so template sync can upsert a stable
-- integer position for each roster student without creating duplicates.
CREATE UNIQUE INDEX device_slots_one_per_student
    ON device_slots(institution_id, device_id, student_record_id);

-- +goose Down
DROP INDEX IF EXISTS device_slots_one_per_student;
DROP TABLE IF EXISTS student_fingerprints;
