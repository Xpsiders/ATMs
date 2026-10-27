# Decisions

This file records implementation decisions that clarify the engineering
specification. Later phases must add a dated entry when a material ambiguity is
resolved.

## Phase 0

- The HTTP server refuses to start without `DATABASE_URL` and
  `ALLOWED_ORIGIN`; silently using permissive security defaults would make a
  deployment appear healthy while misconfigured.
- `/healthz` is a process liveness check and does not contact PostgreSQL.
  `/readyz` pings the database with a bounded timeout.
- Routes are added only with their service implementation and tests; unfinished
  product groups are intentionally omitted rather than exposed as
  success-shaped placeholders.
- Attendance percentages count `present` and, when configured, `late` as
  attended; `excused` remains in sessions held but does not count as attended.
  The below-threshold comparison is strict (`percentage < minimum`), so an
  exact threshold passes.
- A web check-in in a session with no scanner is classified as `verified`;
  without scanner evidence, it is the only available check-in method.
- At exactly `late_after_min`, a check-in is on time; it becomes late only
  after that instant, matching the domain wording "past late_after_min".

## Session API

- Session codes rotate every 60 seconds; static codes expire after five
  minutes. These defaults keep the API configuration small while allowing the
  firmware and client to use a predictable rotation interval.
- Session start, end, and code retrieval are currently limited to the assigned
  offering lecturers and coordinator. Department/institution administrator
  scope authorization will be added with the RBAC-backed organization APIs;
  trusting a caller-provided role would be unsafe.
- Starting a session snapshots the venue's coordinates and radius, or the
  lecturer's own fix when it is good enough, and the snapshot stays fixed for
  that session apart from `POST /sessions/{id}/location`. A venue with no
  coordinates snapshots none; see "Venue location and check-in geofence".

## Device API

- Device task ETags hash only task identity/state and intentionally exclude
  `server_time`; otherwise a polling device would never receive a 304.
- Offline scan `client_scanned_at` is used only to associate a late-synced scan
  with the session active at that venue at scan time. Attendance lateness and
  current session state remain server-controlled; devices must persist queued
  scan timestamps in flash to survive reboots.
- Device keys are currently provisioned outside the API because the device
  management endpoints are not yet implemented. The runtime accepts only
  hashes and never returns key plaintext.
- TLS CA trust is deployment-specific. Firmware must validate the configured
  host certificate against a trusted CA bundle; the deployment operator must
  provide the exact issuing chain before device provisioning rather than the
  repository assuming one cloud provider.

## Student claim

- The claim-start endpoint returns the same `request_received` response for a
  matching record and an unknown matric number. This avoids making the public
  endpoint a student-record enumeration oracle; matched records receive mail,
  while unknown records are queued for review.
- Claim verification consumes the matching, unexpired code and creates the
  user, binds the existing student record, and assigns its department-scoped
  student capability in one PostgreSQL transaction. Department and level are
  taken only from the locked student record.

## Current-user profile response

- Login and `GET /me` return profile and role fields at the top level. Role
  scope IDs are resolved from the stored role assignments; platform scope has
  a null `scope_id`, while institution, faculty, and department scopes use
  their corresponding resource ID. Student roles additionally include the
  student's locked home `department_id` and `level`.
- Keep `force_password_change` in the login token response for compatibility,
  and include the clearer `must_change_password` profile field in both login
  and `/me`. The `/me` endpoint remains accessible with a forced-change token
  so the client can determine the required next action.

## Staff invitations

- Staff invitations expire after seven days and use a random 32-byte token;
  only its SHA-256 hash is stored. The plaintext token is delivered to the
  invited email and is never returned to the inviter; development log delivery
  deliberately prints the invitation link for local testing.
- The acceptance request includes the institution ID so the token lookup and
  every related query remain tenant-scoped. The institution ID is also included
  in the invitation email.
