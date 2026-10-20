# Implementation progress

Status is based on repository inspection on 2026-10-20. **Done** means the
implementation and its required verification are present; **partial** means a
slice exists but important requirements or verification remain; **not
started** means no usable implementation is present. This is a delivery
checklist, not a claim that database-backed behavior has been exercised.

## Phase 0 — Scaffold

| Item | Status | Notes |
|---|---|---|
| Go module, API entry point, chi router | Done | |
| Environment configuration and PostgreSQL pool | Done | |
| JSON/error helpers, request IDs, middleware, health/readiness | Done | Strict JSON/error helpers, request IDs, CORS, request logging, rate limits, health/readiness, and panic recovery are implemented with unit tests; database UUID values returned through catalogue maps are normalized to lowercase hyphenated strings; broader endpoint hardening is tracked in Phase 6. |
| FakeClock and in-memory test helpers | Partial | Fake clock and selected stores/mailer helpers exist, not fakes for every service. |
| Makefile targets | Partial | Build, test, migration, and seed targets exist; database-backed targets remain unverified. |
| Dockerfile and API/PostgreSQL Compose stack | Partial | Files exist; end-to-end stack verification is blocked while Docker Desktop is paused. |
| Goose migration and sqlc wiring | Partial | Migrations and generated bindings exist; migration up/down has not been run against PostgreSQL. |
| CI build, vet, lint, SQL generation check, OpenAPI validation, race tests | Partial | Workflow exists; local race testing is blocked by disabled CGO and no native compiler. |

## Phase 1 — Organization and identity

| Item | Status | Notes |
|---|---|---|
| Institution/faculty/department/programme schema and APIs | Partial | Catalogue APIs and resource-derived scope checks are implemented and unit-tested; database behavior and remaining endpoints require verification. |
| Users, role assignments, student records and tenant constraints | Partial | Identity schema and tenant-aware queries exist; full schema/query audit remains. |
| Login, bcrypt passwords, access JWT, refresh-token rotation/revocation | Partial | Login includes the current profile and scoped roles, and authenticated `GET /me` returns the same profile; token hardening and live DB verification remain. |
| Verified student signup claim and one-time verification codes | Partial | Account creation follows one-time verification and locks department/level; start and verify responses now conceal record eligibility, with PostgreSQL verification and attempt-cap hardening outstanding. |
| Unknown-matric signup approval requests and admin review | Partial | Request persistence exists; review API is absent. |
| Invitations | Partial | Scoped staff invitations and acceptance are implemented with hashed one-time tokens, issuer scope checks, transactional role creation/audit, and unit tests; live PostgreSQL verification remains. |
| Password reset | Not started | |
| Student/staff CSV import and row validation | Partial | Student CSV parsing, scoped preview, atomic commit, row-level errors, size limits, and concurrent matric conflict handling are implemented and unit-tested; staff import remains absent and PostgreSQL behavior is unverified. |
| Authentication and identity unit tests | Partial | Unit tests cover login and `/me` profile/role JSON, including student department/level; the full identity requirement matrix remains incomplete. |

## Phase 2 — Academics, catalogue and registration

| Item | Status | Notes |
|---|---|---|
| Academic sessions and semesters | Partial | Generic catalogue CRUD exists; full scope enforcement and API coverage remain. |
| Courses, offerings, lecturer assignments and auto-registration targets | Partial | CRUD and assignment routes exist; `/me/offerings` now applies the authenticated resource visibility and pagination rules, while broader scope coverage and DB verification remain. |
| Full-text/trigram offering search and required filters/defaults | Partial | Search query exists; PostgreSQL ranking and typo behavior are not live-tested. |
| Registration modes and roster policy rules | Done | Pure rule logic and unit tests exist. |
| Registration submit/list/review APIs | Partial | Routes and services exist; database behavior and comprehensive authorization tests remain. |
| General-course visibility and department filtering | Partial | Some report/registration filters exist; catalogue read enforcement is incomplete. |
| Academic/catalogue/registration unit tests | Partial | Service/rule coverage exists; HTTP and DB-backed behavior is incomplete. |

## Phase 3 — Venues, sessions and web check-in

| Item | Status | Notes |
|---|---|---|
| Venues, devices, timetable schema and CRUD APIs | Partial | Tenant-scoped, role-gated paginated `GET /venues` is implemented with total count and unit tests; venue/timetable management APIs remain absent and DB behavior is unverified. |
| Session start/end and one-open-session-per-venue constraint | Partial | Session service/schema logic exists; PostgreSQL migration/constraint has not been exercised. |
| Static and rotating codes, code retrieval | Partial | Pure code logic and routes exist; behavior is covered by unit tests. |
| Web check-in, roster policy, geofence and duplicate handling | Partial | Service and unit tests exist; race-safe SQL and live DB behavior are unverified. |
| Session/check-in HTTP and database verification | Partial | Some handler tests exist; complete endpoint test matrix and PostgreSQL run remain. |

## Phase 4 — Device API

| Item | Status | Notes |
|---|---|---|
| Device task polling, ETag and heartbeat | Partial | Task API and ETag logic exist; heartbeat/coverage DB behavior needs live verification. |
| Device scan, idempotency, late-synced timestamp and batch endpoint | Partial | Implemented and unit-tested; PostgreSQL behavior is unverified. |
| Enrolment completion | Partial | Endpoint exists; complete device enrolment administration flow is absent. |
| Device key generation/hash utility | Partial | Utility and tests exist; provisioning, rotation and revocation APIs are priority 4. |
| Firmware API documentation, flash queue, TLS and time guidance | Partial | Device guide exists; deployment CA must be supplied by operator. |

