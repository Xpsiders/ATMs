# Implementation progress

Status is based on repository inspection on 2026-10-21. **Done** means the
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
| Makefile targets | Partial | Build, test, migration, and seed targets exist; seed execution remains unverified in this session. |
| Dockerfile and API/PostgreSQL Compose stack | Partial | Files exist; end-to-end stack verification is blocked while Docker Desktop is paused. |
| Goose migration and sqlc wiring | Done | Migrations have now been run successfully against PostgreSQL; the owner confirmed the resulting schema contains 35 tables. Generated bindings are checked in; down-migration behavior remains unverified. |
| CI build, vet, lint, SQL generation check, OpenAPI validation, race tests | Partial | Workflow exists; local race testing is blocked by disabled CGO and no native compiler. |

## Phase 1 — Organization and identity

| Item | Status | Notes |
|---|---|---|
| Institution/faculty/department/programme schema and APIs | Partial | Catalogue APIs and resource-derived scope checks are implemented and unit-tested; database behavior and remaining endpoints require verification. |
| Users, role assignments, student records and tenant constraints | Partial | Identity schema and tenant-aware queries exist; faculty/department administrator assignment is scoped and audited; full schema/query audit remains. |
| Login, bcrypt passwords, access JWT, refresh-token rotation/revocation | Partial | Login includes the current profile and scoped roles, and authenticated `GET /me` returns the same profile; token hardening and live DB verification remain. |
| Verified student signup claim and one-time verification codes | Partial | Account creation follows one-time verification and locks department/level; start and verify responses now conceal record eligibility, with PostgreSQL verification and attempt-cap hardening outstanding. |
| Unknown-matric signup approval requests and admin review | Partial | Unknown requests are department-tagged; list/reject/approve routes enforce department/faculty/institution scope and approval transactionally creates the student record and audit event. Unit coverage is present; PostgreSQL behavior remains unverified. |
| Invitations | Partial | Scoped staff invitations and acceptance use hashed one-time tokens; acceptance links imported staff records to users transactionally. Development-only logging includes invitation links; outbox delivery is wired. Live PostgreSQL verification remains. |
| Password reset | Partial | Enumeration-safe forgot/reset routes use HMAC-hashed one-time codes, a persistent five-attempt cap, 15-minute expiry, forced-change clearing, refresh-token revocation, and transactional audit/outbox writes; database behavior remains unverified. |
| Student/staff CSV import and row validation | Partial | Student and staff CSV parsing, scoped preview, atomic commit, row-level errors, and 5 MiB limits are implemented. Staff commit creates lecturer invitations and outbox rows atomically; student duplicate conflicts are handled. PostgreSQL behavior remains unverified. |
| Authentication and identity unit tests | Partial | Unit tests cover login and `/me` profile/role JSON, including student department/level; the full identity requirement matrix remains incomplete. |

## Phase 2 — Academics, catalogue and registration

| Item | Status | Notes |
|---|---|---|
| Academic sessions and semesters | Partial | Generic catalogue CRUD is institution-scoped; mutations are audited and setting `current` clears the prior current session transactionally. PostgreSQL behavior remains unverified. |
| Courses, offerings, lecturer assignments and auto-registration targets | Partial | `GET /courses` supports department/kind filters. Lecturer `POST /offerings` creates same-department offerings with automatic assignment or queues cross-department teaching requests; scoped administrators can review them. Offering edits/deletes and timetable changes are audited; database verification remains. |
| Full-text/trigram offering search and required filters/defaults | Partial | Search results include registration mode/state, course and department metadata, level, kind, and lecturers; PostgreSQL ranking and result query behavior are not live-tested. |
| Registration modes and roster policy rules | Done | Pure rule logic and unit tests exist. |
| Registration submit/list/review APIs | Partial | Submit returns approved/pending decisions for auto/open/approval modes and a clear conflict for closed; `GET /me/registrations` lists only the caller's pending requests. Lecturer/admin pending-approval queue and single/bulk review are scoped, with review audits; database behavior remains unverified. |
| General-course visibility and department filtering | Partial | Some report/registration filters exist; catalogue read enforcement is incomplete. |
| Academic/catalogue/registration unit tests | Partial | Service tests cover offering input validation, timetable overlap math, approval pagination/bulk result handling, and seed lecturer/course department ownership; SQL authorization and transaction behavior remain unverified. |

## Phase 3 — Venues, sessions and web check-in

