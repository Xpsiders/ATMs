-- +goose Up
CREATE TABLE teaching_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL,
    lecturer_user_id uuid NOT NULL,
    course_id uuid NOT NULL,
    semester_id uuid NOT NULL,
    level integer NOT NULL CHECK (level > 0),
    registration_mode text NOT NULL CHECK (registration_mode IN ('auto', 'open', 'approval', 'closed')),
    roster_policy text NOT NULL CHECK (roster_policy IN ('registered_only', 'open')),
    min_attendance_pct numeric(5,2) NOT NULL CHECK (min_attendance_pct BETWEEN 0 AND 100),
    late_counts_as_attended boolean NOT NULL DEFAULT true,
    auto_targets jsonb NOT NULL DEFAULT '[]'::jsonb,
    timetable jsonb NOT NULL DEFAULT '[]'::jsonb,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    reviewed_by_user_id uuid,
    reviewed_at timestamptz,
    offering_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, lecturer_user_id) REFERENCES users(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, course_id) REFERENCES courses(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, semester_id) REFERENCES semesters(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, reviewed_by_user_id) REFERENCES users(institution_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (institution_id, offering_id) REFERENCES course_offerings(institution_id, id) ON DELETE RESTRICT
);
CREATE INDEX teaching_requests_pending_idx
    ON teaching_requests(institution_id, status, created_at);
CREATE UNIQUE INDEX teaching_requests_pending_lecturer_course_idx
    ON teaching_requests(institution_id, lecturer_user_id, course_id, semester_id)
    WHERE status = 'pending';

-- +goose Down
DROP TABLE teaching_requests;
