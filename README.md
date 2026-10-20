# Campus Attendance API

Standalone Go REST API repository for the Campus Attendance Platform.

## Status

The repository contains the Phase 0 foundation and tested slices for
authentication/student claims, check-in, sessions, device scans,
organization/catalogue, registration, student CSV preview/commit, scoped staff
invitations, session reports, roster sheets, attendance overrides, student live
classes, lecturer-visible offerings, and paginated venue listing. A development
seed loader is available. The backend is still incomplete:
database-backed verification, staff CSV import, signup-request review,
device-key management, aggregate reports, disputes, email outbox processing,
and several hardening requirements remain. Do not deploy this as a
feature-ready attendance service.

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
load the demo data and print credentials and the initial scanner key. The
plaintext device key is shown only when the seed command creates the device;
rerunning seed does not recover or rotate an existing key.
The demo dataset includes development-only accounts for platform admin,
institution admin, faculty admin, department admins for both departments,
lecturers, and a claimed student. The seed output lists each account's email
and password; use the institution UUID printed there for login.

## Commands

`make build`, `make run`, `make test`, `make test-race`, `make fmt`, `make lint`,
`make sqlc`, `make migrate-up`, `make migrate-down`, `make seed`,
`make docker-up`, `make docker-down`, and `make openapi-lint`.

In Windows PowerShell, use the same `make` commands if GNU Make is installed;
otherwise use the equivalent Go command, e.g. `go test ./...` and
`go run ./cmd/api`. Set environment variables using `$env:DATABASE_URL = "..."`
and `$env:ALLOWED_ORIGIN = "..."` before running Go commands.

## Frontend contract

`api/openapi.yaml` is intended to be the source contract for
`attendance-web`. It describes implemented health, authentication and staff
invitation, catalogue, registration, student import, check-in, session, live
class, venue, report, roster, override, and device operations; remaining
endpoint groups are still under development.

See `docs/ARCHITECTURE.md`, `docs/DECISIONS.md`, and
`docs/TESTING_GAPS.md`. Track delivery status in `docs/PROGRESS.md`.
