# 0003 - Core Types and Schemas (FeatureSpec, Task, Run, Evidence)

Commit message:
- `feat: add core domain types and validation`

Goal:
- Define the typed artifacts that everything else reads and writes.

Scope:
- Go structs for all load-bearing artifacts.
- Enum types and versioning strategy.
- Deterministic validation (no LLM in this commit).

Planned changes:
- Add `internal/model/` (or `pkg/model/`) with Go types: `FeatureSpec`, `AcceptanceCriterion`, `TestPlan`, `Task`, `Run`, `Evidence`, `ActionTrace`, `Finding`, `Verdict`.
- Add JSON tags and stable IDs (`run_id`, `task_id`, etc.).
- Add explicit validation helpers per type (e.g., `Validate() error`).
- Add schema version field(s) to allow forward-compatible migrations.

Verification:
- Unit tests for `Validate()` on representative valid/invalid payloads.
- `go test ./...` stays green.

Exit criteria:
- Later components can persist artifacts without inventing ad-hoc JSON.
