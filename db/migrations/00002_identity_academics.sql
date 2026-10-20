-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE faculties (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (institution_id, name)
);

CREATE TABLE departments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
    faculty_id uuid NOT NULL,
    name text NOT NULL,
    code text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (institution_id, code),
    FOREIGN KEY (institution_id, faculty_id) REFERENCES faculties(institution_id, id) ON DELETE RESTRICT
);

CREATE TABLE programmes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
    department_id uuid NOT NULL,
    name text NOT NULL,
    code text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (institution_id, code),
    FOREIGN KEY (institution_id, department_id) REFERENCES departments(institution_id, id) ON DELETE RESTRICT
);

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
    email text NOT NULL,
    password_hash text NOT NULL,
    display_name text NOT NULL,
    phone text,
    bound_device_hash text,
    force_password_change boolean NOT NULL DEFAULT false,
    email_verified_at timestamptz,
    disabled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (institution_id, email)
);
CREATE INDEX users_display_name_trgm_idx ON users USING gin(display_name gin_trgm_ops);
CREATE UNIQUE INDEX users_institution_email_lower_idx ON users(institution_id, lower(email));

CREATE TABLE student_records (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
    department_id uuid NOT NULL,
    programme_id uuid,
    matric_no text NOT NULL,
    email text NOT NULL,
    display_name text NOT NULL,
    level integer NOT NULL CHECK (level > 0),
    claimed_by_user_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (institution_id, matric_no),
    UNIQUE (institution_id, claimed_by_user_id),
    FOREIGN KEY (institution_id, department_id) REFERENCES departments(institution_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (institution_id, programme_id) REFERENCES programmes(institution_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (institution_id, claimed_by_user_id) REFERENCES users(institution_id, id) ON DELETE RESTRICT
);

CREATE TABLE staff_records (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
    department_id uuid NOT NULL,
    staff_no text NOT NULL,
    email text NOT NULL,
    display_name text NOT NULL,
    user_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (institution_id, staff_no),
    FOREIGN KEY (institution_id, department_id) REFERENCES departments(institution_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (institution_id, user_id) REFERENCES users(institution_id, id) ON DELETE RESTRICT
);

CREATE TABLE role_assignments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
    user_id uuid NOT NULL,
    role text NOT NULL CHECK (role IN ('platform_admin', 'institution_admin', 'faculty_admin', 'department_admin', 'lecturer', 'student')),
    scope_type text NOT NULL CHECK (scope_type IN ('platform', 'institution', 'faculty', 'department')),
    faculty_id uuid,
    department_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (institution_id, user_id, role, scope_type, faculty_id, department_id),
    FOREIGN KEY (institution_id, user_id) REFERENCES users(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, faculty_id) REFERENCES faculties(institution_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (institution_id, department_id) REFERENCES departments(institution_id, id) ON DELETE RESTRICT,
    CHECK ((scope_type = 'platform' AND faculty_id IS NULL AND department_id IS NULL)
        OR (scope_type = 'institution' AND faculty_id IS NULL AND department_id IS NULL)
        OR (scope_type = 'faculty' AND faculty_id IS NOT NULL AND department_id IS NULL)
        OR (scope_type = 'department' AND faculty_id IS NULL AND department_id IS NOT NULL))
);

CREATE TABLE refresh_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL,
    user_id uuid NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    replaced_by_id uuid REFERENCES refresh_tokens(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (institution_id, user_id) REFERENCES users(institution_id, id) ON DELETE CASCADE
);
CREATE INDEX refresh_tokens_user_idx ON refresh_tokens(institution_id, user_id);

CREATE TABLE email_verifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL,
    student_record_id uuid NOT NULL,
    code_hash bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (institution_id, student_record_id) REFERENCES student_records(institution_id, id) ON DELETE CASCADE
);

CREATE TABLE password_resets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL,
    user_id uuid NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (institution_id, user_id) REFERENCES users(institution_id, id) ON DELETE CASCADE
);

CREATE TABLE signup_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
    matric_no text NOT NULL,
    email text NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    reviewed_by_user_id uuid,
    reviewed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (institution_id, reviewed_by_user_id) REFERENCES users(institution_id, id) ON DELETE RESTRICT
);

