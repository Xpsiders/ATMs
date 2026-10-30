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
| Dockerfile and API/PostgreSQL Compose stack | Partial | Containerized migration, seed, and API services always connect to the bundled `postgres` service, deriving credentials/database from the Postgres service settings; the host `DATABASE_URL` cannot override that connection. `docker compose config` resolves app services to the service hostname. End-to-end stack verification remains outstanding. |
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
| Venues, devices, timetable schema and CRUD APIs | Partial | Scoped venue create/update/deactivate and active listing are audited; `GET /venues` now exposes every venue in the authenticated institution to lecturers and administrator roles. Scanner listing and all management actions require institution/platform admin; denials log role, target ID, and reason. `GET /venues/{id}/scanner-status` is tenant-scoped and exposes only status/last-seen to venue-reader roles. Tests cover each management role/route and cross-tenant access; live DB behavior remains unverified. Inactive venues are excluded from new session starts/timetable assignments. A venue's coordinates are optional and come from the lecturer's device: `POST /sessions` captures them at start and `POST /sessions/{id}/location` refines them mid-class, both writing back to the venue and auditing `venue.location_captured`. `POST /venues/{id}/location` remains as an explicit recalibrate and is open to administrators over the venue's department and to a lecturer who teaches there. `POST /venues` needs only a name; `radius_meters` defaults to 75 m with a position and both routes cap it at 2000 m. Student timetable and audited offering timetable CRUD remain implemented. Seed venues can be updated without resetting data; database behavior remains unverified. |
| Session start/end and one-open-session-per-venue constraint | Partial | Session service/schema logic exists; PostgreSQL migration/constraint has not been exercised. |
| Static and rotating codes, code retrieval | Partial | Pure code logic and routes exist; behavior is covered by unit tests. The rotating-code window is configurable via `CHECKIN_CODE_WINDOW_SECONDS` (default 30 s, bounded 10-300) and shared by the issuer and the check-in verifier, which must agree or codes verify inconsistently. The window is the remaining bound on a student forwarding a code to an absent friend. |
| Web check-in, roster policy, geofence and duplicate handling | Partial | Service and unit tests exist; race-safe SQL and live DB behavior are unverified. Web check-in requires `lat`/`lng`/`accuracy` plus a valid session code. The code remains decisive: outside-radius coordinates are recorded, not rejected, and may set `location_unverified`. Web-only attendance is classified as `proxy` for lecturer review, with scanner/location reasons; a matching fingerprint scan marks the student present regardless of phone location. Per-session device binding still rejects a second student from the same device with `device_already_used` (409), served by the partial index added in migration `00008`. |
| Session/check-in HTTP and database verification | Partial | `GET /sessions/{id}/live` returns scoped counts and paginated status/flags for polling; service tests cover scope propagation and classification. PostgreSQL behavior remains unverified. |

## Phase 4 — Device API

