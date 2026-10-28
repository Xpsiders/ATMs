# Manual test guide

This guide covers implemented routes. The seed loader creates a reproducible
demo dataset; PostgreSQL-backed behavior still needs live verification. Device
key provisioning APIs and wider product workflows are outstanding. Do not use
production student data.

Commands below use Windows CMD-compatible quoting. In bash, replace escaped
double quotes in `--data` JSON with single-quoted JSON strings.

## Current route checks

1. **Start PostgreSQL and apply migrations.**

   ```cmd
   docker compose up -d postgres
   set DATABASE_URL=postgres://attendance:attendance@localhost:5432/attendance?sslmode=disable
   go run ./cmd/migrate up
   ```

   Seed the demo dataset once migrations are applied:

   ```cmd
   make seed
   ```

   In PowerShell without GNU Make, use `go run ./scripts/seed` (append
   `--reset` to recreate only the demo institution). The command prints a
   table of login credentials and all unclaimed matric/email pairs. Its
   date-relative dataset includes two faculties, four departments/programmes,
   60 courses and offerings across all four registration modes, 60 student
   records, fourteen lecturers, four venues and scanners, timetable entries,
   nine closed sessions at LT 2, varied attendance evidence and overrides, plus
   pending invitation, signup, and scanner-enrolment records. Every offering is
   linked to its assigned lecturer, and every seeded lecturer can start at
   least one offering. It prints
   plaintext scanner keys and the pending invitation token only when each is
   first created; save them securely, as a rerun does not reveal or rotate
   existing secrets. Use `make seed ARGS=--reset` or
   `go run ./scripts/seed --reset` only when you intend to delete and recreate
   the demo institution.

   **Seed venue locations.** Every seeded venue has its own scanner and
   timetable entries for assigned offerings, but starts without coordinates
   or a radius. Only LT 2 has seeded historical sessions; the other venues
   remain unoccupied, not disabled. No venue-origin environment variables are
   needed. A lecturer can start an assigned offering at any venue. When they
   start a class, the API can capture their device's location for the selected
   venue; denying browser location still allows the session to start without a geofence. To
   clear locations captured by an earlier test run without reseeding the rest
   of the data, run:

   ```powershell
   $env:DATABASE_URL = "postgres://attendance:attendance@localhost:5432/attendance?sslmode=disable"
   go run ./scripts/seed --update-venues
   ```

   Or run `docker compose run --rm seed seed --update-venues`. A regular seed
   run also updates its known historical session fixtures.

   Start the API in a second CMD window with configured secrets:

   ```cmd
   set DATABASE_URL=postgres://attendance:attendance@localhost:5432/attendance?sslmode=disable
   set ALLOWED_ORIGIN=http://localhost:5173
   set JWT_SECRET=replace-this-local-secret-with-at-least-32-bytes
   set CODE_HMAC_SECRET=replace-this-local-code-secret-with-32-bytes
   go run ./cmd/api
   ```

   The compose service uses development placeholders and must not be exposed
   publicly.

2. **Health and readiness.**

   ```cmd
   curl.exe -i http://localhost:8080/healthz
   curl.exe -i http://localhost:8080/readyz
   ```

   Expect `200 {"status":"ok"}` and `200 {"status":"ready"}`. Stop PostgreSQL
   and `/readyz` should return `503` with error code `database_unavailable`.

