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
   Compose, which reads `.env` for variable substitution. `DATABASE_URL` is
   for Go commands running on the host (`localhost`). Compose services always
   connect to the bundled `postgres` service using `POSTGRES_USER`,
   `POSTGRES_PASSWORD`, and `POSTGRES_DB`; a host `DATABASE_URL` does not
   override their in-network database connection.
2. Start PostgreSQL with `docker compose up -d postgres`.
3. Run the API with `go run ./cmd/api`.
4. Check `http://localhost:8080/healthz` and `/readyz`.

The compose file provides an API and PostgreSQL service. Apply migrations with
`go run ./cmd/migrate up` after PostgreSQL is healthy, then run `make seed` to
load the date-relative demo dataset. It includes one institution, two
faculties, four departments and programmes, 60 courses and offerings across all
registration modes (20 of them general-studies courses, spread over 100-500
level), fourteen lecturers, 60 student records (40 claimed accounts) across
100-500 level, four venues and scanners, weekly timetable entries, historical
sessions, varied attendance evidence and overrides, and pending invitation,
signup, and scanner-enrolment records.

**The seed leaves no open class, by design.** Starting one is the lecturer's
job, and it is what gives a room its coordinates, so a pre-opened session would
both pre-empt that and block the venue under the one-open-session-per-venue
rule. One venue ("Innovation Lab") is seeded with no coordinates at all, so the
first lecturer to start a class there exercises the capture. Rerunning the seed
closes any session left open and releases stale student claims, so it converges
on the dataset rather than failing. The seed prints a login
table and the unclaimed matric/email pairs. Use the institution UUID printed
there for login. Newly created scanner keys and the pending invitation token
are displayed only once; reruns preserve them. To recreate only the demo
institution and generate new keys, run `make seed ARGS=--reset`.

### A venue's location comes from the lecturer's device

Nobody types coordinates, and nobody has to visit a room to set them up. A
venue needs only a name; its position is optional metadata that the first
lecturer to teach there supplies without thinking about it.

When a lecturer starts a class, the app sends the position the device already
knows:

```http
POST /sessions
{"offering_id": "...", "venue_id": "...", "rotating_code": true,
 "latitude": 6.5244, "longitude": 3.3792, "accuracy_meters": 12.5}
```

Get the fix from `navigator.geolocation.getCurrentPosition` with
`{enableHighAccuracy: true, timeout: 15000, maximumAge: 0}` and send
`coords.accuracy` as `accuracy_meters` unmodified — the server uses it to
decide whether to trust the reading. The `201` response reports what it did as
`location_source`:

| `location_source` | Meaning |
|---|---|
| `device` | The lecturer's fix centred the session **and** recalibrated the venue, so the next class in that room starts from a good anchor. |
| `venue` | The fix was absent or vaguer than 100 metres, so the venue's stored centre was used instead. |
| `none` | Neither supplied a centre. The session has no geofence and check-ins at it carry no distance. |

**Never block starting a class on geolocation.** If permission is denied or the
fix times out, send the request without the three fields. Attendance still
works; location is evidence, not a gate.

While the session is open, post the current fix again every five minutes and
whenever the app returns to the foreground:

```http
POST /sessions/{id}/location
{"latitude": 6.5244, "longitude": 3.3792, "accuracy_meters": 12.5}
```

The response carries the geofence in force afterwards and an `outcome`:

| `outcome` | Meaning |
|---|---|
| `refined` | The centre moved to the new fix and the venue was recalibrated too. |
| `established` | The venue had no centre at all; this fix became its first. |
| `unchanged` | The fix agreed with the current centre (within 10 metres). No UI needed. |
| `rejected_accuracy` | Vaguer than 100 metres, so it was ignored. No UI needed. |
| `rejected_distance` | Outside the session's own radius, so the check-in area deliberately did **not** move. Prompt the lecturer — "You seem to be away from the room" — rather than erroring, and offer a "Recalibrate this room" action. |

The geofence does not follow the lecturer out of the room: if it did, every
student still sitting in the class would fall outside it the moment the
lecturer stepped into their office. For the case where the lecturer really is
in the right place and the room's saved location is wrong, the explicit
recalibrate action posts the current fix to `POST /venues/{id}/location`, which
is open to administrators over the venue's department and to any lecturer who
teaches there. Every path audits `venue.location_captured`.

`POST /venues` defaults `radius_meters` to 75 metres when a position is given
and caps it at 2000 metres. An existing radius is never overwritten by a
captured fix — a fix says where a room is, not how big it is.

### Set demo seed venues to your location

This applies to the demo seed data only. For real venues use the capture
endpoint above. In Google Maps, right-click your physical position and copy the
coordinates;
enter latitude first and longitude second. Indoor browser location can be off
by tens of metres, so a venue radius of 150–200 metres is a sensible starting
point for check-in testing. The seed places two venues at or within 25 metres
of the configured origin, leaves "Innovation Lab" with no coordinates so the
lecturer-device capture can be exercised, and adds a fourth venue about 5 km
away for testing `location_unverified`.

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
warns that check-ins from your actual location will be recorded as
`location_unverified`.

