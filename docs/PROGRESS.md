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
| Makefile targets | Partial | Build, test, migration, and seed targets exist; seed now uses human-readable fictional names for lecturer and student accounts/records while keeping academic identifiers in emails and matric numbers; seed execution remains unverified in this session. |
| Dockerfile and API/PostgreSQL Compose stack | Partial | Files exist; end-to-end stack verification is blocked while Docker Desktop is paused. |
| Goose migration and sqlc wiring | Done | Migrations have now been run successfully against PostgreSQL; the owner confirmed the resulting schema contains 35 tables. Generated bindings are checked in; down-migration behavior remains unverified. |
| CI build, vet, lint, SQL generation check, OpenAPI validation, race tests | Partial | Workflow exists; local race testing is blocked by disabled CGO and no native compiler. |

## Phase 1 — Organization and identity

| Item | Status | Notes |
|---|---|---|
| Institution/faculty/department/programme schema and APIs | Partial | Catalogue APIs and resource-derived scope checks are implemented and unit-tested; database behavior and remaining endpoints require verification. |
| Users, role assignments, student records and tenant constraints | Partial | Identity schema and tenant-aware queries exist; faculty/department administrator assignment is scoped and audited; a scoped `GET /users` directory search (q/role/department_id filters, admin-authority-scoped so a department admin sees only their department, a faculty admin their faculty, an institution admin their institution; 403 otherwise) powers the lecturer-assignment and role-assignment pickers, with handler/service tests for scope enforcement and the filters; full schema/query audit and live DB verification of the directory query remain. |
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
| Courses, offerings, lecturer assignments and auto-registration targets | Partial | `GET /courses` supports department/kind filters. `POST /offerings` lets an administrator over the course's department (or a lecturer who owns it) create the offering directly — admins create it with no coordinator and assign lecturers afterwards — while a lecturer without department ownership queues a teaching request scoped administrators review. (Fixes the prior gap where admins, the only role able to create courses, could not then create the offering.) `DELETE /courses/{id}` is authorized the same way, now writes a `course.deleted` audit row, and returns a clean 409 (`resource_in_use`) when the course still has offerings instead of a raw 500 from the `ON DELETE RESTRICT` foreign key. Offering edits/deletes and timetable changes are audited; database verification remains. |
| Full-text/trigram offering search and required filters/defaults | Partial | Search results include registration mode/state, course and department metadata, level, kind, and lecturers; PostgreSQL ranking and result query behavior are not live-tested. |
| Registration modes and roster policy rules | Done | Pure rule logic and unit tests exist. |
| Registration submit/list/review APIs | Partial | Submit returns approved/pending decisions for auto/open/approval modes and a clear conflict for closed; `GET /me/registrations` lists only the caller's pending requests. Lecturer/admin pending-approval queue and single/bulk review are scoped, with review audits; database behavior remains unverified. |
| General-course visibility and department filtering | Partial | Some report/registration filters exist; catalogue read enforcement is incomplete. |
| Academic/catalogue/registration unit tests | Partial | Service tests cover offering input validation, timetable overlap math, approval pagination/bulk result handling, and seed lecturer/course department ownership; SQL authorization and transaction behavior remain unverified. |

## Phase 3 — Venues, sessions and web check-in

| Item | Status | Notes |
|---|---|---|
| Venues, devices, timetable schema and CRUD APIs | Partial | Scoped venue create/update/deactivate and active, scope-filtered listing are audited; inactive venues are excluded from new session starts/timetable assignments. Student timetable and audited offering timetable CRUD remain implemented. Seed venues can be updated without resetting data, and rerunning the seed closes its known legacy open-session fixture; database behavior remains unverified. |
| Session start/end and one-open-session-per-venue constraint | Partial | Session service/schema logic exists; PostgreSQL migration/constraint has not been exercised. |
| Static and rotating codes, code retrieval | Partial | Pure code logic and routes exist; behavior is covered by unit tests. |
| Web check-in, roster policy, geofence and duplicate handling | Partial | Service and unit tests exist; race-safe SQL and live DB behavior are unverified. |
| Session/check-in HTTP and database verification | Partial | `GET /sessions/{id}/live` returns scoped counts and paginated status/flags for polling; service tests cover scope propagation and classification. PostgreSQL behavior remains unverified. |

## Phase 4 — Device API

