# 0020 - Replay + Audit (best-effort re-run, trace inspection)

Commit message:
- `feat: add replay and trace inspection commands`

Goal:
- Make results debuggable and (best-effort) repeatable using recorded action traces.

Scope:
- Trace inspection.
- Best-effort task re-run.
- Strictly local behavior.

Planned changes:
- Implement `qa-agent trace --run-id ...` to view orchestrator and runner traces.
- Implement `qa-agent replay --run-id ... --task-id ...` as best-effort.
- Replay provisions a fresh sandbox, re-runs the task using the same runner, captures new evidence, and stores it as a new `Run` linked to the original.
- Ensure replay never mutates original evidence; it appends new runs/evidence.

Verification:
- Integration test using fake runner and deterministic traces.

Exit criteria:
- A reviewer can understand what happened and attempt a repro without reading raw logs manually.