| Item | Status | Notes |
|---|---|---|
| Venues, devices, timetable schema and CRUD APIs | Partial | Scoped venue create/update/deactivate and active, scope-filtered listing are audited; inactive venues are excluded from new session starts/timetable assignments. Student timetable and audited offering timetable CRUD remain implemented. Seed venues can be updated without resetting data; database behavior remains unverified. |
| Session start/end and one-open-session-per-venue constraint | Partial | Session service/schema logic exists; PostgreSQL migration/constraint has not been exercised. |
| Static and rotating codes, code retrieval | Partial | Pure code logic and routes exist; behavior is covered by unit tests. |
| Web check-in, roster policy, geofence and duplicate handling | Partial | Service and unit tests exist; race-safe SQL and live DB behavior are unverified. |
| Session/check-in HTTP and database verification | Partial | `GET /sessions/{id}/live` returns scoped counts and paginated status/flags for polling; service tests cover scope propagation and classification. PostgreSQL behavior remains unverified. |

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
| Session report classification and scanner coverage | Partial | Service, classification, report API, live-session roster/count endpoint, and unit tests exist; SQL behavior needs live verification. |
| Roster sheet JSON/CSV and scope | Partial | API and tests exist; end-to-end verification remains. |
| Audited single and bulk overrides | Partial | Bulk override service/API and transactional store exist; live transaction testing remains. |
| Offering/department aggregate attendance reports | Not started | |
| Attendance percentages and below-threshold list | Partial | Student `GET /me/courses` uses the pure percentage math, counts late according to offering policy, and applies override status first; department aggregate endpoints remain absent and database behavior is unverified. |
| CSV exports | Partial | Session report/roster export exists; aggregate exports remain. |
| Audit logging for all role/attendance changes | Partial | Offering/timetable changes, teaching request decisions, registration reviews, venue/academic mutations, role assignment, signup review, and password reset now write audit rows transactionally; PostgreSQL verification remains. |
| Disputes | Not started | |
| Email outbox worker | Partial | Invitations, verification, staff-import, and reset messages enqueue in PostgreSQL; a leased batch worker retries delivery with backoff and the development-only mailer logs links/codes. Retry/concurrency behavior remains unverified against PostgreSQL. |

## Phase 6 — Hardening and delivery

| Item | Status | Notes |
|---|---|---|
| Rate limits for login, signup, verification, check-in and device requests | Partial | Removed shared IP/user quotas from authenticated read and session route groups that caused routine endpoints (for example `/me/timetable`) to return 429. Verification has its own five-requests-per-minute-per-IP limit; login, check-in, invitations, and device routes retain their endpoint-specific limits. Persistent per-code failed-attempt caps and trusted-proxy IP handling remain. |
| JSON request limits, strict decoding and error envelope | Partial | Shared helpers exist; every handler must be checked for consistent use. |
| CORS allowlist, request IDs and structured logs | Partial | Infrastructure exists; complete hardening verification remains. |
| Optional phone/device binding | Not started | |
| OpenAPI coverage and accurate schemas for all implemented endpoints | Partial | Contract now includes staff imports, signup review, password reset, venue management, role assignment, and department-scoped unknown-matric claims; many product routes remain uncovered. |
| Demo seed loader and reproducible checklist | Partial | Date-relative repeatable seed now includes one institution, two faculties, four departments/programmes, ten courses/offerings across all registration modes, four lecturer accounts/staff records, 48 students (16 claimed), three venues/scanners, weekly timetable entries, two open and six historical sessions, registration states, varied attendance evidence/overrides, and pending invitation/signup/enrolment records. Login credentials and unclaimed matric/email pairs are printed; newly created scanner keys and invitation token are shown once. `--reset` recreates only the demo institution. Pure fixture tests pass; PostgreSQL execution remains unverified. |
| Manual guide, testing gaps, architecture and decisions docs | Partial | Docs exist and are being updated with implemented workflows; full product checklist remains incomplete. |
| README setup, deployment, frontend contract and Windows instructions | Partial | Setup, seed and frontend contract instructions exist; deployment guidance and full contract coverage remain. |
| Build/vet/format/lint/race/coverage gates | Partial | Build, vet, formatting, tests and targeted coverage pass; lint unavailable and race blocked by CGO. |
| Migration up/down verification on empty PostgreSQL | Partial | The owner confirmed migrations ran successfully against PostgreSQL and created 35 tables. Down-migration behavior and a fresh empty-database replay have not been independently verified in this session. |

## Hardening addendum

| Item | Status | Notes |
|---|---|---|
| Security sweep of every route and permission boundary | Partial | Catalogue/offering resource scopes and source/destination write checks are implemented with unit tests; PostgreSQL execution and broader endpoint hardening remain. |
| Access-token lifetime, refresh cookie attributes, reuse rejection, forced password change | Partial | Core token functionality exists; full configuration and security matrix need verification. |
| Rate-limit failures logged without leaking PII | Partial | Requires endpoint-by-endpoint review. |
| Device authentication separated from user authentication | Done | Separate `X-Device-Key` middleware is wired. |
| CSV import size limit, dry-run preview, row-level errors, commit | Partial | Student and staff CSV preview/commit use scoped validation, 5 MiB limits, row-level errors, and all-or-nothing writes; PostgreSQL behavior remains unverified. |
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
loader (date-relative expanded dataset and fixture tests implemented, PostgreSQL
execution pending); (3) finish imports, verified claims, and invitations
(student/staff preview and commit, signup review, password reset, and scoped
invitations are implemented; live DB verification remains); (4) device-key
management; (5) aggregate reports; (6) remaining hardening; (7) close
power-cut gaps.
Update this file whenever an item changes status.
