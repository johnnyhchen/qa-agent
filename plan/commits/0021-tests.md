# 0021 - Tests (unit + fake runners + integration smoke)

Commit message:
- `test: add unit and integration tests for core loop components`

Goal:
- Make the architecture safe to refactor by pinning down critical invariants.

Scope:
- Unit tests for persistence and queue correctness.
- Fake runner infrastructure.
- End-to-end integration smoke (no external LLMs required).

Planned changes:
- Unit tests for blackboard migrations and CRUD.
- Unit tests for task queue atomic claim.
- Unit tests for stability wrapper retry behavior.
- Add a tiny fake CLI runner that emits deterministic results and evidence files.
- Integration test: run orchestrator with deterministic planner and judge stubs and fake runners, then assert verdict and report artifacts exist.

Verification:
- `go test ./...` runs in CI without needing Docker or external CLIs.

Exit criteria:
- Core correctness properties are covered without making the test suite fragile.
