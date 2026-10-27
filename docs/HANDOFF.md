# Development handoff

Updated 2026-10-20. This is a point-in-time engineering handoff, not a claim
that every phase or database-backed behavior is complete. See
[PROGRESS.md](./PROGRESS.md) for the phase-by-phase checklist and
[DECISIONS.md](./DECISIONS.md) for established design decisions.

## Repository and environment

- Workspace: `C:\Dev\ATMs`; Go module: `attendance-api`.
- This directory is a Git repository on `master`; commits are available.
- `docs/HANDOFF.md` was absent when the handoff instructions were first checked;
  this file now records the current state.
- The owner reports that Goose migrations have been applied successfully to
  PostgreSQL and produced 35 tables. This task did not start Docker,
  PostgreSQL, or the API; the expanded seed has not yet been executed.
- `make` is not installed. The OpenAPI linter was run directly with
  `npx.cmd`; it validated `api/openapi.yaml` with four recommended-rule
  warnings (license field, localhost server URL, and missing 4xx responses on
  health routes).
- `go test -race ./...` has previously been blocked by the local CGO/compiler
  environment. Do not report race verification as passing unless rerun
  successfully.

## Implemented and verified work

- Catalogue/offering resource-scope filtering and destination checks, with
  related RBAC tests and documentation.
- A transactional, repeatable, date-relative `scripts/seed` dataset with two
  faculties, four departments/programmes, a prior and a current semester under
  the current academic session, ten courses/offerings (in the current semester)
  spanning all registration modes, fourteen lecturer accounts/staff records
  (one assigned per department plus ten spare across departments for the
  lecturer picker), 48 students (16 claimed), three venues/scanners, weekly
  timetables, open and historical sessions, registrations, and varied
  attendance/check-in/scan/override states.
  It also creates pending invitation, signup, and scanner-enrolment records.
  Credentials and unclaimed matric/email pairs are printed; new scanner keys
  and the invitation token appear only on first creation. `--reset` is scoped
  to the demo institution. Deterministic fixture tests pass; execution against
  PostgreSQL remains outstanding.
- Student CSV preview and commit:
  - `POST /imports/students/preview` parses and validates rows without writes.
  - `POST /imports/students` revalidates and commits all rows in one
    transaction.
  - Department authorization is resolved from each target department; upload
    size is capped at 5 MiB and the handler requires `text/csv`.
  - A concurrent duplicate matric insert is mapped to a safe 409 response
    (`matric_number_conflict`) without returning the CSV row or database error.
  - OpenAPI, manual test guide, testing gaps, and progress checklist include the
    import flow.
- Scoped staff invitations and acceptance:
  - `POST /invitations` checks inviter permissions against the target
    institution/faculty/department in SQL. Department admins can invite
    lecturers only within their department; faculty admins can invite within
    their faculty; institution admins can invite institution-owned roles.
  - Invitation tokens use 32 random bytes; only SHA-256 hashes are stored.
    Tokens expire after seven days and are single-use. Acceptance creates the
    verified account, scoped role, consumes the invite, and writes the audit
    event in one transaction.
  - The OpenAPI contract, sqlc queries and generated code, main wiring,
    deterministic service/handler tests, decisions, manual guide, testing
    gaps, README, and progress checklist were updated.
- The latest verification run passed `gofmt -l .` (no output), `go vet ./...`,
  `go build ./...`, `go test ./...`, uncached `go test -count=1 ./internal/auth`,
  and sqlc generation. OpenAPI validation passed with four recommended-rule
  warnings (missing license field, localhost server URL, and missing 4xx
  responses on health routes).
- Shared HTTP middleware now recovers handler panics, logs request metadata
  and panic type without logging panic values, and returns the standard generic
  500 envelope when the response has not yet been committed. It preserves an
  already-started response rather than appending a corrupt JSON body. Focused
  tests cover both cases. For this task, `gofmt -l .` returned no output;
  `go vet ./...`, `go build ./...`, and `go test ./...` all exited successfully.
  The passing test packages include `internal/httpx`; packages with no test
  files are listed as such by `go test`.
- Login and authenticated `GET /me` now return the ID, name, email, status,
  `must_change_password`, and role assignments with their resource-derived
  scopes; student roles include the locked department and level. Login
  preserves `force_password_change` for compatibility, and `/me` is available
  to password-change-required tokens. OpenAPI, SQL/sqlc bindings, unit tests,
  decisions, progress, and the manual walkthrough were updated. The current
  checks passed: `gofmt -l .` (no output), `go vet ./...`, `go build ./...`,
  and `go test ./...`. OpenAPI validation passed with four recommended-rule
  warnings. PostgreSQL-backed queries remain unverified.
- Student claim start now returns one acknowledgement for matching,
  mismatched, already-claimed, and unknown records; verification failures and
  already-claimed records share the same error envelope. The OpenAPI contract,
  handler tests, and manual walkthrough were updated. Focused
  `go test ./internal/auth` passed. Persistent attempt caps and live PostgreSQL
  verification remain open.