CREATE TABLE invitations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
    email text NOT NULL,
    role text NOT NULL CHECK (role IN ('institution_admin', 'faculty_admin', 'department_admin', 'lecturer')),
    scope_type text NOT NULL CHECK (scope_type IN ('institution', 'faculty', 'department')),
    faculty_id uuid,
    department_id uuid,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    accepted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (institution_id, faculty_id) REFERENCES faculties(institution_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (institution_id, department_id) REFERENCES departments(institution_id, id) ON DELETE RESTRICT,
    CHECK ((scope_type = 'institution' AND faculty_id IS NULL AND department_id IS NULL)
        OR (scope_type = 'faculty' AND faculty_id IS NOT NULL AND department_id IS NULL)
        OR (scope_type = 'department' AND faculty_id IS NULL AND department_id IS NOT NULL))
);

CREATE TABLE academic_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
    name text NOT NULL,
    starts_on date NOT NULL,
    ends_on date NOT NULL,
    current boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    CHECK (ends_on >= starts_on)
);
CREATE UNIQUE INDEX academic_sessions_one_current ON academic_sessions(institution_id) WHERE current;

CREATE TABLE semesters (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL,
    academic_session_id uuid NOT NULL,
    name text NOT NULL,
    starts_on date NOT NULL,
    ends_on date NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, academic_session_id) REFERENCES academic_sessions(institution_id, id) ON DELETE CASCADE,
    CHECK (ends_on >= starts_on)
);

CREATE TABLE courses (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL,
    department_id uuid NOT NULL,
    code text NOT NULL,
    title text NOT NULL,
    kind text NOT NULL DEFAULT 'departmental' CHECK (kind IN ('departmental', 'general')),
    description text NOT NULL DEFAULT '',
    credits integer NOT NULL DEFAULT 0 CHECK (credits >= 0),
    search_vector tsvector GENERATED ALWAYS AS (
        to_tsvector('simple', coalesce(code, '') || ' ' || coalesce(title, '') || ' ' || coalesce(description, ''))
    ) STORED,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (institution_id, code),
    FOREIGN KEY (institution_id, department_id) REFERENCES departments(institution_id, id) ON DELETE RESTRICT
);
CREATE INDEX courses_search_vector_idx ON courses USING gin(search_vector);
CREATE INDEX courses_code_trgm_idx ON courses USING gin(code gin_trgm_ops);
CREATE INDEX courses_title_trgm_idx ON courses USING gin(title gin_trgm_ops);
CREATE INDEX departments_name_trgm_idx ON departments USING gin(name gin_trgm_ops);

