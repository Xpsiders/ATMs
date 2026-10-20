-- name: ReportSessionExists :one
SELECT EXISTS (
    SELECT 1 FROM lecture_sessions
    WHERE institution_id = $1 AND id = $2
);