- Institution admins may invite non-platform roles, faculty admins may invite
  department admins or lecturers within their faculty, and department admins
  may invite lecturers only within their department. Platform administrator
  invitations are excluded because platform scope is not an institution-owned
  capability.
- Acceptance proves control of the invited mailbox; the new account is marked
  email-verified. User creation, role assignment, invitation consumption, and
  the audit event are performed in one transaction. If an invitation matches
  a staff record, that record is linked to the newly created user in the same
  transaction.
- Staff CSV imports use the student import's five-megabyte limit and
  preview/revalidate/atomic-commit pattern. Each imported staff row creates a
  lecturer invitation scoped to its department, and the staff record,
  invitation, audit events, and queued email are committed together.
- Invitation URLs, student verification codes, and password reset codes may
  appear in the log-only mailer only when `APP_ENV=development`; non-development
  environments require the configured delivery provider.

## Password reset and email delivery

- Password reset requests return the same acknowledgement whether or not an
  enabled account exists. Reset codes are six digits, HMAC-hashed with the
  server secret, expire after 15 minutes, and permit at most five failed
  attempts. A successful reset consumes the code, clears forced password
  change, revokes refresh tokens, and writes an audit event transactionally.
- Email delivery is queued in `email_outbox`; the worker claims rows with
  leases, sends via the configured `Mailer`, retries with bounded exponential
  backoff, and marks a message failed after five attempts.

## Signup review and administration

- Unknown-matric signup requests carry the selected department so list and
  review operations can be filtered to the request's department scope. Approval
  requires the same department plus a programme, level, and display name;
  creating the student record, changing request status, and writing the audit
  row are one transaction.
- Institution administrators assign faculty or department administrator
  roles. Faculty administrators can assign department administrators only
  inside their faculty; department administrators cannot grant administrator
  roles.
- Venue management is limited to the venue's department scope, except
  institution administrators can manage institution-wide venues. Deactivation
  is soft so existing sessions retain their snapshots and history. Academic
  session/semester mutations are institution-scoped and audited; setting the
  current academic session clears the prior one in the same transaction.

## Catalogue access

- Catalogue detail reads return the same `404` for an out-of-scope resource as
  for a missing resource, avoiding a cross-department existence oracle.
  Collection reads filter rows in SQL before ordering and pagination, so
  caller-supplied department filters never grant access.
- Course and offering access is resolved through the owning course department.
  Department/faculty/institution/platform administrators see only resources
  covered by their assignment; lecturers and coordinators can read offerings
  they are assigned to. A student's offering visibility is limited to their
  department and level, or an explicit matching general-course auto-target.
- A department or faculty administrator may discover a general offering when
  it has registrations or auto-registration targets in their scope. This
  catalogue visibility does not grant full-roster access: report and
  registration services continue to filter general-course students by home
  department.
- Updating a resource's owning parent requires authorization for both its
  current resource scope and the proposed destination scope; otherwise a
  caller could move an owned course or offering into another department.

## Venue location and check-in geofence

- **Location is evidence, never a gate.** Nothing refuses a check-in on
  position any more: `outside_radius` is gone. Students were being denied
  attendance for a hardware limit they cannot fix, and a cheap or indoor-bound
  handset is not evidence of a proxy. A fix is recorded, compared, and where it
  cannot be explained, flagged for the lecturer.
- **Device binding replaces the geofence as the anti-proxy control.** One
  device may record at most one student per session; a second student from the
  same device is refused with `device_already_used` (409). `device_hash` is
  therefore required on `POST /checkins`, where it used to be optional, because
  an absent one can no longer be treated as an opt-out.
- **A venue's coordinates are optional and come from the lecturer's device.** A
  venue is a named room first; its position is metadata it may not have. An
  administrator never visits the room, so the only reliable source is the
  lecturer standing in it. `POST /sessions` accepts `latitude`, `longitude`,
  and `accuracy_meters` from the browser Geolocation API: a fix of 100 m or
  better centres the session and is written back to the venue, so the next
  class there starts from a good anchor. The response reports
  `location_source` as `device`, `venue`, or `none`. Starting a class never
  depends on it — permission denied or a timeout simply opens the session with
  whatever the venue has, possibly nothing.