CREATE TABLE course_offerings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL,
    course_id uuid NOT NULL,
    semester_id uuid NOT NULL,
    coordinator_user_id uuid,
    level integer NOT NULL CHECK (level > 0),
    registration_mode text NOT NULL CHECK (registration_mode IN ('auto', 'open', 'approval', 'closed')),
    roster_policy text NOT NULL DEFAULT 'registered_only' CHECK (roster_policy IN ('registered_only', 'open')),
    min_attendance_pct numeric(5,2) NOT NULL DEFAULT 75 CHECK (min_attendance_pct >= 0 AND min_attendance_pct <= 100),
    late_counts_as_attended boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, course_id) REFERENCES courses(institution_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (institution_id, semester_id) REFERENCES semesters(institution_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (institution_id, coordinator_user_id) REFERENCES users(institution_id, id) ON DELETE RESTRICT
);

CREATE TABLE offering_lecturers (
    institution_id uuid NOT NULL,
    offering_id uuid NOT NULL,
    lecturer_user_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (institution_id, offering_id, lecturer_user_id),
    FOREIGN KEY (institution_id, offering_id) REFERENCES course_offerings(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, lecturer_user_id) REFERENCES users(institution_id, id) ON DELETE RESTRICT
);
CREATE INDEX offering_lecturers_user_idx ON offering_lecturers(institution_id, lecturer_user_id);

CREATE TABLE offering_auto_targets (
    institution_id uuid NOT NULL,
    offering_id uuid NOT NULL,
    department_id uuid NOT NULL,
    level integer NOT NULL CHECK (level > 0),
    PRIMARY KEY (institution_id, offering_id, department_id, level),
    FOREIGN KEY (institution_id, offering_id) REFERENCES course_offerings(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, department_id) REFERENCES departments(institution_id, id) ON DELETE CASCADE
);

CREATE TABLE course_registrations (
    institution_id uuid NOT NULL,
    offering_id uuid NOT NULL,
    student_record_id uuid NOT NULL,
    status text NOT NULL CHECK (status IN ('pending', 'approved', 'rejected')),
    created_at timestamptz NOT NULL DEFAULT now(),
    approved_at timestamptz,
    PRIMARY KEY (institution_id, offering_id, student_record_id),
    FOREIGN KEY (institution_id, offering_id) REFERENCES course_offerings(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, student_record_id) REFERENCES student_records(institution_id, id) ON DELETE CASCADE
);
CREATE INDEX course_registrations_student_idx ON course_registrations(institution_id, student_record_id);
CREATE INDEX course_registrations_offering_idx ON course_registrations(institution_id, offering_id, status);

CREATE TABLE venues (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
    name text NOT NULL,
    latitude double precision NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude double precision NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    radius_meters integer NOT NULL CHECK (radius_meters > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id)
);

CREATE TABLE devices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL,
    venue_id uuid NOT NULL,
    name text NOT NULL,
    key_hash bytea NOT NULL UNIQUE,
    disabled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, venue_id) REFERENCES venues(institution_id, id) ON DELETE RESTRICT
);

CREATE TABLE device_slots (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL,
    device_id uuid NOT NULL,
    student_record_id uuid NOT NULL,
    slot_number integer NOT NULL CHECK (slot_number > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (institution_id, device_id, slot_number),
    FOREIGN KEY (institution_id, device_id) REFERENCES devices(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, student_record_id) REFERENCES student_records(institution_id, id) ON DELETE CASCADE
);

CREATE TABLE timetable (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL,
    offering_id uuid NOT NULL,
    venue_id uuid NOT NULL,
    weekday smallint NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    starts_at time NOT NULL,
    ends_at time NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, offering_id) REFERENCES course_offerings(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, venue_id) REFERENCES venues(institution_id, id) ON DELETE RESTRICT,
    CHECK (ends_at > starts_at)
);

CREATE TABLE lecture_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL,
    offering_id uuid NOT NULL,
    venue_id uuid NOT NULL,
    timetable_id uuid,
    started_by_user_id uuid NOT NULL,
    started_at timestamptz NOT NULL DEFAULT now(),
    ended_at timestamptz,
    latitude double precision NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude double precision NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    radius_meters integer NOT NULL CHECK (radius_meters > 0),
    rotating_code boolean NOT NULL DEFAULT true,
    static_code text,
    code_expires_at timestamptz,
    late_after_min integer NOT NULL DEFAULT 0 CHECK (late_after_min >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, offering_id) REFERENCES course_offerings(institution_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (institution_id, venue_id) REFERENCES venues(institution_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (institution_id, timetable_id) REFERENCES timetable(institution_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (institution_id, started_by_user_id) REFERENCES users(institution_id, id) ON DELETE RESTRICT,
    CHECK (ended_at IS NULL OR ended_at >= started_at),
    CHECK ((rotating_code AND static_code IS NULL AND code_expires_at IS NULL)
        OR (NOT rotating_code AND static_code IS NOT NULL AND code_expires_at IS NOT NULL))
);
CREATE INDEX lecture_sessions_offering_started_idx ON lecture_sessions(institution_id, offering_id, started_at DESC);
CREATE UNIQUE INDEX lecture_sessions_one_open_per_venue ON lecture_sessions(institution_id, venue_id) WHERE ended_at IS NULL;

CREATE TABLE web_checkins (
    institution_id uuid NOT NULL,
    session_id uuid NOT NULL,
    student_record_id uuid NOT NULL,
    latitude double precision NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude double precision NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    accuracy_meters double precision NOT NULL CHECK (accuracy_meters >= 0),
    distance_meters double precision NOT NULL CHECK (distance_meters >= 0),
    device_hash bytea,
    on_roster boolean NOT NULL,
    status text NOT NULL CHECK (status IN ('present', 'late')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, student_record_id),
    FOREIGN KEY (institution_id, session_id) REFERENCES lecture_sessions(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, student_record_id) REFERENCES student_records(institution_id, id) ON DELETE CASCADE
);
CREATE INDEX web_checkins_session_idx ON web_checkins(institution_id, session_id);

CREATE TABLE fingerprint_scans (
    institution_id uuid NOT NULL,
    session_id uuid NOT NULL,
    student_record_id uuid NOT NULL,
    device_id uuid NOT NULL,
    confidence numeric(5,4) NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    client_scanned_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, student_record_id),
    FOREIGN KEY (institution_id, session_id) REFERENCES lecture_sessions(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, student_record_id) REFERENCES student_records(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, device_id) REFERENCES devices(institution_id, id) ON DELETE RESTRICT
);
CREATE INDEX fingerprint_scans_session_idx ON fingerprint_scans(institution_id, session_id);

CREATE TABLE attendance_overrides (
    institution_id uuid NOT NULL,
    session_id uuid NOT NULL,
    student_record_id uuid NOT NULL,
    status text NOT NULL CHECK (status IN ('present', 'absent', 'late', 'excused')),
    reason text NOT NULL,
    changed_by_user_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, student_record_id),
    FOREIGN KEY (institution_id, session_id) REFERENCES lecture_sessions(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, student_record_id) REFERENCES student_records(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, changed_by_user_id) REFERENCES users(institution_id, id) ON DELETE RESTRICT
);

