# Architecture

`cmd/api` is the composition root. It loads environment configuration,
constructs a PostgreSQL pool, configures structured JSON logging, and starts a
chi HTTP server with bounded timeouts and graceful shutdown.

Packages under `internal` own their domain and should expose small service
interfaces. Handlers are transport adapters; PostgreSQL access is through
pgx/sqlc, and pure rules must not depend on HTTP or the database. Server-side
time is supplied to business services through an injected clock.

The API uses a uniform JSON error envelope, strict one-value JSON decoding,
request IDs, and an explicit CORS origin. Liveness is independent of database
availability; readiness includes a PostgreSQL ping.

The frontend contract is `api/openapi.yaml`. Device-specific requests must use
separate device-key authentication and be documented in `docs/DEVICE_API.md`.