| Item | Status | Notes |
|---|---|---|
| Device task polling, ETag and heartbeat | Partial | Task API and ETag logic exist; a `POST /device/status` telemetry endpoint records firmware/battery/queue-depth/free-storage on the device record; heartbeat/coverage/telemetry DB behavior needs live verification. |
| Device scan, idempotency, late-synced timestamp and batch endpoint | Partial | Implemented and unit-tested. Scans now reference the matched fingerprint by integer `slot_number` (the sensor's native position, resolved against `device_slots`) rather than a UUID, so firmware needs no UUID map. PostgreSQL behavior is unverified. |
| Enrolment completion | Partial | Device completion endpoint exists, and `POST /devices/{id}/enrolments` lets a scoped administrator queue a student onto the next device slot. The portal submits the account UUID, while the enrolment API historically looked up only the student-record UUID; the tenant-scoped lookup now resolves either an existing student-record ID or its claimed account ID to the canonical record, while unknown IDs still return 404. Live PostgreSQL evidence confirmed all 60 seeded student profiles exist (40 claimed accounts, 20 unclaimed), and Amara's account UUID `...0050` links to student-record UUID `...0112`. The seed now removes its old deterministic scanner slots and pending enrolment tasks, leaving seeded students to be manually enrolled; new slot assignment is capped at firmware-supported 1-300. The task ETag test covers pending-enrolment changes. `POST /device/enrolments/{id}/complete` accepts the sensor's exported fingerprint `template` (base64), stored centrally in `student_fingerprints`; `POST /device/templates/sync` returns roster templates with slot positions. Unit tests pass; no live enqueue was issued to avoid dispatching a task to the paired scanner. |
| Device key generation/hash utility | Partial | The provisioning lifecycle is implemented: `POST /devices` issues a one-time pairing code (flashed into firmware), `POST /device/pair` redeems it for a device key, `POST /devices/{id}/pairing-code` reissues, `POST /devices/{id}/rotate-key` invalidates the key and forces re-pair, `DELETE /devices/{id}` soft-revokes, and `GET /devices` lists institution-scoped scanners with status/health. Only institution/platform admins may manage scanners; every management route has role-matrix coverage and denial logs include role, target ID, and reason. Migration `00006` and live DB behavior remain unverified. |
| Firmware API documentation, flash queue, TLS and time guidance | Partial | Device guide and the README "Device onboarding" section describe pairing-code flashing, first-boot pairing, and automatic polling/scan/enrolment/status; deployment CA must be supplied by operator. |

## Phase 5 — Reports and audit

| Item | Status | Notes |
|---|---|---|
| Session report classification and scanner coverage | Partial | Service, classification, report API, live-session roster/count endpoint, and unit tests exist; nullable student scope is normalized and lecturer/coordinator authorization resolves the session's offering; SQL behavior needs live verification. Any web-only check-in is now classified as `proxy` for lecturer review, with reasons `location_unverified`, `scanner_unavailable`, `scanner_offline`, or `no_fingerprint`; attendance status remains present/late because the valid code is authoritative. A fingerprint scan of a student record marks the student present and overrides distant web GPS. `SessionReportPage` derives the location flag in SQL and has a deterministic query-contract test, but sqlc was not re-run in-session (no binary, module fetch blocked), so the generated row struct was hand-edited to match. |
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
| Lecturer-device venue location and advisory geofence | Done | Venue/session coordinates remain optional because lecturers can start class without location permission. Student web check-in now requires latitude, longitude, and accuracy; a valid code still records an outside-radius fix, and the report marks web-only attendance as `proxy`. A matching fingerprint scan confirms presence regardless of phone location. Per-session device binding is also retained. `POST /sessions` still captures lecturer device location at start, and session-location/recalibration behavior is unchanged. Earlier database verification exercised migration `00008`, session location capture/recalibration, check-in distance recording, and device binding; current mandatory-student-GPS and proxy-report changes are covered by deterministic unit tests but are not yet live-DB verified. |
| OpenAPI coverage and accurate schemas for all implemented endpoints | Partial | Check-in now documents required student `lat`/`lng`/`accuracy`, required code and device hash, the `location_required` error, outside-radius acceptance, and the `proxy` report classification with fingerprint precedence. Session start's lecturer location remains optional. Other route coverage and existing OpenAPI recommended-rule warnings remain as noted. |
| Demo seed loader and reproducible checklist | Partial | Date-relative repeatable seed includes one institution, two faculties, four departments/programmes, a prior ("Semester 1") and a current ("Semester 2") semester under the current academic session, **60 courses/offerings** (the ten hand-written ones plus fifty more, twenty general-studies, spread over 200-500 level and assigned across all fourteen lecturers), fourteen lecturer accounts/staff records, **60 students (40 claimed, 20 unclaimed) across 100-500 level**, four venues/scanners with no seeded coordinates or radius, weekly timetable entries for assigned offerings at every venue, **nine closed historical sessions at LT 2 only** (the other venues remain unoccupied but are not disabled), registration states (every approval offering carries both an approved registration and a pending one), varied attendance evidence/overrides including check-ins without fabricated locations, device hashes on every seeded check-in, and pending invitation/signup/enrolment records. Each offering is written as both coordinator and `offering_lecturers` assignment; deterministic plan validation checks lecturer/course department agreement, complete timetable/session references, unique scanner links, and that every seeded lecturer can start an assigned offering. Rerunning the seed releases stale student claims and closes any session left open; known fixture session IDs are reused and moved to LT 2. Login credentials and unclaimed matric/email pairs are printed; newly created scanner keys and invitation token are shown once. `--reset` recreates only the demo institution. Current fixture tests are deterministic; the updated seed has not been run against PostgreSQL in this task. |
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
| Web-only `proxy` classification during scanner outages | Done | Pure classification logic and tests cover scanner availability, location evidence, and fingerprint precedence. |
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