CREATE TABLE enrolment_tasks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL,
    device_id uuid NOT NULL,
    student_record_id uuid NOT NULL,
    slot_id uuid,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done', 'failed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    FOREIGN KEY (institution_id, device_id) REFERENCES devices(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, student_record_id) REFERENCES student_records(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, slot_id) REFERENCES device_slots(institution_id, id) ON DELETE RESTRICT
);

CREATE TABLE disputes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL,
    session_id uuid NOT NULL,
    student_record_id uuid NOT NULL,
    message text NOT NULL,
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'rejected')),
    resolved_by_user_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    FOREIGN KEY (institution_id, session_id) REFERENCES lecture_sessions(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, student_record_id) REFERENCES student_records(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, resolved_by_user_id) REFERENCES users(institution_id, id) ON DELETE RESTRICT
);

CREATE TABLE audit_log (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    institution_id uuid NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
    actor_user_id uuid,
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (institution_id, actor_user_id) REFERENCES users(institution_id, id) ON DELETE RESTRICT
);
CREATE INDEX audit_log_resource_idx ON audit_log(institution_id, resource_type, resource_id, created_at DESC);

CREATE TABLE email_outbox (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
    recipient text NOT NULL,
    subject text NOT NULL,
    body text NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sending', 'sent', 'failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX email_outbox_pending_idx ON email_outbox(available_at, created_at) WHERE status = 'pending';

-- +goose Down
DROP TABLE email_outbox;
DROP TABLE audit_log;
DROP TABLE disputes;
DROP TABLE enrolment_tasks;
DROP TABLE attendance_overrides;
DROP TABLE fingerprint_scans;
DROP TABLE web_checkins;
DROP TABLE lecture_sessions;
DROP TABLE timetable;
DROP TABLE device_slots;
DROP TABLE devices;
DROP TABLE venues;
DROP TABLE course_registrations;
DROP TABLE offering_auto_targets;
DROP TABLE offering_lecturers;
DROP TABLE course_offerings;
DROP TABLE courses;
DROP TABLE semesters;
DROP TABLE academic_sessions;
DROP TABLE invitations;
DROP TABLE signup_requests;
DROP TABLE password_resets;
DROP TABLE email_verifications;
DROP TABLE refresh_tokens;
DROP TABLE role_assignments;
DROP TABLE staff_records;
DROP TABLE student_records;
DROP TABLE users;
DROP TABLE programmes;
DROP TABLE departments;
DROP TABLE faculties;
DROP EXTENSION IF EXISTS pg_trgm;