3. **Log in and retain credentials.** Use one of the demo credentials printed
   by `make seed`:

   ```cmd
   curl.exe -i -X POST http://localhost:8080/auth/login -H "Content-Type: application/json" --data "{\"institution_id\":\"INSTITUTION_UUID\",\"email\":\"USER_EMAIL\",\"password\":\"USER_PASSWORD\"}"
   ```

   For example, institution admin is `admin@attendance.test` /
   `DemoAdmin-2026!`. The seed command prints credentials for platform admin,
   institution admin, faculty admin, each department admin, lecturers, and a
   set of claimed students. Use each account to verify its role-scoped frontend
   views.
   All demo accounts use the printed institution ID when logging in.
   Expect a JSON `access_token`, `expires_in: 900`, `force_password_change`,
   `must_change_password`, `id`, `name`, `email`, `status`, and a `roles` array.
   Each role includes `role`, `scope_type`, and `scope_id`; student roles also
   include their locked `department_id` and `level`. Fetch the profile again
   with the access token:

   ```cmd
   curl.exe -i http://localhost:8080/me -H "Authorization: Bearer ACCESS_TOKEN"
   ```

   Expect `200` with the same user profile fields and role scopes. This route
   remains available when a forced password change is required so the client
   can read the `must_change_password` flag.
   Save the access token and the refresh cookie from `Set-Cookie`. Use the
   access token as
   `Authorization: Bearer ACCESS_TOKEN`. Refresh with
   `curl.exe -i -X POST http://localhost:8080/auth/refresh -b "attendance_refresh=REFRESH_COOKIE"`.
   Logout with `curl.exe -i -X POST http://localhost:8080/auth/logout -b
   "attendance_refresh=REFRESH_COOKIE"`.

   **Student live classes.** With the claimed demo student's token, query only
   currently open sessions for approved registrations. The response includes
   session/course/venue/lecturer metadata and check-in state, but never an
   attendance code:

   ```cmd
   curl.exe -i http://localhost:8080/me/live-classes -H "Authorization: ******"
   ```

   **Lecturer offerings and institution venues.** The lecturer's offering list
   is filtered using their coordinator/assignment visibility. Venue listing is
   limited to the authenticated institution and is available to lecturers and
   administrators:

   ```cmd
   curl.exe -i "http://localhost:8080/me/offerings?limit=20&offset=0" -H "Authorization: ******"
   curl.exe -i "http://localhost:8080/venues?limit=20&offset=0" -H "Authorization: ******"
   ```

   `/venues` returns `items`, `limit`, `offset`, and institution-wide `total`.
   A student caller receives `403 access_denied`.

4. **Student claim and email verification.** A matching matric number and
   record email starts verification. In development, read the one-time code
   from the API log entry named `development email`. Signup start returns the
   same `202 request_received` acknowledgement for matching, mismatched,
   already-claimed, and unknown student records. Unknown matric numbers create
   a department review request; mismatches and already-claimed records do not
   trigger an email or a new account. Invalid, expired, used, and already-claimed
   verification attempts return the same `400 invalid_verification` response.

   ```cmd
   curl.exe -i -X POST http://localhost:8080/auth/signup-claim -H "Content-Type: application/json" --data "{\"institution_id\":\"INSTITUTION_UUID\",\"matric_no\":\"MATRIC_NO\",\"email\":\"EMAIL_ON_STUDENT_RECORD\",\"department_id\":\"DEPARTMENT_UUID\"}"
   curl.exe -i -X POST http://localhost:8080/auth/verify-email -H "Content-Type: application/json" --data "{\"institution_id\":\"INSTITUTION_UUID\",\"matric_no\":\"MATRIC_NO\",\"code\":\"123456\",\"password\":\"a-long-new-password\"}"
   ```

   The first call returns `202 request_received`. The second returns `201`, an
   access token, and the refresh cookie.    The required department identifies the review scope for unknown matric
   requests. For a matching student record, the department and level still come
   from that record, not request fields. Eligible auto-mode offerings
   are registered in the same transaction as the account claim. Admin review
   routes for unknown matric requests are described in the administrator
   workflow below.

5. **Invite a staff member.** As an institution admin, faculty admin, or
   department admin, invite a user only into a role/scope that you manage.
   The API sends the 7-day invitation token and institution ID to the invited
   email. With the development mailer, retrieve the token from the
   `development email` log entry:

   ```cmd
   curl.exe -i -X POST http://localhost:8080/invitations -H "Authorization: Bearer ACCESS_TOKEN" -H "Content-Type: application/json" --data "{\"email\":\"lecturer@example.test\",\"role\":\"lecturer\",\"scope_type\":\"department\",\"department_id\":\"DEPARTMENT_UUID\"}"
   curl.exe -i -X POST http://localhost:8080/invitations/accept -H "Content-Type: application/json" --data "{\"institution_id\":\"INSTITUTION_UUID\",\"token\":\"INVITATION_TOKEN_FROM_EMAIL\",\"display_name\":\"Demo Lecturer\",\"password\":\"a-long-new-password\"}"
   ```

   Creation returns `202 invitation_sent`. Acceptance returns `201`, an access
   token, and the HttpOnly refresh cookie. Reusing or presenting an expired
   token returns `400 invalid_invitation`. An administrator outside the target
   scope receives `403 invitation_scope_denied`; platform-admin invitations are
   not supported.

