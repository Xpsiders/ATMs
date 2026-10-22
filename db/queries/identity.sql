-- name: LookupLogin :one
SELECT
    u.id AS user_id,
    u.institution_id,
    u.email,
    u.password_hash,
    u.display_name,
    u.force_password_change,
    u.email_verified_at,
    sr.id AS student_record_id,
    sr.department_id,
    sr.level
FROM users AS u
LEFT JOIN student_records AS sr
    ON sr.institution_id = u.institution_id
   AND sr.claimed_by_user_id = u.id
WHERE u.institution_id = $1
  AND lower(u.email) = lower($2)
  AND u.disabled_at IS NULL;

-- name: CreateRefreshToken :exec
INSERT INTO refresh_tokens (institution_id, user_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4);

-- name: RevokeRefreshToken :one
UPDATE refresh_tokens
SET revoked_at = $2
WHERE institution_id = $1
  AND token_hash = $3
  AND revoked_at IS NULL
  AND expires_at > $2
RETURNING user_id, institution_id;

-- name: LockRefreshToken :one
SELECT institution_id, user_id, expires_at, revoked_at
FROM refresh_tokens
WHERE token_hash = $1
FOR UPDATE;

-- name: LoadRefreshIdentity :one
SELECT
    u.id AS user_id,
    u.institution_id,
    u.force_password_change,
    sr.id AS student_record_id,
    sr.department_id,
    sr.level
FROM users AS u
LEFT JOIN student_records AS sr
  ON sr.institution_id = u.institution_id AND sr.claimed_by_user_id = u.id
WHERE u.institution_id = $1 AND u.id = $2 AND u.disabled_at IS NULL;

-- name: GetPasswordHash :one
SELECT password_hash
FROM users
WHERE institution_id = $1 AND id = $2 AND disabled_at IS NULL;

-- name: SetPasswordHash :execrows
UPDATE users
SET password_hash = $3, force_password_change = false
WHERE institution_id = $1 AND id = $2 AND disabled_at IS NULL;

-- name: GetUserProfile :one
SELECT id, display_name, email,
       CASE WHEN disabled_at IS NULL THEN 'active' ELSE 'disabled' END::text AS status,
       force_password_change
FROM users
WHERE institution_id = $1 AND id = $2;

-- name: GetUserRoles :many
SELECT assignment.role, assignment.scope_type,
       CASE assignment.scope_type
           WHEN 'institution' THEN assignment.institution_id
           WHEN 'faculty' THEN assignment.faculty_id
           WHEN 'department' THEN assignment.department_id
           ELSE NULL::uuid
       END AS scope_id,
       student.department_id,
       student.level
FROM role_assignments AS assignment
LEFT JOIN student_records AS student
  ON student.institution_id = assignment.institution_id
 AND student.claimed_by_user_id = assignment.user_id
 AND assignment.role = 'student'
WHERE assignment.institution_id = $1 AND assignment.user_id = $2
ORDER BY assignment.role, assignment.scope_type, scope_id;

-- name: FindStudentByMatric :one
SELECT id, institution_id, department_id, matric_no, email, display_name, level,
       claimed_by_user_id
FROM student_records
WHERE institution_id = $1
  AND lower(matric_no) = lower($2);

-- name: CreateSignupRequest :exec
INSERT INTO signup_requests (institution_id, matric_no, email)
VALUES ($1, $2, $3);

-- name: AutoRegisterClaimedStudent :execrows
WITH claimed_student AS (
    SELECT institution_id, id, department_id, level
    FROM student_records
    WHERE institution_id = sqlc.arg(institution_id)
      AND id = sqlc.arg(student_record_id)
)
INSERT INTO course_registrations (
    institution_id, offering_id, student_record_id, status, approved_at
)
SELECT student.institution_id, offering.id, student.id, 'approved', sqlc.arg(approved_at)
FROM claimed_student AS student
JOIN course_offerings AS offering
  ON offering.institution_id = student.institution_id
 AND offering.level = student.level
 AND offering.registration_mode = 'auto'
JOIN courses AS course
  ON course.institution_id = offering.institution_id
 AND course.id = offering.course_id
WHERE (
      (course.kind = 'departmental' AND course.department_id = student.department_id)
      OR (
          course.kind = 'general'
          AND EXISTS (
              SELECT 1
              FROM offering_auto_targets AS target
              WHERE target.institution_id = offering.institution_id
                AND target.offering_id = offering.id
                AND target.department_id = student.department_id
                AND target.level = student.level
          )
      )
  )
ON CONFLICT (institution_id, offering_id, student_record_id) DO UPDATE
SET status = 'approved', approved_at = EXCLUDED.approved_at
WHERE course_registrations.status <> 'approved';
-- name: CreateEmailVerification :exec
INSERT INTO email_verifications (institution_id, student_record_id, code_hash, expires_at)
VALUES ($1, $2, $3, $4);