## When a student's location does not match the venue

Nothing refuses a check-in on location. A student on a cheap or indoor-bound
handset was being denied attendance for a hardware limit they cannot fix, so
the position is now recorded as evidence and, where it cannot be explained,
flagged for the lecturer:

| Fix | Outcome |
|---|---|
| Inside the venue radius | Recorded normally. |
| Outside the radius, but the phone's own reported accuracy covers the gap (capped at 250 metres past it) | Recorded normally. |
| Further out than the accuracy can explain | Recorded, with `location_unverified: true` in the response. The session report shows the row as `web_only_unverified` with reason `location_unverified` for the lecturer to confirm. |
| No position at all (permission denied, or the venue has no centre) | Recorded, with `distance_meters: null`. |

What stops a student checking in for absent friends is **device binding**, not
distance: one device may record at most one student per session, so a second
student from the same phone is refused with `device_already_used` (409).
`device_hash` is required on `POST /checkins` for that reason.

A student whose fix was flagged still has two routes to a clean record, both
already in the API:

1. **The fingerprint scanner in the room.** A matching scan verifies them
   outright — a biometric at the venue is stronger evidence than any GPS fix,
   so a scan upgrades a weak or missing web check-in to `verified`.
2. **A lecturer override.** After the class ends,
   `POST /sessions/{id}/overrides/bulk` sets the status for one or many students
   with a recorded reason code and an audit entry (it returns 409 while the
   session is still open). An override takes precedence over every other
   signal, so it also clears a `location_unverified` flag.

If a whole room comes back `location_unverified`, the venue's stored centre is
wrong rather than the students' phones. The lecturer's next class there fixes it
on its own, or they can recalibrate it immediately with
`POST /venues/{id}/location` from inside the room.

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

### Offering registration mode

When creating an offering (`POST /offerings`), `registration_mode` decides how
students join. A frontend can present the two common choices as a single
toggle:

- **Self-register** → send `"open"`: students register themselves and are
  approved immediately.
- **Needs approval** → send `"approval"`: students register, the request is
  `pending`, and a lecturer/admin approves it via `GET /registration-approvals`
  and the review endpoint.

Two advanced modes also exist: `"auto"` (matching department-and-level students
are enrolled automatically at claim time) and `"closed"` (no new registrations).
The server behavior is unchanged by how the UI labels these; see the
`registration_mode` description in `api/openapi.yaml`.

### Device onboarding

Fingerprint scanners authenticate with an `X-Device-Key` header, never a user
token. A scanner is brought online without any keypad or typing on the device:

1. An administrator provisions the scanner with `POST /devices` (scoped to the
   caller's authority over the venue's department). The response carries a short
   one-time **pairing code**, shown only once.
2. That pairing code is flashed into the firmware image (a config constant); no
   human enters it on the device.
3. On first boot the firmware calls `POST /device/pair` with the code and
   receives its permanent device key, which it stores and sends thereafter.
4. From then on the scanner runs automatically: it polls `GET /device/tasks`
   for the session currently open **at its venue** (no per-class code needed),
   pushes check-ins via `POST /device/scans` (and `POST /device/scans/batch`
   for late offline sync), completes queued fingerprint enrolments, and reports
   health with `POST /device/status`.

To enrol students, an administrator (or a lecturer at the venue) queues each one
with `POST /devices/{id}/enrolments`; the scanner picks the task up from
`GET /device/tasks` (which includes the integer `slot_number` to use) and
confirms with `POST /device/enrolments/{id}/complete`, sending the sensor's
exported fingerprint `template`. Templates are stored centrally, so a scanner at
another venue can recognise the same student: when a session starts, that scanner
calls `POST /device/templates/sync` to load the session roster's templates into
its sensor at the returned slot positions. Fingerprint scans then identify the
student by `slot_number`.
Administrators can reissue a pairing code (`POST /devices/{id}/pairing-code`),
rotate a key so the device must re-pair (`POST /devices/{id}/rotate-key`), or
revoke a scanner (`DELETE /devices/{id}`), and list scanners with their status
and health via `GET /devices`.

Device management is allowed for administrators with authority over the venue's
department (department admins in their department, faculty admins in their
faculty, institution admins institution-wide) and for lecturers at venues they
teach at — a lecturer who is assigned to an offering timetabled at a venue may
provision, list, re-pair, rotate, revoke, and enrol students on scanners at that
venue. Viewing the resulting attendance needs no device role: an assigned
lecturer already sees their sessions' live counts, report, roster, and overrides.

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
  `POST /venues/{id}/location` (store a position captured inside the room so
  nobody types coordinates), and `DELETE /venues/{id}` (soft deactivation).
  Department/faculty scopes
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
