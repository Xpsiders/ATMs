-- name: LoadRegistrationContext :one
SELECT o.registration_mode, o.roster_policy, sr.department_id, sr.level,
       EXISTS (
           SELECT 1 FROM course_registrations AS cr
           WHERE cr.institution_id = o.institution_id AND cr.offering_id = o.id
             AND cr.student_record_id = sr.id AND cr.status = 'approved'
       ) AS registered,
       EXISTS (
           SELECT 1 FROM offering_auto_targets AS target
           WHERE target.institution_id = o.institution_id AND target.offering_id = o.id
             AND target.department_id = sr.department_id AND target.level = sr.level
       ) AS auto_target
FROM course_offerings AS o
JOIN student_records AS sr
  ON sr.institution_id = o.institution_id AND sr.id = $3
WHERE o.institution_id = $1 AND o.id = $2;

-- name: GetOfferingRegistration :one
SELECT status
FROM course_registrations
WHERE institution_id = $1 AND offering_id = $2 AND student_record_id = $3;

-- name: CreateCourseRegistration :one
INSERT INTO course_registrations (institution_id, offering_id, student_record_id, status, approved_at)
VALUES ($1, $2, $3, $4, CASE WHEN $4 = 'approved' THEN $5::timestamptz ELSE NULL END)
ON CONFLICT (institution_id, offering_id, student_record_id) DO UPDATE
SET status = CASE
    WHEN course_registrations.status = 'rejected' THEN EXCLUDED.status
    ELSE course_registrations.status
END,
approved_at = CASE
    WHEN course_registrations.status = 'rejected' THEN EXCLUDED.approved_at
    ELSE course_registrations.approved_at
END
RETURNING status;

-- name: ListOfferingRegistrations :many
SELECT cr.student_record_id, sr.matric_no, sr.display_name, sr.department_id,
       sr.level, cr.status, cr.created_at, cr.approved_at
FROM course_registrations AS cr
JOIN student_records AS sr
  ON sr.institution_id = cr.institution_id AND sr.id = cr.student_record_id
WHERE cr.institution_id = $1 AND cr.offering_id = $2
  AND (sqlc.narg(status)::text IS NULL OR cr.status = sqlc.narg(status)::text)
  AND (sqlc.narg(department_id)::uuid IS NULL OR sr.department_id = sqlc.narg(department_id)::uuid)
ORDER BY sr.matric_no, sr.id
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: SetCourseRegistrationStatus :one
UPDATE course_registrations
SET status = $4,
    approved_at = CASE WHEN $4 = 'approved' THEN $5::timestamptz ELSE NULL END
WHERE institution_id = $1 AND offering_id = $2 AND student_record_id = $3
  AND status = 'pending'
RETURNING status;

-- name: CanManageStudentRegistration :one
SELECT EXISTS (
    SELECT 1
    FROM course_offerings AS o
    JOIN courses AS c
      ON c.institution_id = o.institution_id AND c.id = o.course_id
    JOIN student_records AS sr
      ON sr.institution_id = o.institution_id AND sr.id = $3
    WHERE o.institution_id = $1 AND o.id = $2
      AND (
        o.coordinator_user_id = $4
        OR EXISTS (
          SELECT 1 FROM offering_lecturers AS ol
          WHERE ol.institution_id = o.institution_id AND ol.offering_id = o.id
            AND ol.lecturer_user_id = $4
        )
        OR EXISTS (
          SELECT 1 FROM role_assignments AS ra
          JOIN departments AS home ON home.institution_id = ra.institution_id
            AND home.id = ra.department_id
          WHERE ra.institution_id = o.institution_id AND ra.user_id = $4
            AND ra.role = 'department_admin'
            AND (ra.department_id = c.department_id OR (c.kind = 'general' AND ra.department_id = sr.department_id))
        )
        OR EXISTS (
          SELECT 1 FROM role_assignments AS ra
          JOIN departments AS owner ON owner.institution_id = ra.institution_id
            AND owner.id = c.department_id
          WHERE ra.institution_id = o.institution_id AND ra.user_id = $4
            AND ra.role = 'faculty_admin' AND ra.faculty_id = owner.faculty_id
        )
        OR EXISTS (
          SELECT 1 FROM role_assignments AS ra
          WHERE ra.institution_id = o.institution_id AND ra.user_id = $4
            AND ra.role IN ('institution_admin', 'platform_admin')
        )
      )
);

