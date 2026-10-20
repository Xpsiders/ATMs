-- name: GetInstitution :one
SELECT id, name, created_at
FROM institutions
WHERE id = $1;

-- name: ListFaculties :many
SELECT id, institution_id, name, created_at
FROM faculties
WHERE institution_id = $1
ORDER BY name, id;

-- name: ListDepartments :many
SELECT id, institution_id, faculty_id, name, code, created_at
FROM departments
WHERE institution_id = $1
  AND (sqlc.narg('faculty_id')::uuid IS NULL OR faculty_id = sqlc.narg('faculty_id'))
ORDER BY code, id;

-- name: ListProgrammes :many
SELECT id, institution_id, department_id, name, code, created_at
FROM programmes
WHERE institution_id = $1
  AND (sqlc.narg('department_id')::uuid IS NULL OR department_id = sqlc.narg('department_id'))
ORDER BY code, id;

-- name: ListAcademicSessions :many
SELECT id, institution_id, name, starts_on, ends_on, current, created_at
FROM academic_sessions
WHERE institution_id = $1
ORDER BY starts_on DESC, name;

-- name: ListSemesters :many
SELECT id, institution_id, academic_session_id, name, starts_on, ends_on, created_at
FROM semesters
WHERE institution_id = $1
  AND (sqlc.narg('academic_session_id')::uuid IS NULL OR academic_session_id = sqlc.narg('academic_session_id'))
ORDER BY starts_on, name;

-- name: SearchCourses :many
SELECT id, institution_id, department_id, code, title, kind, description, credits, created_at
FROM courses
WHERE institution_id = $1
  AND (sqlc.narg('department_id')::uuid IS NULL OR department_id = sqlc.narg('department_id'))
  AND (sqlc.narg('query')::text IS NULL OR search_vector @@ websearch_to_tsquery('simple', sqlc.narg('query')))
ORDER BY code;

-- name: SearchOfferings :many
SELECT o.id, o.institution_id, o.course_id, o.semester_id, o.coordinator_user_id,
       o.level, o.registration_mode, o.roster_policy, o.min_attendance_pct,
       o.late_counts_as_attended, o.created_at
FROM course_offerings o
JOIN courses c ON c.institution_id = o.institution_id AND c.id = o.course_id
JOIN semesters s ON s.institution_id = o.institution_id AND s.id = o.semester_id
WHERE o.institution_id = $1
  AND (sqlc.narg('course_id')::uuid IS NULL OR o.course_id = sqlc.narg('course_id'))
  AND (sqlc.narg('semester_id')::uuid IS NULL OR o.semester_id = sqlc.narg('semester_id'))
  AND (sqlc.narg('level')::integer IS NULL OR o.level = sqlc.narg('level'))
  AND (sqlc.narg('registration_mode')::text IS NULL OR o.registration_mode = sqlc.narg('registration_mode'))
  AND (sqlc.narg('query')::text IS NULL OR c.search_vector @@ websearch_to_tsquery('simple', sqlc.narg('query')))
ORDER BY c.code, o.level, s.starts_on;

-- name: ListOfferingLecturers :many
SELECT u.id, u.display_name, u.email
FROM offering_lecturers ol
JOIN users u ON u.institution_id = ol.institution_id AND u.id = ol.lecturer_user_id
WHERE ol.institution_id = $1 AND ol.offering_id = $2
ORDER BY u.display_name, u.id;

-- name: ListOfferingAutoTargets :many
SELECT department_id, level
FROM offering_auto_targets
WHERE institution_id = $1 AND offering_id = $2
ORDER BY department_id, level;