| Item | Status | Notes |
|---|---|---|
| Device task polling, ETag and heartbeat | Partial | Task API and ETag logic exist; a `POST /device/status` telemetry endpoint records firmware/battery/queue-depth/free-storage on the device record; heartbeat/coverage/telemetry DB behavior needs live verification. |
| Device scan, idempotency, late-synced timestamp and batch endpoint | Partial | Implemented and unit-tested. Scans now reference the matched fingerprint by integer `slot_number` (the sensor's native position, resolved against `device_slots`) rather than a UUID, so firmware needs no UUID map. PostgreSQL behavior is unverified. |
| Enrolment completion | Partial | Device completion endpoint exists, and `POST /devices/{id}/enrolments` lets a scoped administrator (or a lecturer at the venue) queue a student onto the next device slot. `POST /device/enrolments/{id}/complete` now also accepts the sensor's exported fingerprint `template` (base64), stored centrally in `student_fingerprints`; `POST /device/templates/sync` returns a session roster's templates with their slot positions so any scanner serving that session recognises the students (multi-venue / Option B). The pending-enrolment task now carries `slot_number`. Unit-tested; PostgreSQL behavior unverified. |
| Device key generation/hash utility | Partial | The provisioning lifecycle is implemented: `POST /devices` issues a one-time pairing code (flashed into firmware), `POST /device/pair` redeems it for a device key, `POST /devices/{id}/pairing-code` reissues, `POST /devices/{id}/rotate-key` invalidates the key and forces re-pair, `DELETE /devices/{id}` soft-revokes, and `GET /devices` lists scoped scanners with status/health. Management is allowed for venue-department administrators and for lecturers at venues they teach at (an offering timetabled at the venue); all mutations are audited. Deterministic unit tests cover the admin scope helper, pairing, rotation, revocation, and enrolment enqueue; the lecturer-venue SQL path, migration `00006`, and live DB behavior are unverified in-session. |
| Firmware API documentation, flash queue, TLS and time guidance | Partial | Device guide and the README "Device onboarding" section describe pairing-code flashing, first-boot pairing, and automatic polling/scan/enrolment/status; deployment CA must be supplied by operator. |

## Phase 5 — Reports and audit

| Item | Status | Notes |
|---|---|---|
| Session report classification and scanner coverage | Partial | Service, classification, report API, live-session roster/count endpoint, and unit tests exist; nullable student scope is normalized and lecturer/coordinator authorization resolves the session's offering; SQL behavior needs live verification. |
| Roster sheet JSON/CSV/PDF and scope | Partial | API and unit tests cover all three formats; live scope query is unverified. |
| Audited single and bulk overrides | Partial | Bulk override service/API and transactional store exist; live transaction testing remains. |
| Offering/department aggregate attendance reports | Not started | |
| Attendance percentages and below-threshold list | Partial | Student `GET /me/courses` uses the pure percentage math, counts late according to offering policy, and applies override status first; department aggregate endpoints remain absent and database behavior is unverified. |
| Attendance exports | Partial | Session report and roster support JSON, CSV, and PDF; aggregate exports remain. |
| Audit logging for all role/attendance changes | Partial | Offering/timetable changes, teaching request decisions, registration reviews, venue/academic mutations, role assignment, signup review, and password reset now write audit rows transactionally; PostgreSQL verification remains. |
| Disputes | Not started | |
| Email outbox worker | Partial | Invitations, verification, staff-import, and reset messages enqueue in PostgreSQL; a leased batch worker retries delivery with backoff and the development-only mailer logs links/codes. Fixed the stale-lock query's timestamp parameter typing; delivery and retry/concurrency behavior still need live PostgreSQL verification. |

## Phase 6 — Hardening and delivery

| Item | Status | Notes |
|---|---|---|
| Rate limits for login, signup, verification, check-in and device requests | Partial | Removed shared IP/user quotas from authenticated read and session route groups that caused routine endpoints (for example `/me/timetable`) to return 429. Verification has its own five-requests-per-minute-per-IP limit; login, check-in, invitations, and device routes retain their endpoint-specific limits. Persistent per-code failed-attempt caps and trusted-proxy IP handling remain. |
| JSON request limits, strict decoding and error envelope | Partial | Shared helpers exist; every handler must be checked for consistent use. |
| CORS allowlist, request IDs and structured logs | Partial | Infrastructure exists; complete hardening verification remains. |
| Optional phone/device binding | Not started | |
| OpenAPI coverage and accurate schemas for all implemented endpoints | Partial | Contract now includes staff imports, signup review, password reset, venue management, role assignment, and department-scoped unknown-matric claims; many product routes remain uncovered. `GET /offerings/{id}/lecturers` now returns a typed `OfferingLecturer` (id/display_name/email) matching the existing user join, and the spec parses cleanly under `openapi-typescript` after removing duplicate `"500"` response keys (password reset, `createRoleAssignment`) and closing two malformed inline `"200"` response mappings (teaching-request review, bulk registration review) that previously blocked codegen. The new scoped `GET /users` directory endpoint and reusable `DirectoryUser` schema are documented, as are the device-management endpoints (`POST`/`GET /devices`, `/devices/{id}/pairing-code`, `/devices/{id}/rotate-key`, `DELETE /devices/{id}`, `/devices/{id}/enrolments`) and the device-side `POST /device/pair` and `POST /device/status`, with their request/response schemas. |
| Demo seed loader and reproducible checklist | Partial | Date-relative repeatable seed now includes one institution, two faculties, four departments/programmes, a prior ("Semester 1") and a current ("Semester 2") semester under the current academic session, ten courses/offerings (in the current semester) across all registration modes, fourteen lecturer accounts/staff records (one assigned per department plus ten unassigned across departments so the lecturer picker has spare options), 48 students (16 claimed), three venues/scanners, weekly timetable entries, two open and six historical sessions, registration states, varied attendance evidence/overrides, and pending invitation/signup/enrolment records. Login credentials and unclaimed matric/email pairs are printed; newly created scanner keys and invitation token are shown once. `--reset` recreates only the demo institution. Pure fixture tests pass; PostgreSQL execution remains unverified. |
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
| Scoped, ordered roster sheet in JSON, CSV, and PDF | Partial | API and format unit tests exist; live scope query is unverified. |
| Device flash queue, late-sync timestamp and reboot guidance | Partial | Client timestamp and batch route/documentation exist; no firmware is in this repository. |
| OpenAPI/manual/testing-gap updates | Partial | Updated for the implemented addendum slice; recheck as remaining work is completed. |

## Current work order

The active priority list is: (1) catalogue and offering scopes (implementation
and unit checks complete; SQL runtime verification still needed); (2) seed
loader (date-relative expanded dataset and fixture tests implemented, PostgreSQL
execution pending); (3) finish imports, verified claims, and invitations
(student/staff preview and commit, signup review, password reset, and scoped
invitations are implemented; live DB verification remains); (4) device-key
management (provisioning, pairing, rotation, revocation, enrolment enqueue, and
status telemetry are implemented and unit-tested; migration `00006` and live DB
verification remain); (5) aggregate reports; (6) remaining hardening; (7) close
power-cut gaps.
Update this file whenever an item changes status.
