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
- Starting a session copies the venue coordinates and radius. A lecturer may
  explicitly override individual values at start, and the snapshot remains
  fixed for that session.

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
  invited email and is never returned to the inviter or written to logs.
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
  the audit event are performed in one transaction.

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
