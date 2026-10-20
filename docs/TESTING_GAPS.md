# Testing gaps

The test suite uses unit tests without Docker or a live database. Those tests
cannot establish PostgreSQL query correctness or concurrency properties. The
PostgreSQL-backed authentication, catalogue, registration, check-in, session,
scanner, report, and override paths need manual database verification. Use the
manual guide and a real PostgreSQL 15+ instance to validate at least:

- Goose migrations apply and roll back cleanly on an empty database.
- `make seed` creates the complete demo dataset atomically on an empty migrated
  database, can be rerun without duplicating rows, and prints the device key
  only when that device is first created. Confirm the printed credentials can
  log in and the unclaimed student records can complete signup verification.
- Tenant predicates and foreign-key/check constraints reject cross-institution
  or invalid writes.
- Student CSV preview and commit handle scoped departments and duplicate
  matric numbers as intended; verify the transaction rolls back when a
  concurrent insert violates the matric-number constraint, and confirm no
  import logs disclose row PII.
- Staff invitations enforce creator role/scope against target resources, send
  only token hashes to storage, and accept tokens once. Verify that user
  creation, scoped role assignment, invitation consumption, and the role audit
  entry commit or roll back together, including a competing acceptance.
- Student verification-code consumption, account creation, record claiming,
  and student-role assignment commit or roll back together.
- Signup start responses are identical for matching, mismatched,
  already-claimed, and unknown records, while only matching eligible records
  receive a code; verification failures do not reveal claim state. Exercise
  rate limits and any persistent verification-attempt caps after they are
  implemented.
- Device task queries resolve only the authenticated device's venue and
  pending enrolments, and enrolment completion cannot update another device's
  task.
- Concurrent check-ins rely on the `(session_id, student_id)` primary key and
  `INSERT ... ON CONFLICT DO NOTHING` to retain exactly one row per student.
- Search ranking and typo matching behave as expected with `pg_trgm` and
  full-text indexes.
- Catalogue CRUD, cross-tenant predicates, parent foreign keys, dynamic SQL
  filter construction, and resource-derived read/write authorization behave
  correctly for institution, faculty, department, and student identities.
- Catalogue unit tests check that resource read predicates bind to owning
  departments and include distinct coordinator/lecturer, home-department,
  and student-target paths. Verify with PostgreSQL that unauthorized detail
  reads return 404, collection pagination excludes out-of-scope rows, source
  and destination checks prevent moving resources outside scope, and general
  course reads never expose another department's students.
- Verify `/me/offerings` returns only offerings visible to the authenticated
  caller, including a lecturer's assigned and coordinated offerings; requests
  from different department scopes must not reveal another department's
  offerings.
- Verify `/venues` rejects student users, allows lecturers and administrator
  roles, and applies the authenticated institution to both page and total
  queries. Verify `/me/live-classes` returns only open sessions for the
  authenticated student's approved registrations, reflects both web and
  fingerprint check-ins, and never returns attendance codes.
- Registration modes and review status transitions persist correctly; repeated
  submissions and attempts to review an already-reviewed registration cannot
  change its outcome.
- SQL report joins include unrostered students and apply cross-department
  visibility correctly.
- Session report pagination, roster-sheet ordering, CSV escaping, and
  department filtering behave correctly for general courses.
- Bulk override transactions write one audit record for each successful item,
  roll back an individual override if its audit insert fails, and report
  per-item failures without affecting other students.
- Historical device heartbeats produce correct scanner coverage intervals at
  session boundaries, and the manual unavailable marker affects session
  classification as intended.
- Late-synced device scans associate to the session that was active at the
  device venue at `client_scanned_at`, including the session end boundary.
- Fixed-code conversion updates the session and inserts its audit row in one
  transaction; rollback of migration 00003 handles converted fixed-code rows.
- Bulk overrides preserve per-student partial success while writing each
  attendance change and audit row atomically.
- Database transaction boundaries keep attendance overrides and role changes
  atomic with their audit records.

This list is not evidence that those behaviors have been verified. A full
PostgreSQL-backed check requires Docker or a reachable PostgreSQL 15+ instance;
that is deliberately outside the unit-test requirement.