- **A fix vaguer than 100 m is discarded rather than used.** Centring a class
  on a 500-metre reading would strand the students inside the room, so the
  session falls back to the venue's stored centre.
- **The geofence does not follow the lecturer.** `POST /sessions/{id}/location`
  takes a mid-session fix (the app posts one about every five minutes). A fix
  inside the session's own radius refines the centre and recalibrates the
  venue; a fix beyond it is reported as `rejected_distance` and changes
  nothing. A reading outside the radius is either a lecturer who stepped out or
  a venue whose stored centre is wrong, and nothing in the reading tells the
  two apart — moving the centre on that guess would push every student still in
  the room outside it and pull whoever is near the lecturer in.
- **A venue with no centre is the one case a fix may establish one.** There is
  no circle to push anyone out of and nothing to contradict, so the first
  trusted fix sets it (`established`) and the room is calibrated for every
  class that follows. Without this, a venue whose lecturer denied location
  permission at start could never acquire a centre at all, which would defeat
  the point of capturing it on their device.
- `POST /venues/{id}/location` remains for an explicit "recalibrate this room"
  action and is audited as `venue.location_captured`, the same action the
  session paths write, so a venue's location history is one stream. It is open
  to administrators over the venue's department **and** to a lecturer who
  teaches at the venue, since they are the only person who is ever in it.
- `POST /venues` needs only a name. `radius_meters` defaults to 75 metres when
  a position is supplied and both routes cap it at 2000 metres, so one venue
  cannot cover a campus. A venue with no position stores NULL for all three
  columns rather than a zero radius, which the surviving
  `CHECK (radius_meters > 0)` would reject and which would also read as a real
  coordinate off the coast of Ghana.
- A radius is kept once set: a device fix says where a room is, not how big it
  is. Only a venue with no radius takes one derived from the captured accuracy.
- **`location_unverified` means the phone's own error cannot explain the gap.**
  The allowance is the reported accuracy, capped at 250 metres past the radius.
  Inside it, the fix is simply accepted and unflagged; beyond it, the check-in
  is still recorded but reported as `web_only_unverified` with reason
  `location_unverified` for the lecturer to confirm.
- Location trust is derived, not stored. The report recomputes it from the
  check-in's recorded `distance_meters` and `accuracy_meters` against the
  session's `radius_meters`, applying the same 250-metre cap as the service so
  the report and the check-in response cannot disagree. The cap is written in
  both Go and SQL; each carries a comment pointing at the other.
- A student whose fix is flagged still has two routes to a clean record: the
  room's fingerprint scanner, which verifies them outright because a biometric
  at the venue outranks any GPS reading, and a lecturer override after the
  session closes, which takes precedence over every other signal.

## Attendance is never classified as a proxy

- There is no `suspected_proxy` classification. A web check-in with no
  fingerprint, taken while a working scanner stood in the room, is reported as
  `web_only_unverified` with reason `no_fingerprint`. Nothing the server
  observes separates a stand-in from a queue at the sensor, a finger the sensor
  will not read, or a student who did not know to scan, so the report states
  what is missing and leaves the judgement to the lecturer, who can confirm or
  clear it with an override.
- The check-in behind such a row is not unguarded: it passed a rotating code
  that is valid for 30 seconds by default (`CHECKIN_CODE_WINDOW_SECONDS`) and
  the one-student-per-device binding that stops a single handset signing in a
  row of friends. The window is deliberately short, because forwarding the code
  to an absent friend is the remaining gap and its lifetime is what bounds it.
- The fingerprint scanner remains the only positive proof of presence, and an
  override still outranks every other signal.
