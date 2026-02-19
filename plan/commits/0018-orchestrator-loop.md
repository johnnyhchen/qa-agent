# 0018 - Orchestrator Loop (Non-linear scheduling + stop rules)

Commit message:
- `feat: implement deterministic orchestrator loop`

Goal:
- Implement the core non-linear scheduler over the blackboard: plan, execute, judge, repeat within budgets.

Scope:
- Deterministic scheduling.
- Budget enforcement.
- Barrier semantics (judge does not evaluate in-flight work).

Planned changes:
- Add `internal/orchestrator/` with a loop that creates a `ValidationRun`, runs the planner, drains an execution batch, runs the judge, then either finalizes a verdict or enqueues more tasks and continues.
- Enforce saturation and loop budgets.
- Budget: `MAX_QUEUED_TASKS`.
- Budget: `MAX_NEW_TASKS_PER_JUDGE_TURN`.
- Budget: max judge turns.
- Budget: max wall time per run.
- Budget: max retries per task (delegated to stability wrapper).
- Ensure `P1` breaker tasks are executed (or exhausted) before allowing `pass`.

Verification:
- Integration test using fake runners and deterministic planner/judge stubs.
- Confirm stop rules lead to `pass`, `fail`, and `cannot_verify` outcomes deterministically given fixed artifacts.

Exit criteria:
- End-to-end loop can run locally using dummy runners without external dependencies.