6. **Student CSV import preview and commit.** As an institution admin or
   department admin, create a CSV file with these exact columns and a department
   in your management scope:

   ```cmd
   (echo matric_no,email,display_name,department_id,level& echo MAT300A,student-300a@attendance.test,Casey Demo,DEPARTMENT_UUID,300)>students.csv
   ```

   Preview without writing records, then commit only after reviewing the
   returned `rows` and `errors`:

   ```cmd
   curl.exe -i -X POST http://localhost:8080/imports/students/preview -H "Authorization: ******" -H "Content-Type: text/csv" --data-binary "@students.csv"
   curl.exe -i -X POST http://localhost:8080/imports/students -H "Authorization: ******" -H "Content-Type: text/csv" --data-binary "@students.csv"
   ```

   Preview returns `200` with row-level errors and does not persist records.
   Commit returns `201` if all rows pass validation, `422` with row errors if
   validation fails, or `409 matric_number_conflict` if a duplicate matric
   number was inserted concurrently. Uploads larger than 5 MiB return `413`.
   Import errors and logs must not disclose CSV row values.

7. **Session control.** An assigned offering lecturer or coordinator can start
   a session. Omit the location fields to copy the venue's centre, or send the
   lecturer device's own fix to centre the class on it and recalibrate the room:

   ```cmd
   curl.exe -i -X POST http://localhost:8080/sessions -H "Authorization: Bearer ACCESS_TOKEN" -H "Content-Type: application/json" --data "{\"offering_id\":\"OFFERING_UUID\",\"venue_id\":\"VENUE_UUID\",\"rotating_code\":true,\"late_after_min\":10,\"latitude\":6.5244,\"longitude\":3.3792,\"accuracy_meters\":12.5}"
   ```

   Check `location_source` in the response: `device` when the fix was used and
   the venue recalibrated, `venue` when the fix was absent or vaguer than 100
   metres, `none` when neither supplied a centre. Send the body without the
   three location fields and confirm the session still starts — a denied
   geolocation permission must never block a class.

   **Re-check the position during the session.** With the session open:

   ```cmd
   curl.exe -i -X POST http://localhost:8080/sessions/SESSION_UUID/location -H "Authorization: Bearer ACCESS_TOKEN" -H "Content-Type: application/json" --data "{\"latitude\":6.5244,\"longitude\":3.3792,\"accuracy_meters\":12.5}"
   ```

   Expect `200` with `outcome`, the geofence in force afterwards, and
   `moved_meters`. Exercise each outcome: a point within 10 metres of the
   centre gives `unchanged`; a point 30–50 metres away (inside the radius)
   gives `refined` and must also update the venue row; a test coordinate at
   least 1 km away from the session centre gives `rejected_distance` and must
   leave `lecture_sessions.latitude` untouched; an `accuracy_meters` of `250`
   gives `rejected_accuracy`. Against a session started at a venue whose
   `latitude`/`longitude`/`radius_meters` are NULL, a fix with good accuracy
   gives `established` and fills in both the session and the venue. Confirm an
   `audit_log` row with action `venue.location_captured` for each accepted fix,
   and a `409 session_closed` after the session ends.

   The seeded lecturers are assigned to the seeded offerings, and each
   offering has a timetable entry linked to its venue. Use the IDs printed by
   the seed command if supplying `timetable_id`.

   Expect `201` with a session, six-digit code, and `seconds_remaining`. Save
   `session.id`. Starting another session at the same venue must return `409`
   and `venue_session_already_open`, including the open `session_id`.

   Fetch/refresh the code:

   ```cmd
   curl.exe -i http://localhost:8080/sessions/SESSION_UUID/code -H "Authorization: Bearer ACCESS_TOKEN"
   ```

   To convert a rotating code to a fixed code during a power interruption, an
   assigned lecturer or coordinator can make this one-way change. Save the
   returned six-digit code; it remains valid until the session ends:

   ```cmd
   curl.exe -i -X PATCH http://localhost:8080/sessions/SESSION_UUID -H "Authorization: ******" -H "Content-Type: application/json" --data "{\"rotating\":false}"
   ```

   Expect `200` with the fixed `code`. A second conversion returns `409
   code_already_fixed`.

   If the scanner loses power, record the outage against the open session:

   ```cmd
   curl.exe -i -X POST http://localhost:8080/sessions/SESSION_UUID/scanner-status -H "Authorization: ******" -H "Content-Type: application/json" --data "{\"state\":\"unavailable\",\"note\":\"Scanner lost power\"}"
   ```

   Expect `200 {"state":"unavailable"}`. The marker and its audit event are
   stored together. Session report endpoints are not yet available to inspect
   the resulting classification.

   End the session:

   ```cmd
   curl.exe -i -X POST http://localhost:8080/sessions/SESSION_UUID/end -H "Authorization: Bearer ACCESS_TOKEN"
   ```

