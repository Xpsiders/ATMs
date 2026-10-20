# Repository agent instructions

- Read `docs/HANDOFF.md`, `docs/PROGRESS.md`, and `api/openapi.yaml` before
  starting a task.
- Work on exactly one task: the first `partial` or `not started` item in
  `docs/PROGRESS.md`, unless the user explicitly names another task.
- Do not refactor working code without a requirement.
- Update `api/openapi.yaml` before adding or changing a route.
- Add deterministic unit tests for new behavior. Do not require Docker or a live
  database for unit tests.
- After changes, run and report the actual output of `gofmt -l .`,
  `go vet ./...`, `go build ./...`, and `go test ./...`.
- Report environment-blocked checks plainly. Never claim a command ran unless
  it ran.
- Update `docs/PROGRESS.md` when a task's status changes.
- Before finishing, report what changed, what was verified, what remains
  unverified, and the recommended next task.
- Do not push changes unless the user explicitly asks.
