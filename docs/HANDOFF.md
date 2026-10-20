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
- Docker Desktop is paused. PostgreSQL migrations, seed execution, and
  database-backed behavior have not been verified against a live database.
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
- A transactional, repeatable `scripts/seed` demo data loader, with unit tests.
  It prints credentials and reveals the device key only when it first creates
  the device. Live PostgreSQL execution remains outstanding.
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

## Next priority

Continue using the active order in `docs/PROGRESS.md`. Database verification
of the catalogue scope queries and seed loader remains blocked while Docker
Desktop is paused; code work still outstanding includes staff CSV imports,
signup-request review, device-key provisioning/rotation/revocation, aggregate
reports, and the email outbox worker.

## Do not claim complete

The complete seven-phase backend is not finished. Live database/migration
verification, the full race suite, full lint, several domain API groups,
device-key management, aggregate reports, disputes, outbox processing, and
some hardening requirements remain outstanding as detailed in `PROGRESS.md`.