8. **Web check-in.** Use a student access token and an open session code:

   ```cmd
   curl.exe -i -X POST http://localhost:8080/checkins -H "Authorization: Bearer STUDENT_ACCESS_TOKEN" -H "Content-Type: application/json" --data "{\"session_id\":\"SESSION_UUID\",\"code\":\"123456\",\"lat\":6.5244,\"lng\":3.3792,\"accuracy\":8.5}"
   ```

   Replace the example code and coordinates with the actual code and a point
   within the session radius. Expect `201` with `status`, `on_roster`,
   `accuracy_poor`, `distance_meters`, and `location_unverified: false`. Try a
   wrong/expired code (`wrong_or_expired_code`), an unregistered student under
   `registered_only` (`not_on_roster`), an ended session (`session_closed`),
   and repeating a successful check-in (`already_checked_in`, HTTP `409`).

   **Confirm location never refuses anyone.** Use the same open session and
   valid code, but set `lat` and `lng` to the coordinates printed for **Far
   Test Venue (5 km)** with a small `accuracy` such as `5`. Expect `201` with
   `location_unverified: true`, not a `400`: a position the phone's own error
   cannot explain is flagged for the lecturer, never refused. Omit `lat`, `lng`,
   and `accuracy` entirely for another student and expect `201` with
   `distance_meters: null`. Use a fresh registered student for each attempt
   because check-ins are unique per student/session.

   **Test the poor-GPS tolerance.** Send a point just outside the session
   radius (the seed radius defaults to 150 metres, so about 0.0018 degrees of
   latitude is roughly 200 metres) together with an `accuracy` large enough to
   cover the gap, for example `120`. Expect `201` with
   `location_unverified: false`: within the phone's own reported error, capped
   at 250 metres past the radius, the fix is simply accepted.

   **Test device binding.** This is what now stops a student checking in for
   absent friends. Check one student in, then send a second registered
   student's check-in for the same session with the same `device_hash`. Expect
   `409 device_already_used`. Confirm a request with no `device_hash` at all is
   refused with `400 invalid_checkin_location`, since an absent hash cannot be
   treated as an opt-out.

   Then open `GET /sessions/{id}/live` (or the session report once closed) and
   confirm the tolerated student appears as `web_only_unverified` with reason
   `location_unverified`. A matching fingerprint
   scan for that student must upgrade the row to `verified`, and a closed-session
   `POST /sessions/{id}/overrides/bulk` with `status: present` must take
   precedence over both.

   **Recalibrate a room explicitly.** This is the "Recalibrate this room"
   action behind a `rejected_distance` outcome. As an administrator over the
   venue's department, or as a lecturer who teaches at the venue:

   ```cmd
   curl.exe -i -X POST http://localhost:8080/venues/VENUE_UUID/location -H "Authorization: Bearer ACCESS_TOKEN" -H "Content-Type: application/json" --data "{\"latitude\":6.5244,\"longitude\":3.3792,\"accuracy_meters\":12.5}"
   ```

   Expect `200` with the stored coordinates and radius. Repeat with
   `accuracy_meters` of `250` and expect `400 location_too_inaccurate`. Repeat
   as a user who is neither an administrator over the department nor a lecturer
   timetabled at the venue and expect `403`. Confirm an `audit_log` row with
   action `venue.location_captured`, and that a session started afterwards at
   that venue (with no location fields in the body) inherits the newly captured
   coordinates. Also create a venue with only a name (`POST /venues`) and
   confirm its `latitude`, `longitude`, and `radius_meters` are all NULL rather
   than zero.

