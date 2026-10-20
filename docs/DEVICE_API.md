# Scanner device API

All device routes use device authentication, separate from user JWT
authentication. Send `X-Device-Key` with the provisioned high-entropy key over
HTTPS. The database stores only its SHA-256 hash; the plaintext key is returned
only when a device is provisioned or its key is rotated. Key management routes
are not yet implemented; provision keys through the trusted administrative
process until that phase is delivered.

The server is the source of time. Do not require an RTC or use a device clock to
decide whether a session is open. Sync NTP at boot for TLS certificate
validation, then use `server_time` returned by the API for server-relative
timing. For a scan queued while offline, preserve its `client_scanned_at`
timestamp in flash and submit it when reconnecting. The server uses that
timestamp to associate the scan with the session active at that venue when the
scan occurred; it does not use the timestamp to decide whether the session is
open now or to calculate attendance lateness.

## `GET /device/tasks`

Fetches the open session at this device's venue and the oldest pending
enrolment task, if any.

**Headers**

- `X-Device-Key: <device-key>` (required)
- `If-None-Match: "<etag>"` (optional; send the ETag from the last `200`)

**200 response**

```json
{
  "active_session": {
    "id": "24f1401e-5907-4223-910c-4a2e078caa66",
    "venue_id": "48c5efb3-9651-478d-9d70-bb5be469f62f"
  },
  "pending_enrolment": null,
  "server_time": "2026-10-20T10:27:08Z"
}
```

Either task may be `null`. The server includes an `ETag` header derived from
the task state, not `server_time`; when the state is unchanged,
`If-None-Match` receives `304 Not Modified` with no response body. Polling
clients should retain the previous task state on a 304. Poll every few seconds
while waiting, and back off after network failures (for example 2, 4, 8, then
30 seconds).

Windows CMD:

```cmd
curl.exe -i "https://attendance.example.edu/device/tasks" -H "X-Device-Key: YOUR_DEVICE_KEY"
curl.exe -i "https://attendance.example.edu/device/tasks" -H "X-Device-Key: YOUR_DEVICE_KEY" -H "If-None-Match: \"ETAG_FROM_PREVIOUS_RESPONSE\""
```

## `POST /device/scans`

The server resolves `slot_id` against the authenticated device, finds the
session open at that device's venue, and inserts one fingerprint scan per
student/session. A repeated scan is successful and marked `duplicate: true`.

```json
{
  "slot_id": "550e8400-e29b-41d4-a716-446655440000",
  "confidence": 0.98,
  "client_scanned_at": "2026-10-20T10:26:52Z"
}
```

`client_scanned_at` is optional for live scans. Include the original timestamp
for late-synced scans so they can be attached to the correct historical
session. Do not submit biometric data or templates; the API accepts only the
registered slot identifier and match confidence.

**New scan (`201`)**

```json
{
  "session_id": "24f1401e-5907-4223-910c-4a2e078caa66",
  "student_id": "0bfe0a6f-8d59-4a4b-89bc-118b011b3930",
  "duplicate": false
}
```

**Duplicate scan (`200`)** has the same shape with `duplicate: true`.

**No open session (`409`)**

```json
{
  "error": {
    "code": "no_active_session",
    "message": "There is no open session at this device venue",
    "details": {}
  }
}
```

Queue scans in non-volatile flash so they survive power loss and reboot. Keep
each scan's original `client_scanned_at`, then flush queued scans through
`/device/scans/batch` after reconnecting. Retry network failures with
exponential backoff; the session/student uniqueness constraint makes
resubmission idempotent. Do not repeatedly retry `invalid_slot` or
`invalid_confidence`; those require correcting the queued item.

Windows CMD:

```cmd
curl.exe -i -X POST "https://attendance.example.edu/device/scans" -H "X-Device-Key: YOUR_DEVICE_KEY" -H "Content-Type: application/json" --data "{\"slot_id\":\"550e8400-e29b-41d4-a716-446655440000\",\"confidence\":0.98}"
```