## Phase 5 — Reports and audit

| Item | Status | Notes |
|---|---|---|
| Session report classification and scanner coverage | Partial | Service, classification, report API and unit tests exist; SQL behavior needs live verification. |
| Roster sheet JSON/CSV and scope | Partial | API and tests exist; end-to-end verification remains. |
| Audited single and bulk overrides | Partial | Bulk override service/API and transactional store exist; live transaction testing remains. |
| Offering/department aggregate attendance reports | Not started | |
| Attendance percentages and below-threshold list | Partial | Pure percentage math exists; aggregate report endpoints and queries are absent. |
| CSV exports | Partial | Session report/roster export exists; aggregate exports remain. |
| Audit logging for all role/attendance changes | Partial | Session and override audit paths exist; role-change coverage and verification are incomplete. |
| Disputes | Not started | |
| Email outbox worker | Not started | Mailer implementations exist; persistent outbox/worker is absent. |

## Phase 6 — Hardening and delivery

| Item | Status | Notes |
|---|---|---|
| Rate limits for login, signup, verification, check-in and device requests | Partial | IP limits are wired for auth and user/device limits exist; persistent verification attempt caps, trusted-proxy IP handling, and failure logging audit remain. |
| JSON request limits, strict decoding and error envelope | Partial | Shared helpers exist; every handler must be checked for consistent use. |
| CORS allowlist, request IDs and structured logs | Partial | Infrastructure exists; complete hardening verification remains. |
| Optional phone/device binding | Not started | |
| OpenAPI coverage and accurate schemas for all implemented endpoints | Partial | Added student-only `GET /me/live-classes`, scoped `GET /me/offerings`, and paginated `GET /venues`; many product routes remain uncovered. |
| Demo seed loader and reproducible checklist | Partial | `make seed` includes development accounts for every role (platform, institution, faculty, department admins, lecturers, student) and prints credentials; unit-tested, database execution remains blocked pending PostgreSQL availability. |
| Manual guide, testing gaps, architecture and decisions docs | Partial | Docs exist and are being updated with implemented workflows; full product checklist remains incomplete. |
| README setup, deployment, frontend contract and Windows instructions | Partial | Setup, seed and frontend contract instructions exist; deployment guidance and full contract coverage remain. |
| Build/vet/format/lint/race/coverage gates | Partial | Build, vet, formatting, tests and targeted coverage pass; lint unavailable and race blocked by CGO. |
| Migration up/down verification on empty PostgreSQL | Not started | Docker Desktop is paused; no live PostgreSQL verification was run. |

## Hardening addendum

| Item | Status | Notes |
|---|---|---|
| Security sweep of every route and permission boundary | Partial | Catalogue/offering resource scopes and source/destination write checks are implemented with unit tests; PostgreSQL execution and broader endpoint hardening remain. |
| Access-token lifetime, refresh cookie attributes, reuse rejection, forced password change | Partial | Core token functionality exists; full configuration and security matrix need verification. |
| Rate-limit failures logged without leaking PII | Partial | Requires endpoint-by-endpoint review. |
| Device authentication separated from user authentication | Done | Separate `X-Device-Key` middleware is wired. |
| CSV import size limit, dry-run preview, row-level errors, commit | Partial | Student CSV preview/commit is implemented with scoped validation, 5 MiB limit, all-or-nothing writes, and conflict response; staff import and live DB verification remain. |
| Manual security checks and documented deployment limitations | Partial | Existing guides need continued alignment with completed features. |

## Power-cut addendum

| Item | Status | Notes |
|---|---|---|
| One-way rotating-to-fixed six-digit code conversion and audit | Partial | Service/API and unit tests exist; PostgreSQL transaction not integration-tested. |
| Scanner coverage states and heartbeat-derived offline intervals | Partial | Computation, query and report wiring exist; SQL behavior needs live DB verification. |
| Manual scanner-unavailable flag and audit | Partial | Service/API exists; transaction not integration-tested. |
| `web_only_unverified` classification during scanner outages | Done | Pure classification logic and tests exist. |
| Bulk overrides, per-item results and atomic audit entries | Partial | Service/API/store exist; transaction behavior is not live-tested. |
| Scoped, ordered roster sheet in JSON and CSV | Partial | API and unit tests exist; live scope query is unverified. |
| Device flash queue, late-sync timestamp and reboot guidance | Partial | Client timestamp and batch route/documentation exist; no firmware is in this repository. |
| OpenAPI/manual/testing-gap updates | Partial | Updated for the implemented addendum slice; recheck as remaining work is completed. |

## Current work order

The active priority list is: (1) catalogue and offering scopes (implementation
and unit checks complete; SQL runtime verification still needed); (2) seed
loader (implemented, live DB run pending); (3) finish imports, verified claims,
and invitations (student preview/commit and scoped staff invitations are
implemented and unit-tested; staff import, signup-request review, and live DB
verification remain); (4) device-key management; (5) aggregate reports and
email outbox; (6) remaining hardening; (7) close power-cut gaps.
Update this file whenever an item changes status.