9. **Scanner task polling.** Device keys are not yet provisioned by an API.
   For a test, use a device key whose SHA-256 hash has been inserted for a
   development device:

   ```cmd
   curl.exe -i http://localhost:8080/device/tasks -H "X-Device-Key: DEVICE_KEY"
   ```

   Expect `200`, `ETag`, `active_session` / `pending_enrolment` (each may be
   `null`), and server UTC `server_time`. Poll with the previous ETag:

   ```cmd
   curl.exe -i http://localhost:8080/device/tasks -H "X-Device-Key: DEVICE_KEY" -H "If-None-Match: \"ETAG_VALUE\""
   ```

   An unchanged task state returns `304` with no body.

10. **Scanner scan and offline batch.**

   ```cmd
   curl.exe -i -X POST http://localhost:8080/device/scans -H "X-Device-Key: DEVICE_KEY" -H "Content-Type: application/json" --data "{\"slot_id\":\"SLOT_UUID\",\"confidence\":0.98,\"client_scanned_at\":\"2026-10-20T10:26:52Z\"}"
   curl.exe -i -X POST http://localhost:8080/device/scans/batch -H "X-Device-Key: DEVICE_KEY" -H "Content-Type: application/json" --data "{\"scans\":[{\"slot_id\":\"SLOT_UUID\",\"confidence\":0.98},{\"slot_id\":\"ANOTHER_SLOT_UUID\",\"confidence\":0.94}]}"
   ```

   The first new scan returns `201`; a repeated session/student scan returns
   `200` with `duplicate: true`. Batch returns one result per item. With no
   active venue session, the single-scan endpoint returns `409
   no_active_session`; batch carries that code in the item's `error_code`.
   Invalid items include `invalid_slot` or `invalid_confidence`. For offline
   queue testing, preserve the original `client_scanned_at` in non-volatile
   storage and submit it after reconnecting; the server attaches the scan to
   the session active at the device venue at that timestamp.

   Complete an enrolment task:

   ```cmd
   curl.exe -i -X POST http://localhost:8080/device/enrolments/TASK_UUID/complete -H "X-Device-Key: DEVICE_KEY" -H "Content-Type: application/json" --data "{\"status\":\"done\"}"
   ```

   Expect `204`. A task not pending for that device returns `404
   enrolment_not_found`.

## Organization, catalogue, and registration routes

The authenticated catalogue routes include faculties, departments,
programmes, academic sessions, semesters, courses, and offerings. Each
collection accepts `limit` and `offset`; `GET /courses` can additionally filter
by `department_id` and `kind`. Lecturer offering creation assigns the lecturer
automatically when the course belongs to their department. A cross-department
course creates a pending teaching request for scoped administrator review.
For example:

```cmd
curl.exe -i "http://localhost:8080/offerings/search?q=ece301&limit=20&offset=0" -H "Authorization: Bearer ACCESS_TOKEN"
curl.exe -i "http://localhost:8080/offerings/search?q=Cicuits&limit=20&offset=0" -H "Authorization: Bearer ACCESS_TOKEN"
curl.exe -i -X POST http://localhost:8080/offerings/OFFERING_UUID/registrations -H "Authorization: Bearer STUDENT_ACCESS_TOKEN"
curl.exe -i "http://localhost:8080/offerings/OFFERING_UUID/registrations?status=pending&limit=50&offset=0" -H "Authorization: Bearer ADMIN_ACCESS_TOKEN"
curl.exe -i -X PATCH http://localhost:8080/offerings/OFFERING_UUID/registrations/STUDENT_RECORD_UUID -H "Authorization: Bearer ADMIN_ACCESS_TOKEN" -H "Content-Type: application/json" --data "{\"status\":\"approved\"}"
```

Additional lecturer/admin workflows can be exercised with:

```cmd
curl.exe -i "http://localhost:8080/courses?department_id=DEPARTMENT_UUID&kind=departmental&limit=50&offset=0" -H "Authorization: ******"
curl.exe -i -X POST http://localhost:8080/offerings -H "Authorization: ******" -H "Content-Type: application/json" --data "{\"course_id\":\"COURSE_UUID\",\"semester_id\":\"SEMESTER_UUID\",\"level\":200,\"registration_mode\":\"approval\",\"roster_policy\":\"registered_only\",\"min_attendance_pct\":75,\"auto_targets\":[],\"timetable\":[{\"weekday\":2,\"starts_at\":\"09:00\",\"ends_at\":\"10:00\",\"venue_id\":\"VENUE_UUID\"}]}"
curl.exe -i "http://localhost:8080/teaching-requests?limit=50&offset=0" -H "Authorization: ******"
curl.exe -i -X PATCH http://localhost:8080/teaching-requests/REQUEST_UUID -H "Authorization: ******" -H "Content-Type: application/json" --data "{\"status\":\"approved\"}"
curl.exe -i "http://localhost:8080/offerings/OFFERING_UUID/timetable" -H "Authorization: ******"
curl.exe -i -X POST http://localhost:8080/offerings/OFFERING_UUID/timetable -H "Authorization: ******" -H "Content-Type: application/json" --data "{\"weekday\":2,\"starts_at\":\"09:00\",\"ends_at\":\"10:00\",\"venue_id\":\"VENUE_UUID\"}"
curl.exe -i -X PUT http://localhost:8080/offerings/OFFERING_UUID/timetable/SLOT_UUID -H "Authorization: ******" -H "Content-Type: application/json" --data "{\"weekday\":2,\"starts_at\":\"09:00\",\"ends_at\":\"10:00\",\"venue_id\":\"VENUE_UUID\"}"
curl.exe -i -X DELETE http://localhost:8080/offerings/OFFERING_UUID/timetable/SLOT_UUID -H "Authorization: ******"
curl.exe -i "http://localhost:8080/registration-approvals?limit=50&offset=0" -H "Authorization: ******"
curl.exe -i -X POST http://localhost:8080/offerings/OFFERING_UUID/registrations/bulk -H "Authorization: ******" -H "Content-Type: application/json" --data "{\"student_ids\":[\"STUDENT_RECORD_UUID\"],\"status\":\"approved\"}"
curl.exe -i "http://localhost:8080/sessions/SESSION_UUID/live?limit=50&offset=0" -H "Authorization: ******"
```

`GET /sessions/{id}/live` supports polling and includes scope-filtered counts
plus roster status and evidence flags. `GET /me/offerings` includes
`session_open` and the next timetable slot. These database-backed results have
not yet been verified on a fresh stack.

Search returns a paginated ranked list. Student search defaults to the
student's home department and level. Registration mode controls whether the
result is approved, pending, or rejected. The handlers and seed data are
implemented, but database-backed behavior has not yet been verified on a
fresh stack.

For a prepared database, verify department-scoped catalogue access with
department administrator accounts from two different departments:

```cmd
curl.exe -i http://localhost:8080/departments/OTHER_DEPARTMENT_UUID -H "Authorization: Bearer DEPT_A_TOKEN"
curl.exe -i http://localhost:8080/courses/OTHER_DEPARTMENT_COURSE_UUID -H "Authorization: Bearer DEPT_A_TOKEN"
curl.exe -i "http://localhost:8080/departments?limit=200&offset=0" -H "Authorization: Bearer DEPT_A_TOKEN"
curl.exe -i -X PATCH http://localhost:8080/courses/OWN_COURSE_UUID -H "Authorization: Bearer DEPT_A_TOKEN" -H "Content-Type: application/json" --data "{\"department_id\":\"OTHER_DEPARTMENT_UUID\"}"
```

The first two requests must return `404` (the same result as an unknown
resource); the department collection must contain only the caller's department
and the move attempt must return `403`. Lecturer/coordinator offering reads
are limited to assigned offerings. For a general course, a home-department
administrator may see only their department's registrations and report rows;
they must not receive the full offering roster. Student offering search is
limited to their home department, level, or a matching general-course
auto-registration target.

## Reports, power-cut recovery, overrides, and roster sheets

To simulate power loss, unplug or disable the scanner during an open session,
then mark the outage. Switch the rotating code to a fixed code and have
students continue to check in with that code:

```cmd
curl.exe -i -X POST http://localhost:8080/sessions/SESSION_UUID/scanner-status -H "Authorization: Bearer LECTURER_TOKEN" -H "Content-Type: application/json" --data "{\"state\":\"unavailable\",\"note\":\"Scanner lost power during lecture\"}"
curl.exe -i -X PATCH http://localhost:8080/sessions/SESSION_UUID -H "Authorization: Bearer LECTURER_TOKEN" -H "Content-Type: application/json" --data "{\"rotating\":false}"
curl.exe -i "http://localhost:8080/sessions/SESSION_UUID/report?limit=50&offset=0" -H "Authorization: Bearer LECTURER_TOKEN"
curl.exe -i "http://localhost:8080/sessions/SESSION_UUID/roster-sheet?format=csv&limit=200&offset=0" -H "Authorization: Bearer LECTURER_TOKEN"
```

The scanner outage and fixed-code conversion each produce an audit entry.
During the outage, web check-ins are classified `web_only_unverified`; the
report includes scanner coverage state and offline intervals. The roster sheet
is ordered and paginated. Both report routes support JSON, CSV, and PDF via the
`format` query parameter. PDF output is limited to the requested page; use
`limit` and `offset` for pagination, up to 200 rows per request.

Save the PDF responses directly to files (omit `-i` so HTTP headers are not
written into the PDF):

```cmd
curl.exe -sS "http://localhost:8080/sessions/SESSION_UUID/report?format=pdf&limit=200&offset=0" -H "Authorization: ******" --output session-report.pdf
curl.exe -sS "http://localhost:8080/sessions/SESSION_UUID/roster-sheet?format=pdf&limit=200&offset=0" -H "Authorization: ******" --output session-roster.pdf
```

After ending the session, apply a scoped, audited bulk override. Each requested
student receives an individual result, so a bad or out-of-scope student does
not prevent other valid overrides:

```cmd
curl.exe -i -X POST http://localhost:8080/sessions/SESSION_UUID/overrides/bulk -H "Authorization: Bearer LECTURER_OR_ADMIN_TOKEN" -H "Content-Type: application/json" --data "{\"student_ids\":[\"STUDENT_RECORD_UUID\",\"ANOTHER_STUDENT_RECORD_UUID\"],\"status\":\"present\",\"reason_code\":\"power_outage\",\"note\":\"Scanner unavailable during lecture\"}"
curl.exe -i "http://localhost:8080/sessions/SESSION_UUID/report?format=csv&limit=200&offset=0" -H "Authorization: Bearer LECTURER_OR_ADMIN_TOKEN"
```

The report and roster routes enforce the same resource-derived scope rules;
department administrators see their department's students, while authorized
offering staff/coordinators can see the full offering roster.

## Security and input checks

- Call a protected route without a bearer token; expect an error envelope with
  `authentication_required`.
- Call a device route without `X-Device-Key`; expect `device_auth_required`.
- Send unknown JSON fields or malformed JSON; expect `400 invalid_json`.
- Send a JSON body over 1 MiB; expect a `400` error envelope.
- Repeatedly call login or device endpoints; expect `429 rate_limited`.
- Ordinary authenticated reads such as `/me`, `/me/courses`, and
  `/me/timetable` are not subject to a shared request quota. Repeatedly submit
  `/auth/verify-email` attempts from one client IP; after five requests in one
  minute, expect `429 rate_limited` on that verification endpoint only.
- Check that an unassigned lecturer cannot start/end a session belonging to
  another offering.
- Verify refresh-token rotation: reuse the old refresh cookie after a
  successful refresh and expect `401 invalid_refresh_token`.

## Staff and administrator workflows

### Staff CSV import

As a scoped administrator, prepare the exact header and rows below. Preview is
read-only; commit revalidates every row and creates all records and invitation
messages atomically.

```cmd
(echo staff_no,email,display_name,department_id& echo STAFF-001,lecturer@example.test,Alex Lecturer,DEPARTMENT_UUID)>staff.csv
curl.exe -i -X POST http://localhost:8080/imports/staff/preview -H "Authorization: ******" -H "Content-Type: text/csv" --data-binary "@staff.csv"
curl.exe -i -X POST http://localhost:8080/imports/staff -H "Authorization: ******" -H "Content-Type: text/csv" --data-binary "@staff.csv"
```

