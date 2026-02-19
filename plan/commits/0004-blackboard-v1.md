# 0004 - Blackboard v1 (SQLite + Artifact Store)

Commit message:
- `feat: implement blackboard store (sqlite + filesystem artifacts)`

Goal:
- Make the blackboard a real system: schema, atomic writes, and an agent-safe query/list surface.

Scope:
- SQLite metadata DB.
- Artifact directory layout.
- CRUD/list for core entities.

Planned changes:
- Add `internal/blackboard/` with a single entrypoint like `Store`.
- Use a pure-Go SQLite driver (prefer `modernc.org/sqlite`) to avoid CGO friction.
- Add migrations for tables: `validation_runs`, `feature_specs`, `tasks`, `runs`, `evidence`, `verdicts`.
- Implement atomic state transitions where required (task claim/complete will be extended in the next commit).
- Add artifact store layout per run: `~/.qa-agent/runs/<run_id>/` containing `db.sqlite` and `artifacts/`.
- Implement list APIs with explicit filters (avoid free-form SQL from agents): `TaskList(filter)`, `RunList(filter)`, `EvidenceList(filter)`.
- Add retention knobs to the run record (keep-all, keep-key-artifacts, keep-summary-only); do not implement pruning yet.

Verification:
- Unit tests for migrations and basic CRUD.
- Concurrency smoke test: two goroutines writing evidence to different runs should not corrupt DB.

Exit criteria:
- Orchestrator, agents, and executors have a stable, deterministic persistence layer to build on.