- Student claims now insert approved registrations for matching auto-mode
  offerings in the same PostgreSQL transaction as account and role creation.
  The query is tenant-, department-, level-, mode-, and general-course-target
  scoped and has unit coverage for its generated SQL contract.
- Student `GET /me/live-classes` now requires a claimed student identity and
  lists only open sessions for that student's approved registrations. Its
  tenant/user-bound query includes venue and lecturer metadata, web and
  fingerprint check-in state, and no attendance code. `GET /me/offerings`
  applies the existing resource visibility rules and pagination. `GET /venues`
  enforces the caller's tenant and permits lecturers/admin roles only, with
  page and total queries. The demo seed now connects each lecturer to an
  offering and venue via a timetable entry and approves demo student
  registrations. These paths have unit and generated-query scope tests; the
  seed and SQL have not been run against PostgreSQL.
- HTTP request-completion logs now include the standard machine-readable
  `error_code` for 4xx/5xx responses without logging request or response
  bodies. Student claim start/verify responses also conceal record eligibility
  as described above.
- Latest checks for the endpoint work: `gofmt -l .` produced no output;
  `go vet ./...`, `go build ./...`, and `go test ./...` passed. OpenAPI
  validation passed with the four existing recommended-rule warnings listed
  above. The database-backed seed and queries remain unverified.
- Staff CSV preview/commit now enforces row validation and department scope;
  commit atomically creates staff records, department-scoped lecturer
  invitations, audit entries, and queued messages. Signup requests are tied to
  departments and can be listed/reviewed by scoped administrators, with student
  record creation and audit in one transaction. Password reset uses
  enumeration-safe responses, HMAC-hashed one-time codes, persistent attempt
  limits, refresh-token revocation, and forced-change clearing. Venue CRUD and
  academic session/semester mutations are scoped and audited; administrator
  role assignments follow the institution/faculty hierarchy. The email outbox
  worker retries queued mail, and log-only delivery is development-only. Unit
  tests cover parsing, pagination, math/backoff, and scope rules; PostgreSQL
  transaction and migration behavior remain unverified. Full
  `gofmt -l .`, `go vet ./...`, `go build ./...`, and `go test ./...` passed
  for this feature slice.