-- name: OfferingRegistrationAccess :one
WITH target AS (
    SELECT c.kind, c.department_id AS owning_department_id, d.faculty_id
    FROM course_offerings AS o
    JOIN courses AS c
      ON c.institution_id = o.institution_id AND c.id = o.course_id
    JOIN departments AS d
      ON d.institution_id = c.institution_id AND d.id = c.department_id
    WHERE o.institution_id = $1 AND o.id = $2
)
SELECT
    (
      EXISTS (
        SELECT 1 FROM course_offerings AS o
        JOIN offering_lecturers AS ol
          ON ol.institution_id = o.institution_id AND ol.offering_id = o.id
        WHERE o.institution_id = $1 AND o.id = $2 AND ol.lecturer_user_id = $3
      )
      OR EXISTS (
        SELECT 1 FROM course_offerings AS o
        WHERE o.institution_id = $1 AND o.id = $2 AND o.coordinator_user_id = $3
      )
      OR EXISTS (
        SELECT 1 FROM role_assignments AS ra
        WHERE ra.institution_id = $1 AND ra.user_id = $3
          AND ra.role IN ('institution_admin', 'platform_admin')
      )
      OR EXISTS (
        SELECT 1 FROM role_assignments AS ra, target
        WHERE ra.institution_id = $1 AND ra.user_id = $3
          AND ra.role = 'faculty_admin' AND ra.faculty_id = target.faculty_id
      )
      OR EXISTS (
        SELECT 1 FROM role_assignments AS ra, target
        WHERE ra.institution_id = $1 AND ra.user_id = $3
          AND ra.role = 'department_admin'
          AND (ra.department_id = target.owning_department_id OR target.kind = 'general')
      )
    )::boolean AS can_view,
    CASE
      WHEN EXISTS (
        SELECT 1 FROM course_offerings AS o
        JOIN offering_lecturers AS ol
          ON ol.institution_id = o.institution_id AND ol.offering_id = o.id
        WHERE o.institution_id = $1 AND o.id = $2 AND ol.lecturer_user_id = $3
      )
      OR EXISTS (
        SELECT 1 FROM course_offerings AS o
        WHERE o.institution_id = $1 AND o.id = $2 AND o.coordinator_user_id = $3
      )
      OR EXISTS (
        SELECT 1 FROM role_assignments AS ra
        WHERE ra.institution_id = $1 AND ra.user_id = $3
          AND ra.role IN ('institution_admin', 'platform_admin')
      )
      OR EXISTS (
        SELECT 1 FROM role_assignments AS ra, target
        WHERE ra.institution_id = $1 AND ra.user_id = $3
          AND ra.role = 'faculty_admin' AND ra.faculty_id = target.faculty_id
      )
      OR EXISTS (
        SELECT 1 FROM role_assignments AS ra, target
        WHERE ra.institution_id = $1 AND ra.user_id = $3
          AND ra.role = 'department_admin' AND ra.department_id = target.owning_department_id
      )
      THEN NULL::uuid
      WHEN (SELECT kind FROM target) = 'general'
      THEN (
        SELECT ra.department_id FROM role_assignments AS ra
        WHERE ra.institution_id = $1 AND ra.user_id = $3 AND ra.role = 'department_admin'
        ORDER BY ra.department_id LIMIT 1
      )
      ELSE NULL::uuid
    END AS department_filter;
