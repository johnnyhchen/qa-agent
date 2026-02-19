# 0006 - Tracing + ActionTrace (Structured Logs, Runner I/O Capture)

Commit message:
- `feat: add tracing and action trace persistence`

Goal:
- Make runs auditable and debuggable with a replayable record of decisions and executions.

Scope:
- Structured logging.
- Action trace schema and persistence.
- Subprocess stdout/stderr capture for runners.

Planned changes:
- Standardize logging on `log/slog` (JSON output option).
- Add `ActionTrace` model and storage strategy.
- Orchestrator trace: every scheduling decision and tool invocation.
- Runner trace: runner command line, stdin JSON, stdout JSON, stderr, exit code, wall times.
- Store traces as artifacts on disk and reference them from SQLite (avoid huge blobs in DB).
- Add a `trace_id`/`action_trace_ref` field on `Run`.

Verification:
- Unit test: trace files are written and referenced correctly.
- Manual smoke: run a stub command and confirm trace captures inputs/outputs.

Exit criteria:
- Failures can be debugged without rerunning the whole validation.
