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

   In PowerShell without GNU Make, use `go run ./scripts/seed`. The command
   prints demo account credentials, the institution and sample resource IDs,
   and prints the scanner's plaintext device key only when it creates that
   device for the first time. Save that key securely; a repeat seed does not
   reveal or rotate an existing key.

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
   `DemoAdmin-2026!`; other seeded roles are printed by the seed command.
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

4. **Student claim and email verification.** A matching matric number and
   record email starts verification. In development, read the one-time code
   from the API log entry named `development email`. The response is deliberately
   identical for known and unknown matric numbers:

   ```cmd
   curl.exe -i -X POST http://localhost:8080/auth/signup-claim -H "Content-Type: application/json" --data "{\"institution_id\":\"INSTITUTION_UUID\",\"matric_no\":\"MATRIC_NO\",\"email\":\"EMAIL_ON_STUDENT_RECORD\"}"
   curl.exe -i -X POST http://localhost:8080/auth/verify-email -H "Content-Type: application/json" --data "{\"institution_id\":\"INSTITUTION_UUID\",\"matric_no\":\"MATRIC_NO\",\"code\":\"123456\",\"password\":\"a-long-new-password\"}"
   ```

   The first call returns `202 request_received`. The second returns `201`, an
   access token, and the refresh cookie. The student department and level come
   from the student record, not request fields. An unknown matric number creates
   a department review request; admin review routes are not yet available.

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
   a session. Omit coordinates/radius to copy them from the venue:

   ```cmd
   curl.exe -i -X POST http://localhost:8080/sessions -H "Authorization: Bearer ACCESS_TOKEN" -H "Content-Type: application/json" --data "{\"offering_id\":\"OFFERING_UUID\",\"venue_id\":\"VENUE_UUID\",\"rotating_code\":true,\"late_after_min\":10}"
   ```

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
   `accuracy_poor`, and `distance_meters`. Try a wrong/expired code
   (`wrong_or_expired_code`), an outside point (`outside_radius`), an
   unregistered student under `registered_only` (`not_on_roster`), an ended
   session (`session_closed`), and repeating a successful check-in
   (`already_checked_in`, HTTP `409`).

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
collection accepts `limit` and `offset`; create/update bodies use the relevant
resource fields. For example, to search and register:

```cmd
curl.exe -i "http://localhost:8080/offerings/search?q=ece301&limit=20&offset=0" -H "Authorization: Bearer ACCESS_TOKEN"
curl.exe -i "http://localhost:8080/offerings/search?q=Cicuits&limit=20&offset=0" -H "Authorization: Bearer ACCESS_TOKEN"
curl.exe -i -X POST http://localhost:8080/offerings/OFFERING_UUID/registrations -H "Authorization: Bearer STUDENT_ACCESS_TOKEN"
curl.exe -i "http://localhost:8080/offerings/OFFERING_UUID/registrations?status=pending&limit=50&offset=0" -H "Authorization: Bearer ADMIN_ACCESS_TOKEN"
curl.exe -i -X PATCH http://localhost:8080/offerings/OFFERING_UUID/registrations/STUDENT_RECORD_UUID -H "Authorization: Bearer ADMIN_ACCESS_TOKEN" -H "Content-Type: application/json" --data "{\"status\":\"approved\"}"
```

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
is ordered and paginated, and can be downloaded as JSON or CSV.

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
- Check that an unassigned lecturer cannot start/end a session belonging to
  another offering.
- Verify refresh-token rotation: reuse the old refresh cookie after a
  successful refresh and expect `401 invalid_refresh_token`.

## Not yet available for manual verification

Device key provisioning/rotation, department/offering percentage reports,
below-threshold lists, disputes, and outbox processing remain incomplete.
Import, seed, and catalogue flows require a migrated PostgreSQL database and
have not been live-verified in this environment.

After those routes exist, also test expired tokens, oversized/malformed CSV,
parallel check-in bursts, and migration rollback as described in
[TESTING_GAPS.md](./TESTING_GAPS.md).