- Device management (provisioning, pairing, telemetry) was added as the new
  `internal/devices` package plus a device-side `POST /device/status` on
  `internal/scans`. An administrator provisions a scanner with `POST /devices`
  (scoped to the venue department, audited) and receives a one-time pairing code
  to flash into the firmware; the firmware redeems it at `POST /device/pair` for
  a permanent device key. `POST /devices/{id}/pairing-code` reissues a code,
  `POST /devices/{id}/rotate-key` clears the key and forces re-pair,
  `DELETE /devices/{id}` soft-revokes (the key is rejected immediately), and
  `GET /devices` lists scoped scanners with status and health. `POST
  /devices/{id}/enrolments` queues a student onto the next device slot and a
  pending enrolment task (previously only the seed created tasks). The new
  migration `00006_device_management.sql` makes `devices.key_hash` nullable, adds
  telemetry columns, and adds the `device_pairing_codes` table. Authorization
  reuses the `rbac.CanAccess` venue-department pattern and additionally allows a
  lecturer to manage scanners at venues they teach at (assigned to an offering
  timetabled at the venue); all mutations write audit rows (pairing is
  device-initiated, so its audit row carries a null actor). Viewing attendance
  needs no device role — an assigned lecturer already sees their sessions'
  reports. Fingerprint templates are stored centrally (`student_fingerprints`,
  migration `00007`) so a student enrolled on one scanner is recognised on any
  scanner serving a session they are registered for: `POST
  /device/enrolments/{id}/complete` accepts the sensor template, and `POST
  /device/templates/sync` returns a session roster's templates with the integer
  slot positions to load. Device scans now identify the student by `slot_number`
  (the sensor's native position, resolved via `device_slots`) instead of a UUID,
  and the pending-enrolment task carries `slot_number`.
  Deterministic unit tests cover the scope helper, pairing-code generation,
  provisioning, rotation, revocation, enrolment enqueue, and status validation.
  The migration and all PostgreSQL behavior remain unverified in-session (Docker
  paused). `gofmt`, `go vet`, `go build`, `go test`, and `openapi-typescript`
  validation passed for this slice.

- **Location no longer gates attendance, and a venue's coordinates come from
  the lecturer's device.** This replaced an earlier slice in which an
  administrator captured venue coordinates; administrators are never in the
  room, so that flow could not work in practice.

  Nothing refuses a check-in on position any more and `ErrOutsideRadius` /
  `outside_radius` are deleted. `POST /checkins` takes `lat`, `lng`, and
  `accuracy` as optional (the browser may refuse a position, and the venue may
  have no centre) and requires `device_hash`: per-session device binding is now
  the anti-proxy control, so a second student from the same device is refused
  with `device_already_used` (409). `location_unverified: true` now means the
  fix landed further out than the phone's own reported error can explain
  (allowance capped at 250 m past the radius); inside that allowance it is
  simply accepted. Flagged rows report as `web_only_unverified` with reason
  `location_unverified`.

  Migration `00008_optional_venue_location.sql` drops NOT NULL from
  `venues`/`lecture_sessions` latitude, longitude, radius_meters and from
  `web_checkins` latitude, longitude, accuracy_meters, distance_meters, and
  adds the partial index `web_checkins_session_device_idx` for the binding
  lookup. `CHECK (radius_meters > 0)` survives, so a venue with no position
  stores NULL for all three columns, never 0. Go side: `sessions.Session`,
  `StartInput`, `VenueSettings`, `checkins.Session`/`CheckinRecord` and the
  check-in response's `distance_meters` all became optional/pointer-valued.

  `POST /sessions` accepts `accuracy_meters` and resolves the geofence through
  `sessions.ResolveStartLocation`: a fix of 100 m or better centres the session
  and is written back to the venue inside the session-create transaction
  (`internal/sessions/postgres.calibrateVenue`, audited
  `venue.location_captured`); a vaguer fix falls back to the venue; coordinates
  without an accuracy stay a session-only override. The response reports
  `location_source` as `device`, `venue`, or `none`. Starting a class never
  depends on geolocation.

  `POST /sessions/{id}/location` (new) applies a mid-session fix:
  `refined` (recentres session + venue), `established` (first centre for a
  venue that has none), `unchanged` (<10 m), `rejected_accuracy` (>100 m), and
  `rejected_distance` (outside the session's own radius — the centre
  deliberately does not move, so a lecturer stepping out cannot push the
  students still in the room outside the area). `RecentreSession` re-checks
  `ended_at IS NULL` in the UPDATE rather than trusting the earlier read.
  `established` is a **fifth** outcome beyond the four pinned in the frontend
  prompt; it needs no UI and is safe to ignore, but the prompt should be
  updated.

  `POST /venues/{id}/location` survives as the explicit "recalibrate this room"
  action and is now open to a lecturer who teaches at the venue as well as to
  administrators over its department (`lecturerTeachesAtVenue`, duplicating the
  device store's query over `offering_lecturers`/`timetable`). `POST /venues`
  needs only a name.

  A radius is kept once set — a device fix says where a room is, not how big
  it is. Only a venue with no radius takes one from
  `sessions.RadiusForNewCentre`, which defers to
  `catalogue.VenueRadiusFromAccuracy` so both calibration paths agree. The
  shared haversine moved to `internal/geo`.

  Deterministic tests: `internal/sessions/location_test.go` covers
  `ResolveStartLocation` (trusted fix, vague fix, no fix and no venue, fix
  without accuracy) and every `ClassifyLocationUpdate` outcome including the
  nil-centre cases and unusable input; `service_test.go` covers `UpdateLocation`
  writing and not writing, radius retention versus derivation, and closed and
  unknown sessions; `handler_test.go` covers `location_source` for each source,
  a start with location permission denied, a 400 for a mid-session fix with no
  accuracy, and a 409 on a closed session. The check-in allowance,
  classification reason and report query contract tests from the earlier slice
  still pass.

  `gofmt -l` (excluding `.claude/worktrees`), `go vet ./...`, `go build ./...`
  and `go test ./...` pass; `npx @redocly/cli lint api/openapi.yaml` is valid
  with the four existing recommended-rule warnings. **Nothing is verified
  against PostgreSQL**: migration `00008`, the session-start calibration
  transaction, `RecentreSession`, the device-binding query, the
  `lecturerTeachesAtVenue` authorization path and the report query's derived
  column are all unexercised. `sqlc generate` was broken on the pristine tree
  (v1.29.0, "column reference institution_id is ambiguous" at
  `db/queries/identity.sql:103`); `AutoRegisterClaimedStudent`'s CTE columns are
  now qualified and `SetCourseRegistrationStatus` uses `sqlc.arg(approved_at)`,
  and `internal/db/dbgen` is genuinely regenerated rather than hand-edited.

## Next priority

Continue using the active order in `docs/PROGRESS.md`. Re-run `make sqlc` to
confirm the hand-edited `SessionReportPage` bindings match generated output.
Database verification of the catalogue scope queries, the seed loader, the
device-management migration/queries, and migration `00008` with the whole
venue-location/device-binding slice remains blocked while Docker Desktop is
paused; code work
still outstanding includes aggregate reports, disputes, and remaining
hardening. A natural device follow-up is auto-queuing fingerprint enrolment
when a student registers for a course, rather than the current explicit
`POST /devices/{id}/enrolments` action.

## Do not claim complete

The complete seven-phase backend is not finished. Live Database/migration verification, the full race suite, full lint, several
domain API groups, device-key management, aggregate reports, disputes, and
some hardening requirements remain outstanding as detailed in `PROGRESS.md`.
