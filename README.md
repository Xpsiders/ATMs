# Campus Attendance API

Standalone Go REST API repository for the Campus Attendance Platform.

## Status

The repository contains the Phase 0 foundation and tested slices for
authentication/student claims, check-in, sessions, device scans,
organization/catalogue, registration, student and staff CSV import, scoped
staff invitations and signup-request review, password reset, administrator role
assignment, audited venue and academic management, queued email delivery,
session reports, roster sheets, attendance overrides, student live classes,
lecturer-visible offerings, and paginated venue listing. A development seed
loader is available. The backend is still incomplete: database-backed
verification, device-key management, aggregate reports, disputes, and several
hardening requirements remain. Do not deploy this as a feature-ready
attendance service.

## Requirements

- Go 1.23+
- Docker Desktop or Docker Engine with Compose (for local PostgreSQL)
- `sqlc` and `golangci-lint` for the corresponding Make targets

## Local setup

1. Copy `.env.example` to `.env`, and set `DATABASE_URL` and
   `ALLOWED_ORIGIN`. Keep real secrets out of source control. The Go process
   does not load `.env` automatically; export the values in your shell or use
   Compose, which reads `.env` for variable substitution.
2. Start PostgreSQL with `docker compose up -d postgres`.
3. Run the API with `go run ./cmd/api`.
4. Check `http://localhost:8080/healthz` and `/readyz`.

The compose file provides an API and PostgreSQL service. Apply migrations with
`go run ./cmd/migrate up` after PostgreSQL is healthy, then run `make seed` to
load the date-relative demo dataset. It includes one institution, two
faculties, four departments and programmes, ten courses and offerings across
all registration modes, four lecturers, 48 student records (16 claimed
accounts), three venues and scanners, weekly timetable entries, live and
historical sessions, varied attendance evidence and overrides, and pending
invitation, signup, and scanner-enrolment records. The seed prints a login
table and the unclaimed matric/email pairs. Use the institution UUID printed
there for login. Newly created scanner keys and the pending invitation token
are displayed only once; reruns preserve them. To recreate only the demo
institution and generate new keys, run `make seed ARGS=--reset`.

### Set demo venues to your location

In Google Maps, right-click your physical position and copy the coordinates;
enter latitude first and longitude second. Indoor browser location can be off
by tens of metres, so a venue radius of 150–200 metres is a sensible starting
point for check-in testing. The seed places three venues at or within 25 metres
of the configured origin and adds a fourth venue about 5 km away for testing
`outside_radius`.

Set `SEED_ORIGIN_LAT`, `SEED_ORIGIN_LNG`, and optionally
`SEED_VENUE_RADIUS_M` in the repository-root `.env` file (the radius defaults
to 150 metres), then update seeded venues and open-session location snapshots
without resetting other data:

```powershell
$env:DATABASE_URL = "postgres://attendance:attendance@localhost:5432/attendance?sslmode=disable"
$env:SEED_ORIGIN_LAT = "LATITUDE_FROM_GOOGLE_MAPS"
$env:SEED_ORIGIN_LNG = "LONGITUDE_FROM_GOOGLE_MAPS"
$env:SEED_VENUE_RADIUS_M = "150"
go run ./scripts/seed --update-venues
```

With Compose, save the values in `.env` and run:

```powershell
docker compose run --rm seed seed --update-venues
```

For a host Go run, use `make set-venue` instead of the Go command when GNU Make
is installed. The command prints the origin and every seeded venue's coordinates.
If the origin variables are unset, the seed uses placeholder coordinates and
warns that check-ins from your actual location will fail with `outside_radius`.

## Commands

`make build`, `make run`, `make test`, `make test-race`, `make fmt`, `make lint`,
`make sqlc`, `make migrate-up`, `make migrate-down`, `make seed`, `make set-venue`,
`make docker-up`, `make docker-down`, and `make openapi-lint`.

In Windows PowerShell, use the same `make` commands if GNU Make is installed;
otherwise use the equivalent Go command, e.g. `go test ./...` and
`go run ./cmd/api`. Set environment variables using `$env:DATABASE_URL = "..."`
and `$env:ALLOWED_ORIGIN = "..."` before running Go commands.

## Frontend contract

`api/openapi.yaml` is intended to be the source contract for
`attendance-web`. It describes health, authentication, staff invitations,
password reset, signup-request review, imports, catalogue and academic
management, scoped venues and administrator role assignment, registration,
check-in, session, reports, roster, overrides, and device operations. The
database-backed workflows still need manual verification.

## Identity and staff administration

- Unknown-matric signup requests include `department_id`; a department-scoped
  administrator can list requests and approve one by supplying the student's
  department, programme, level, and display name, or reject it.
- Staff CSV imports use `staff_no,email,display_name,department_id` headers.
  Preview with `POST /imports/staff/preview`, then commit the entire validated
  file with `POST /imports/staff`. Commit creates staff records, lecturer
  invitations, audit rows, and queued invitation messages in one transaction.
- `POST /auth/password/forgot` always returns the same acknowledgement whether
  an account exists or not. `POST /auth/password/reset` consumes a one-time
  six-digit code; each code is capped at five failed attempts and expires after
  15 minutes.
- Venue management is available through `POST /venues`, `PATCH /venues/{id}`,
  and `DELETE /venues/{id}` (soft deactivation). Department/faculty scopes
  require an associated `department_id`; actions are audited. Academic session
  and semester CRUD is audited, and setting an academic session's `current`
  field atomically clears the previous current session.
- `POST /role-assignments` lets institution administrators assign faculty or
  department administrators; faculty administrators can assign department
  administrators only within their faculty.
- Email delivery is queued in PostgreSQL and retried by the API worker. The
  log-only mailer is available only with `APP_ENV=development`; configure
  `MAIL_API_KEY` and `MAIL_FROM` for non-development environments.

See `docs/ARCHITECTURE.md`, `docs/DECISIONS.md`, and
`docs/TESTING_GAPS.md`. Track delivery status in `docs/PROGRESS.md`.