Invalid rows return `422` with their line/column errors and commit no data.
Files over 5 MiB return `413`. A successful commit returns `201`; the invited
lecturer receives an invitation link and can accept it through the public
`POST /invitations/accept` flow documented earlier.

### Signup request review

An unknown matric claim must include the chosen department UUID. As an admin in
that department or its parent faculty, list requests and approve with the
student record details or reject without creating a record:

```cmd
curl.exe -i "http://localhost:8080/signup-requests?limit=50&offset=0" -H "Authorization: ******"
curl.exe -i -X POST http://localhost:8080/signup-requests/SIGNUP_REQUEST_UUID/review -H "Authorization: ******" -H "Content-Type: application/json" --data "{\"status\":\"approved\",\"department_id\":\"DEPARTMENT_UUID\",\"programme_id\":\"PROGRAMME_UUID\",\"level\":200,\"display_name\":\"Alex Student\"}"
curl.exe -i -X POST http://localhost:8080/signup-requests/SIGNUP_REQUEST_UUID/review -H "Authorization: ******" -H "Content-Type: application/json" --data "{\"status\":\"rejected\"}"
```

Approval requires a department, a programme in that department, level, and
display name. The new student record and review audit entry are transactional;
the student can then complete the existing email claim flow.

### Password reset

The forgot endpoint intentionally returns the same acknowledgement for known
and unknown emails. In development, find the one-time code in the `development
email` API log. Codes expire after 15 minutes and stop accepting attempts after
five incorrect submissions:

```cmd
curl.exe -i -X POST http://localhost:8080/auth/password/forgot -H "Content-Type: application/json" --data "{\"institution_id\":\"INSTITUTION_UUID\",\"email\":\"USER_EMAIL\"}"
curl.exe -i -X POST http://localhost:8080/auth/password/reset -H "Content-Type: application/json" --data "{\"institution_id\":\"INSTITUTION_UUID\",\"email\":\"USER_EMAIL\",\"code\":\"123456\",\"password\":\"a-long-new-password\"}"
```

On success, prior refresh tokens are revoked and the forced-password-change
flag is cleared.

### Venue, academic, and role administration

Venue creation and edits are limited to a matching institution/faculty/
department scope; department/faculty administrators should set `department_id`.
Deactivation preserves historical sessions. Academic sessions and semesters
are managed through their catalogue routes; changing `current` clears the
previous current academic session in the same transaction. Institution admins
assign faculty admins; institution or faculty admins assign department admins
within their authorized scope:

```cmd
curl.exe -i -X POST http://localhost:8080/venues -H "Authorization: ******" -H "Content-Type: application/json" --data "{\"name\":\"LT 2\",\"latitude\":6.5244,\"longitude\":3.3792,\"radius_meters\":150,\"department_id\":\"DEPARTMENT_UUID\"}"
curl.exe -i -X PATCH http://localhost:8080/venues/VENUE_UUID -H "Authorization: ******" -H "Content-Type: application/json" --data "{\"active\":false}"
curl.exe -i -X PATCH http://localhost:8080/academic-sessions/SESSION_UUID -H "Authorization: ******" -H "Content-Type: application/json" --data "{\"current\":true}"
curl.exe -i -X POST http://localhost:8080/role-assignments -H "Authorization: ******" -H "Content-Type: application/json" --data "{\"user_id\":\"USER_UUID\",\"role\":\"department_admin\",\"scope_type\":\"department\",\"department_id\":\"DEPARTMENT_UUID\"}"
```

All these mutations write audit rows atomically.

### Email outbox

The API queues invitations, verification codes, and password reset codes in
PostgreSQL. Its worker polls every 10 seconds; failed delivery retries with
backoff and marks a message failed after five attempts. `APP_ENV=development`
uses the log-only mailer, which prints invitation links and one-time codes to
the API log. Non-development environments require `MAIL_API_KEY` and
`MAIL_FROM`.

Device key provisioning/rotation, department/offering percentage reports,
below-threshold lists, disputes, and remaining hardening are still incomplete.
These database-backed workflows have unit coverage but have not been
live-verified in this environment.

After those routes exist, also test expired tokens, oversized/malformed CSV,
parallel check-in bursts, and migration rollback as described in
[TESTING_GAPS.md](./TESTING_GAPS.md).