## `POST /device/scans/batch`

Flush 1–100 queued scans using the same per-item fields as `/device/scans`.
Every item is processed independently; a failed item does not discard
successful items. `index` refers to its position in the request array. Inspect
each result and retry only items that did not record successfully.

```json
{
  "scans": [
    {
      "slot_id": "550e8400-e29b-41d4-a716-446655440000",
      "confidence": 0.98
    },
    {
      "slot_id": "02e65b19-b67b-46f8-8ce9-dde4abff455e",
      "confidence": 0.94
    }
  ]
}
```

The `200` response contains one result for each request item:

```json
{
  "results": [
    {
      "index": 0,
      "result": {
        "session_id": "24f1401e-5907-4223-910c-4a2e078caa66",
        "student_id": "0bfe0a6f-8d59-4a4b-89bc-118b011b3930",
        "duplicate": false
      }
    },
    {
      "index": 1,
      "result": {
        "error_code": "no_active_session"
      }
    }
  ]
}
```

Windows CMD:

```cmd
curl.exe -i -X POST "https://attendance.example.edu/device/scans/batch" -H "X-Device-Key: YOUR_DEVICE_KEY" -H "Content-Type: application/json" --data "{\"scans\":[{\"slot_id\":\"550e8400-e29b-41d4-a716-446655440000\",\"confidence\":0.98},{\"slot_id\":\"02e65b19-b67b-46f8-8ce9-dde4abff455e\",\"confidence\":0.94}]}"
```

## `POST /device/enrolments/{id}/complete`

Report the result of the pending fingerprint enrolment task:

```json
{"status":"done"}
```

Allowed statuses are `done` and `failed`. `204 No Content` confirms the task
was updated. `404 enrolment_not_found` means it is no longer pending or does
not belong to this device; fetch tasks again rather than retrying indefinitely.

Windows CMD:

```cmd
curl.exe -i -X POST "https://attendance.example.edu/device/enrolments/d4f5b2a6-798e-4b2c-bf03-3941827d1ce8/complete" -H "X-Device-Key: YOUR_DEVICE_KEY" -H "Content-Type: application/json" --data "{\"status\":\"done\"}"
```

## Status and retry summary

| Status | Meaning | Device action |
|---|---|---|
| `200` | Duplicate scan recorded previously, or valid task response | Treat duplicate as delivered |
| `201` | New scan recorded | Remove the item from the offline queue |
| `204` | Enrolment completion accepted | Clear the completed task |
| `304` | Task state unchanged | Keep current state; poll later |
| `400` | Malformed request, invalid slot, or confidence | Do not retry unchanged |
| `401` | Missing, invalid, or revoked key | Stop requests and request trusted reprovisioning |
| `409` | No active session for this venue | Keep queued scan and retry after the next tasks poll |
| `429` | Rate limited | Delay and retry with exponential backoff |
| `5xx` | Temporary server failure | Retry with exponential backoff |

Batch `200` results also contain per-item `error_code` values. Only `invalid_slot`,
`invalid_confidence`, and `no_active_session` are currently returned as
expected scan errors.

## HTTPS, TLS, and NTP

Production requests must use HTTPS. Firmware must validate the server
certificate chain and hostname using the current trusted root CA bundle; never
disable certificate validation or pin an unverified leaf certificate. The
specific issuing CA depends on the deployment host and its configured TLS
certificate, so obtain the deployment's documented root/intermediate chain
from the operator before provisioning devices. Sync NTP at boot so certificate
validity checks work, but use API `server_time` rather than the local clock for
attendance/session decisions.

For bash, use the same curl commands with single-quoted JSON bodies, for
example:

```bash
curl -i -X POST 'https://attendance.example.edu/device/scans' \
  -H 'X-Device-Key: YOUR_DEVICE_KEY' \
  -H 'Content-Type: application/json' \
  --data '{"slot_id":"550e8400-e29b-41d4-a716-446655440000","confidence":0.98}'
```