-- name: ConsumeEmailVerification :one
UPDATE email_verifications AS target
SET consumed_at = $4
WHERE target.id = (
    SELECT candidate.id
    FROM email_verifications AS candidate
    WHERE candidate.institution_id = $1
      AND candidate.student_record_id = $2
      AND candidate.code_hash = $3
      AND candidate.consumed_at IS NULL
      AND candidate.expires_at > $4
    ORDER BY candidate.created_at DESC, candidate.id DESC
    LIMIT 1
    FOR UPDATE
)
RETURNING student_record_id;

-- name: CreateClaimedStudentUser :one
INSERT INTO users (institution_id, email, password_hash, display_name, email_verified_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id;

-- name: ClaimStudentRecord :one
UPDATE student_records
SET claimed_by_user_id = $3
WHERE institution_id = $1
  AND id = $2
  AND claimed_by_user_id IS NULL
RETURNING department_id, level;

-- name: GetStudentClaimTarget :one
SELECT email
FROM student_records
WHERE institution_id = $1 AND id = $2;

-- name: CreateStudentRole :exec
INSERT INTO role_assignments (
    institution_id, user_id, role, scope_type, department_id
)
VALUES ($1, $2, 'student', 'department', $3);

-- name: InvitationEmailExists :one
SELECT EXISTS (
    SELECT 1 FROM users
    WHERE institution_id = $1 AND lower(email) = lower($2)
);

-- name: CanInviteTarget :many
WITH target_department AS (
    SELECT department.id, department.faculty_id
    FROM departments AS department
    WHERE department.institution_id = $1 AND department.id = $6::uuid
)
SELECT caller.user_id
FROM role_assignments AS caller
WHERE caller.institution_id = $1
  AND caller.user_id = $2
  AND (
      (
          caller.role = 'institution_admin' AND caller.scope_type = 'institution'
          AND (
              ($3::text = 'institution_admin' AND $4::text = 'institution' AND $5::uuid IS NULL AND $6::uuid IS NULL)
              OR ($3::text = 'faculty_admin' AND $4::text = 'faculty' AND $5::uuid IS NOT NULL AND $6::uuid IS NULL
                  AND EXISTS (
                      SELECT 1 FROM faculties AS target_faculty
                      WHERE target_faculty.institution_id = $1 AND target_faculty.id = $5::uuid
                  ))
              OR ($3::text IN ('department_admin', 'lecturer') AND $4::text = 'department'
                  AND $5::uuid IS NULL AND EXISTS (SELECT 1 FROM target_department))
          )
      )
      OR (
          caller.role = 'faculty_admin' AND caller.scope_type = 'faculty'
          AND $3::text IN ('department_admin', 'lecturer') AND $4::text = 'department'
          AND $5::uuid IS NULL
          AND EXISTS (
              SELECT 1 FROM target_department AS target
              WHERE target.faculty_id = caller.faculty_id
          )
      )
      OR (
          caller.role = 'department_admin' AND caller.scope_type = 'department'
          AND $3::text = 'lecturer' AND $4::text = 'department'
          AND $5::uuid IS NULL AND caller.department_id = $6::uuid
          AND EXISTS (SELECT 1 FROM target_department)
      )
  )
LIMIT 1;

-- name: CreateInvitation :one
WITH target_department AS (
    SELECT department.id, department.faculty_id
    FROM departments AS department
    WHERE department.institution_id = $1 AND department.id = $6
)
INSERT INTO invitations (
    institution_id, email, role, scope_type, faculty_id, department_id,
    token_hash, expires_at
)
SELECT $1, lower($2), $3, $4, $5, $6, $7, $8
WHERE (
    ($3 = 'institution_admin' AND $4 = 'institution' AND $5 IS NULL AND $6 IS NULL)
    OR ($3 = 'faculty_admin' AND $4 = 'faculty' AND $5 IS NOT NULL AND $6 IS NULL
        AND EXISTS (
            SELECT 1 FROM faculties AS target_faculty
            WHERE target_faculty.institution_id = $1 AND target_faculty.id = $5
        ))
    OR ($3 IN ('department_admin', 'lecturer') AND $4 = 'department'
        AND $5 IS NULL AND EXISTS (SELECT 1 FROM target_department))
)
AND (
    EXISTS (
        SELECT 1 FROM role_assignments AS institution_caller
        WHERE institution_caller.institution_id = $1
          AND institution_caller.user_id = $9
          AND institution_caller.role = 'institution_admin'
          AND institution_caller.scope_type = 'institution'
    )
    OR (
        $3 IN ('department_admin', 'lecturer')
        AND EXISTS (
            SELECT 1 FROM target_department AS target
            JOIN role_assignments AS caller
              ON caller.institution_id = $1
             AND caller.user_id = $9
             AND caller.role = 'faculty_admin'
             AND caller.scope_type = 'faculty'
             AND caller.faculty_id = target.faculty_id
        )
    )
    OR (
        $3 = 'lecturer'
        AND EXISTS (
            SELECT 1 FROM role_assignments AS department_caller
            WHERE department_caller.institution_id = $1
              AND department_caller.user_id = $9
              AND department_caller.role = 'department_admin'
              AND department_caller.scope_type = 'department'
              AND department_caller.department_id = $6
        )
    )
)
RETURNING id, expires_at;

-- name: LockInvitationForAcceptance :one
SELECT id, institution_id, email, role, scope_type, faculty_id, department_id
FROM invitations
WHERE institution_id = $1 AND token_hash = $2 AND accepted_at IS NULL AND expires_at > $3
FOR UPDATE;

-- name: CreateInvitedUser :one
INSERT INTO users (institution_id, email, password_hash, display_name, email_verified_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id;

-- name: CreateInvitedRole :exec
INSERT INTO role_assignments (
    institution_id, user_id, role, scope_type, faculty_id, department_id
)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: MarkInvitationAccepted :execrows
UPDATE invitations
SET accepted_at = $3
WHERE institution_id = $1 AND id = $2
  AND accepted_at IS NULL AND expires_at > $3;

-- name: CreateInvitationAudit :exec
INSERT INTO audit_log (institution_id, actor_user_id, action, resource_type, resource_id, details)
VALUES (
    $1, $2, 'role_assigned', 'user', $2::text,
    jsonb_build_object(
        'role', $3::text, 'scope_type', $4::text,
        'faculty_id', $5::uuid, 'department_id', $6::uuid
    )
);

-- name: InsertWebCheckin :one
INSERT INTO web_checkins (
    institution_id, session_id, student_record_id, latitude, longitude,
    accuracy_meters, distance_meters, device_hash, on_roster, status
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (session_id, student_record_id) DO NOTHING
RETURNING session_id, student_record_id, status, created_at;

-- name: FindDeviceByKeyHash :one
SELECT id, institution_id, venue_id
FROM devices
WHERE key_hash = $1
  AND disabled_at IS NULL;

-- name: TouchDeviceHeartbeat :exec
WITH touched AS (
    UPDATE devices AS device
    SET last_seen_at = $3
    WHERE device.institution_id = $1 AND device.id = $2 AND device.disabled_at IS NULL
    RETURNING device.institution_id, device.id
)
INSERT INTO device_heartbeats (institution_id, device_id, heartbeat_at)
SELECT touched.institution_id, touched.id, $3 FROM touched;

-- name: FindDeviceSlotStudent :one
SELECT student_record_id
FROM device_slots
WHERE institution_id = $1
  AND device_id = $2
  AND id = $3;

-- name: GetDeviceTasks :one
SELECT
    active.id AS active_session_id,
    active.venue_id AS active_venue_id,
    pending.id AS pending_enrolment_id,
    student.matric_no AS pending_student_ref,
    pending.slot_id AS pending_slot_id
FROM devices AS device
LEFT JOIN LATERAL (
    SELECT id, venue_id
    FROM lecture_sessions
    WHERE institution_id = device.institution_id
      AND venue_id = device.venue_id
      AND ended_at IS NULL
    ORDER BY started_at DESC
    LIMIT 1
) AS active ON true
LEFT JOIN LATERAL (
    SELECT id, student_record_id, slot_id
    FROM enrolment_tasks
    WHERE institution_id = device.institution_id
      AND device_id = device.id
      AND status = 'pending'
    ORDER BY created_at, id
    LIMIT 1
) AS pending ON true
LEFT JOIN student_records AS student
  ON student.institution_id = device.institution_id
 AND student.id = pending.student_record_id
WHERE device.institution_id = $1
  AND device.id = $2
  AND device.disabled_at IS NULL;

-- name: CompleteEnrolmentTask :execrows
UPDATE enrolment_tasks
SET status = $4, completed_at = $5
WHERE institution_id = $1
  AND device_id = $2
  AND id = $3
  AND status = 'pending';

-- name: FindActiveSessionAtVenue :one
SELECT id, venue_id, institution_id
FROM lecture_sessions
WHERE institution_id = $1
  AND venue_id = $2
  AND ended_at IS NULL
ORDER BY started_at DESC
LIMIT 1;

-- name: FindSessionAtVenueTime :one
SELECT id
FROM lecture_sessions
WHERE institution_id = $1
  AND venue_id = $2
  AND started_at <= $3
  AND (ended_at IS NULL OR ended_at > $3)
ORDER BY started_at DESC
LIMIT 1;

-- name: LoadCheckinSession :one
SELECT
    ls.id,
    ls.ended_at,
    ls.started_at,
    ls.latitude,
    ls.longitude,
    ls.radius_meters,
    ls.rotating_code,
    ls.static_code,
    ls.code_expires_at,
    ls.late_after_min,
    o.roster_policy
FROM lecture_sessions AS ls
JOIN course_offerings AS o
  ON o.institution_id = ls.institution_id AND o.id = ls.offering_id
WHERE ls.institution_id = $1
  AND ls.id = $2;

-- name: IsStudentRegistered :one
SELECT EXISTS (
    SELECT 1
    FROM course_registrations AS cr
    JOIN lecture_sessions AS ls
      ON ls.institution_id = cr.institution_id
     AND ls.offering_id = cr.offering_id
    WHERE cr.institution_id = $1
      AND ls.id = $2
      AND cr.student_record_id = $3
      AND cr.status = 'approved'
);

-- name: HasWebCheckin :one
SELECT EXISTS (
    SELECT 1 FROM web_checkins
    WHERE institution_id = $1 AND session_id = $2 AND student_record_id = $3
);

-- name: InsertFingerprintScan :one
INSERT INTO fingerprint_scans (
    institution_id, session_id, student_record_id, device_id, confidence, client_scanned_at
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (session_id, student_record_id) DO NOTHING
RETURNING session_id, student_record_id, created_at;

-- name: CreateLectureSession :one
INSERT INTO lecture_sessions (
    institution_id, offering_id, venue_id, timetable_id, started_by_user_id,
    started_at, latitude, longitude, radius_meters, rotating_code, static_code,
    code_expires_at, late_after_min
)
SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
WHERE EXISTS (
    SELECT 1 FROM venues
    WHERE institution_id = $1 AND id = $3 AND active = true
)
ON CONFLICT (institution_id, venue_id) WHERE ended_at IS NULL DO NOTHING
RETURNING id, institution_id, offering_id, venue_id, started_at, ended_at,
          latitude, longitude, radius_meters, rotating_code, static_code,
          code_expires_at, late_after_min;

-- name: GetLectureSession :one
SELECT id, institution_id, offering_id, venue_id, started_at, ended_at,
       latitude, longitude, radius_meters, rotating_code, static_code,
       code_expires_at, late_after_min
FROM lecture_sessions
WHERE institution_id = $1 AND id = $2;

-- name: EndLectureSession :one
UPDATE lecture_sessions
SET ended_at = $3
WHERE institution_id = $1 AND id = $2 AND ended_at IS NULL
RETURNING id, institution_id, offering_id, venue_id, started_at, ended_at,
          latitude, longitude, radius_meters, rotating_code, static_code,
          code_expires_at, late_after_min;

-- name: ConvertRotatingSessionToFixed :one
UPDATE lecture_sessions
SET rotating_code = false, static_code = $3, code_expires_at = NULL
WHERE institution_id = $1 AND id = $2
  AND ended_at IS NULL AND rotating_code = true
RETURNING id, institution_id, offering_id, venue_id, started_at, ended_at,
          latitude, longitude, radius_meters, rotating_code, static_code,
          code_expires_at, late_after_min;

-- name: InsertAuditLog :exec
INSERT INTO audit_log (institution_id, actor_user_id, action, resource_type, resource_id, details, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetVenueSettings :one
SELECT latitude, longitude, radius_meters
FROM venues
WHERE institution_id = $1 AND id = $2 AND active = true;

-- name: IsTimetableForOfferingVenue :one
SELECT EXISTS (
    SELECT 1
    FROM timetable
    JOIN venues AS venue
      ON venue.institution_id = timetable.institution_id
     AND venue.id = timetable.venue_id
    WHERE timetable.institution_id = $1
      AND timetable.id = $2
      AND timetable.offering_id = $3
      AND timetable.venue_id = $4
      AND venue.active = true
);

-- name: CanManageOffering :one
SELECT EXISTS (
    SELECT 1
    FROM course_offerings AS o
    WHERE o.institution_id = $1
      AND o.id = $2
      AND (
        o.coordinator_user_id = $3
        OR EXISTS (
          SELECT 1 FROM offering_lecturers AS ol
          WHERE ol.institution_id = o.institution_id
            AND ol.offering_id = o.id
            AND ol.lecturer_user_id = $3
        )
      )
);

-- name: CanManageSession :one
SELECT EXISTS (
    SELECT 1
    FROM lecture_sessions AS ls
    JOIN course_offerings AS o
      ON o.institution_id = ls.institution_id AND o.id = ls.offering_id
    WHERE ls.institution_id = $1
      AND ls.id = $2
      AND (
        o.coordinator_user_id = $3
        OR EXISTS (
          SELECT 1 FROM offering_lecturers AS ol
          WHERE ol.institution_id = o.institution_id
            AND ol.offering_id = o.id
            AND ol.lecturer_user_id = $3
        )
      )
);

-- name: ListStudentLiveClasses :many
SELECT ls.id AS session_id, ls.offering_id, c.code AS course_code,
       c.title AS course_title, ls.venue_id, v.name AS venue_name,
       lecturer.display_name AS lecturer_name,
       ls.started_at, ls.late_after_min, ls.rotating_code,
       EXISTS (
         SELECT 1 FROM web_checkins AS wc
         WHERE wc.institution_id = ls.institution_id
           AND wc.session_id = ls.id
           AND wc.student_record_id = student.id
       ) OR EXISTS (
         SELECT 1 FROM fingerprint_scans AS fs
         WHERE fs.institution_id = ls.institution_id
           AND fs.session_id = ls.id
           AND fs.student_record_id = student.id
       ) AS already_checked_in
FROM lecture_sessions AS ls
JOIN course_offerings AS o
  ON o.institution_id = ls.institution_id AND o.id = ls.offering_id
JOIN courses AS c
  ON c.institution_id = o.institution_id AND c.id = o.course_id
JOIN venues AS v
  ON v.institution_id = ls.institution_id AND v.id = ls.venue_id
JOIN student_records AS student
  ON student.institution_id = ls.institution_id
 AND student.id = $3
 AND student.claimed_by_user_id = $2
JOIN course_registrations AS registration
  ON registration.institution_id = student.institution_id
 AND registration.student_record_id = student.id
 AND registration.offering_id = o.id
 AND registration.status = 'approved'
JOIN users AS lecturer
  ON lecturer.institution_id = ls.institution_id
 AND lecturer.id = ls.started_by_user_id
WHERE ls.institution_id = $1
  AND ls.ended_at IS NULL
ORDER BY ls.started_at DESC, ls.id;

-- name: CanListVenues :one
SELECT EXISTS (
    SELECT 1 FROM role_assignments
    WHERE institution_id = $1 AND user_id = $2
      AND role IN ('platform_admin', 'institution_admin', 'faculty_admin', 'department_admin', 'lecturer')
);

-- name: ListVenues :many
SELECT id, name, latitude, longitude, radius_meters
FROM venues
WHERE institution_id = $1
ORDER BY name, id
LIMIT $2 OFFSET $3;

-- name: CountVenues :one
SELECT count(*)
FROM venues
WHERE institution_id = $1;

-- name: SearchOfferingsFullText :many
SELECT
    o.id AS offering_id,
    c.id AS course_id,
    c.code,
    c.title,
    c.kind,
    c.department_id,
    d.name AS department_name,
    s.id AS semester_id,
    s.name AS semester,
    (
      coalesce(ts_rank(c.search_vector, websearch_to_tsquery('simple', sqlc.arg(q))), 0)
        + greatest(similarity(c.code, sqlc.arg(q)), similarity(c.title, sqlc.arg(q)),
          similarity(d.name, sqlc.arg(q)),
          coalesce(max(similarity(lu.display_name, sqlc.arg(q))), 0))
    )::double precision AS rank
FROM course_offerings AS o
JOIN courses AS c
  ON c.institution_id = o.institution_id AND c.id = o.course_id
JOIN departments AS d
  ON d.institution_id = c.institution_id AND d.id = c.department_id
JOIN semesters AS s
  ON s.institution_id = o.institution_id AND s.id = o.semester_id
LEFT JOIN offering_lecturers AS ol
  ON ol.institution_id = o.institution_id AND ol.offering_id = o.id
LEFT JOIN users AS lu
  ON lu.institution_id = ol.institution_id AND lu.id = ol.lecturer_user_id
WHERE o.institution_id = sqlc.arg(institution_id)
  AND (
    EXISTS (
      SELECT 1
      FROM role_assignments AS reader_role
      JOIN departments AS reader_department
        ON reader_department.institution_id = reader_role.institution_id
      WHERE reader_role.institution_id = o.institution_id
        AND reader_role.user_id = sqlc.arg(user_id)
        AND reader_department.id = c.department_id
        AND (
          (reader_role.role = 'department_admin'
            AND reader_role.scope_type = 'department'
            AND reader_role.department_id = reader_department.id)
          OR (reader_role.role = 'faculty_admin'
            AND reader_role.scope_type = 'faculty'
            AND reader_role.faculty_id = reader_department.faculty_id)
        )
    )
    OR EXISTS (
      SELECT 1 FROM role_assignments AS broad_reader
      WHERE broad_reader.institution_id = o.institution_id
        AND broad_reader.user_id = sqlc.arg(user_id)
        AND ((broad_reader.role = 'institution_admin' AND broad_reader.scope_type = 'institution')
          OR broad_reader.role = 'platform_admin')
    )
    OR EXISTS (
      SELECT 1 FROM offering_lecturers AS reader_lecturer
      WHERE reader_lecturer.institution_id = o.institution_id
        AND reader_lecturer.offering_id = o.id
        AND reader_lecturer.lecturer_user_id = sqlc.arg(user_id)
    )
    OR o.coordinator_user_id = sqlc.arg(user_id)
    OR EXISTS (
      SELECT 1
      FROM role_assignments AS scoped_admin
      JOIN departments AS admin_department
        ON admin_department.institution_id = scoped_admin.institution_id
       AND ((scoped_admin.role = 'department_admin'
          AND admin_department.id = scoped_admin.department_id)
         OR (scoped_admin.role = 'faculty_admin'
          AND admin_department.faculty_id = scoped_admin.faculty_id))
      JOIN student_records AS registered_student
        ON registered_student.institution_id = scoped_admin.institution_id
       AND registered_student.department_id = admin_department.id
      JOIN course_registrations AS registration
        ON registration.institution_id = registered_student.institution_id
       AND registration.student_record_id = registered_student.id
       AND registration.offering_id = o.id
      WHERE scoped_admin.institution_id = o.institution_id
        AND scoped_admin.user_id = sqlc.arg(user_id)
        AND ((scoped_admin.role = 'department_admin' AND scoped_admin.scope_type = 'department')
          OR (scoped_admin.role = 'faculty_admin' AND scoped_admin.scope_type = 'faculty'))
        AND c.kind = 'general'
        AND registration.status IN ('approved', 'pending')
    )
    OR EXISTS (
      SELECT 1
      FROM role_assignments AS scoped_admin
      JOIN departments AS admin_department
        ON admin_department.institution_id = scoped_admin.institution_id
       AND ((scoped_admin.role = 'department_admin'
          AND admin_department.id = scoped_admin.department_id)
         OR (scoped_admin.role = 'faculty_admin'
          AND admin_department.faculty_id = scoped_admin.faculty_id))
      JOIN offering_auto_targets AS target
        ON target.institution_id = o.institution_id
       AND target.offering_id = o.id
       AND target.department_id = admin_department.id
       AND target.level = o.level
      WHERE scoped_admin.institution_id = o.institution_id
        AND scoped_admin.user_id = sqlc.arg(user_id)
        AND c.kind = 'general'
        AND ((scoped_admin.role = 'department_admin' AND scoped_admin.scope_type = 'department')
          OR (scoped_admin.role = 'faculty_admin' AND scoped_admin.scope_type = 'faculty'))
    )
    OR EXISTS (
      SELECT 1 FROM student_records AS home
      WHERE home.institution_id = o.institution_id
        AND home.id = sqlc.narg(student_id)::uuid
        AND home.claimed_by_user_id = sqlc.arg(user_id)
        AND home.level = o.level
        AND (
          c.department_id = home.department_id
          OR (c.kind = 'general' AND EXISTS (
            SELECT 1 FROM offering_auto_targets AS target
            WHERE target.institution_id = o.institution_id
              AND target.offering_id = o.id
              AND target.department_id = home.department_id
              AND target.level = home.level
          ))
        )
    )
  )
  AND (
    c.search_vector @@ websearch_to_tsquery('simple', sqlc.arg(q))
    OR c.code % sqlc.arg(q) OR c.title % sqlc.arg(q) OR d.name % sqlc.arg(q)
    OR EXISTS (
      SELECT 1 FROM offering_lecturers AS search_ol
      JOIN users AS search_user
        ON search_user.institution_id = search_ol.institution_id
       AND search_user.id = search_ol.lecturer_user_id
      WHERE search_ol.institution_id = o.institution_id
        AND search_ol.offering_id = o.id
        AND search_user.display_name % sqlc.arg(q)
    )
  )
  AND (
    sqlc.narg(department_id)::uuid IS NULL
    OR c.department_id = sqlc.narg(department_id)::uuid
    OR (c.kind = 'general' AND EXISTS (
      SELECT 1 FROM offering_auto_targets AS target
      WHERE target.institution_id = o.institution_id
        AND target.offering_id = o.id
        AND target.department_id = sqlc.narg(department_id)::uuid
        AND target.level = o.level
    ))
  )
  AND (sqlc.narg(level)::integer IS NULL OR o.level = sqlc.narg(level)::integer)
  AND (sqlc.narg(kind)::text IS NULL OR c.kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(semester)::uuid IS NULL OR o.semester_id = sqlc.narg(semester)::uuid)
GROUP BY o.id, c.id, d.id, s.id
ORDER BY rank DESC, c.code
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: SessionReportPage :many
WITH session_info AS (
    SELECT ls.institution_id, ls.id AS session_id, ls.offering_id,
           EXISTS (
               SELECT 1 FROM devices AS scanner
               WHERE scanner.institution_id = ls.institution_id
                 AND scanner.venue_id = ls.venue_id
                 AND scanner.disabled_at IS NULL
           ) AS scanner_available
    FROM lecture_sessions AS ls
    WHERE ls.institution_id = $1 AND ls.id = $2
),
candidates AS (
    SELECT cr.student_record_id
    FROM course_registrations AS cr
    JOIN session_info AS si
      ON si.institution_id = cr.institution_id AND si.offering_id = cr.offering_id
    WHERE cr.institution_id = $1 AND cr.status = 'approved'
    UNION
    SELECT wc.student_record_id
    FROM web_checkins AS wc
    WHERE wc.institution_id = $1 AND wc.session_id = $2
    UNION
    SELECT fs.student_record_id
    FROM fingerprint_scans AS fs
    WHERE fs.institution_id = $1 AND fs.session_id = $2
    UNION
    SELECT ao.student_record_id
    FROM attendance_overrides AS ao
    WHERE ao.institution_id = $1 AND ao.session_id = $2
)
SELECT
    sr.id AS student_record_id,
    sr.matric_no,
    sr.display_name,
    sr.department_id,
    sr.level,
    coalesce(cr.status = 'approved', false)::boolean AS on_roster,
    (wc.student_record_id IS NOT NULL)::boolean AS has_web_checkin,
    (fs.student_record_id IS NOT NULL)::boolean AS has_fingerprint,
    si.scanner_available,
    ao.status AS override_status,
    wc.status AS web_status,
    ao.reason AS override_reason,
    wc.created_at AS web_checkin_at,
    coalesce(fs.client_scanned_at, fs.created_at) AS fingerprint_scanned_at
FROM candidates AS c
JOIN student_records AS sr
  ON sr.institution_id = $1 AND sr.id = c.student_record_id
CROSS JOIN session_info AS si
LEFT JOIN course_registrations AS cr
  ON cr.institution_id = $1
 AND cr.offering_id = si.offering_id
 AND cr.student_record_id = sr.id
LEFT JOIN web_checkins AS wc
  ON wc.institution_id = $1
 AND wc.session_id = $2
 AND wc.student_record_id = sr.id
LEFT JOIN fingerprint_scans AS fs
  ON fs.institution_id = $1
 AND fs.session_id = $2
 AND fs.student_record_id = sr.id
LEFT JOIN attendance_overrides AS ao
  ON ao.institution_id = $1
 AND ao.session_id = $2
 AND ao.student_record_id = sr.id
WHERE ($3::uuid IS NULL OR sr.department_id = $3)
  AND ($6::uuid IS NULL OR sr.id = $6)
ORDER BY sr.matric_no
LIMIT $4 OFFSET $5;

-- name: SessionRosterAccess :one
WITH target AS (
    SELECT ls.institution_id, c.kind, d.id AS owning_department_id, d.faculty_id
    FROM lecture_sessions AS ls
    JOIN course_offerings AS o
      ON o.institution_id = ls.institution_id AND o.id = ls.offering_id
    JOIN courses AS c
      ON c.institution_id = o.institution_id AND c.id = o.course_id
    JOIN departments AS d
      ON d.institution_id = c.institution_id AND d.id = c.department_id
    WHERE ls.institution_id = $1 AND ls.id = $2
),
access AS (
    SELECT
      EXISTS (
        SELECT 1 FROM course_offerings AS o
        JOIN offering_lecturers AS ol
          ON ol.institution_id = o.institution_id AND ol.offering_id = o.id
        WHERE o.institution_id = $1 AND o.id = $2 AND ol.lecturer_user_id = $3
      ) AS lecturer,
      EXISTS (
        SELECT 1 FROM course_offerings AS o
        WHERE o.institution_id = $1 AND o.id = $2 AND o.coordinator_user_id = $3
      ) AS coordinator,
      EXISTS (
        SELECT 1 FROM role_assignments AS ra
        WHERE ra.institution_id = $1 AND ra.user_id = $3 AND ra.role IN ('platform_admin', 'institution_admin')
      ) AS institution_admin,
      EXISTS (
        SELECT 1 FROM role_assignments AS ra, target
        WHERE ra.institution_id = $1 AND ra.user_id = $3 AND ra.role = 'faculty_admin'
          AND ra.faculty_id = target.faculty_id
      ) AS faculty_admin,
      EXISTS (
        SELECT 1 FROM role_assignments AS ra, target
        WHERE ra.institution_id = $1 AND ra.user_id = $3 AND ra.role = 'department_admin'
          AND ra.department_id = target.owning_department_id
      ) AS owning_department_admin,
      EXISTS (
        SELECT 1 FROM role_assignments AS ra, target
        WHERE ra.institution_id = $1 AND ra.user_id = $3 AND ra.role = 'department_admin'
          AND target.kind = 'general'
      ) AS general_department_admin,
      EXISTS (
        SELECT 1 FROM student_records AS sr
        JOIN course_registrations AS cr
          ON cr.institution_id = sr.institution_id AND cr.student_record_id = sr.id
        JOIN lecture_sessions AS ls
          ON ls.institution_id = cr.institution_id AND ls.offering_id = cr.offering_id
        WHERE sr.institution_id = $1 AND sr.claimed_by_user_id = $3
          AND cr.status = 'approved' AND ls.id = $2
      ) AS registered_student
)
SELECT
    (lecturer OR coordinator OR institution_admin OR faculty_admin OR owning_department_admin
      OR general_department_admin OR registered_student)::boolean AS can_view,
    CASE
      WHEN lecturer OR coordinator OR institution_admin OR faculty_admin OR owning_department_admin THEN NULL::uuid
      WHEN general_department_admin THEN (
        SELECT ra.department_id FROM role_assignments AS ra
        WHERE ra.institution_id = $1 AND ra.user_id = $3 AND ra.role = 'department_admin'
        ORDER BY ra.department_id LIMIT 1
      )
      ELSE NULL::uuid
    END AS department_filter,
    CASE
      WHEN lecturer OR coordinator OR institution_admin OR faculty_admin OR owning_department_admin
        OR general_department_admin THEN NULL::uuid
      ELSE (
        SELECT sr.id FROM student_records AS sr
        WHERE sr.institution_id = $1 AND sr.claimed_by_user_id = $3 LIMIT 1
      )
    END::text AS student_filter,
    (lecturer OR coordinator OR institution_admin OR faculty_admin OR owning_department_admin
      OR general_department_admin)::boolean AS can_override
FROM access;

-- name: SessionRosterPage :many
SELECT sr.id AS student_record_id, sr.matric_no, sr.display_name, sr.department_id,
       sr.level, d.name AS department_name
FROM lecture_sessions AS ls
JOIN course_offerings AS o
  ON o.institution_id = ls.institution_id AND o.id = ls.offering_id
JOIN course_registrations AS cr
  ON cr.institution_id = o.institution_id AND cr.offering_id = o.id AND cr.status = 'approved'
JOIN student_records AS sr
  ON sr.institution_id = cr.institution_id AND sr.id = cr.student_record_id
JOIN departments AS d
  ON d.institution_id = sr.institution_id AND d.id = sr.department_id
WHERE ls.institution_id = $1 AND ls.id = $2
  AND ($3::uuid IS NULL OR sr.department_id = $3)
  AND ($6::uuid IS NULL OR sr.id = $6)
ORDER BY sr.matric_no, sr.id
LIMIT $4 OFFSET $5;

-- name: SessionRosterCount :one
SELECT count(*)::bigint
FROM lecture_sessions AS ls
JOIN course_offerings AS o
  ON o.institution_id = ls.institution_id AND o.id = ls.offering_id
JOIN course_registrations AS cr
  ON cr.institution_id = o.institution_id AND cr.offering_id = o.id AND cr.status = 'approved'
JOIN student_records AS sr
  ON sr.institution_id = cr.institution_id AND sr.id = cr.student_record_id
WHERE ls.institution_id = $1 AND ls.id = $2
  AND ($3::uuid IS NULL OR sr.department_id = $3);

-- name: GetSessionCoverageMetadata :one
SELECT ls.venue_id, ls.started_at, ls.ended_at,
       EXISTS (
         SELECT 1 FROM lecture_session_scanner_status AS status
         WHERE status.institution_id = ls.institution_id AND status.session_id = ls.id
           AND status.state = 'unavailable'
       )::boolean AS manually_unavailable,
       EXISTS (
         SELECT 1 FROM devices AS device
         WHERE device.institution_id = ls.institution_id
           AND device.venue_id = ls.venue_id
           AND device.disabled_at IS NULL
       )::boolean AS scanner_available
FROM lecture_sessions AS ls
WHERE ls.institution_id = $1 AND ls.id = $2;

-- name: ListSessionCoverageDevices :many
SELECT id
FROM devices
WHERE institution_id = $1 AND venue_id = $2 AND disabled_at IS NULL
ORDER BY id;

-- name: ListSessionHeartbeats :many
SELECT heartbeat.device_id, heartbeat.heartbeat_at
FROM device_heartbeats AS heartbeat
JOIN devices AS device
  ON device.institution_id = heartbeat.institution_id AND device.id = heartbeat.device_id
WHERE heartbeat.institution_id = $1
  AND device.venue_id = $2
  AND device.disabled_at IS NULL
  AND heartbeat.heartbeat_at >= $3
  AND heartbeat.heartbeat_at <= $4
ORDER BY heartbeat.device_id, heartbeat.heartbeat_at;

-- name: IsSessionEnded :one
SELECT CASE WHEN ended_at IS NOT NULL THEN true ELSE false END AS ended
FROM lecture_sessions
WHERE institution_id = $1 AND id = $2;

-- name: GetManualScannerUnavailableAt :one
SELECT created_at
FROM lecture_session_scanner_status
WHERE institution_id = $1 AND session_id = $2 AND state = 'unavailable';

-- name: IsStudentInOverrideScope :one
SELECT EXISTS (
    SELECT 1 FROM student_records
    WHERE institution_id = $1 AND id = $2
      AND ($3::uuid IS NULL OR department_id = $3)
);

-- name: UpsertAttendanceOverride :exec
INSERT INTO attendance_overrides (
    institution_id, session_id, student_record_id, status, reason,
    reason_code, note, changed_by_user_id, created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (session_id, student_record_id) DO UPDATE
SET status = EXCLUDED.status,
    reason = EXCLUDED.reason,
    reason_code = EXCLUDED.reason_code,
    note = EXCLUDED.note,
    changed_by_user_id = EXCLUDED.changed_by_user_id,
    created_at = EXCLUDED.created_at;

-- name: SetScannerUnavailable :exec
INSERT INTO lecture_session_scanner_status (
    institution_id, session_id, state, note, changed_by_user_id, created_at
)
VALUES ($1, $2, 'unavailable', $3, $4, $5)
ON CONFLICT (institution_id, session_id) DO UPDATE
SET state = EXCLUDED.state, note = EXCLUDED.note,
    changed_by_user_id = EXCLUDED.changed_by_user_id, created_at = EXCLUDED.created_at;
