# 0005 - Task Queue (Atomic Claim, Priorities, Saturation)

Commit message:
- `feat: add task queue operations with atomic claim`

Goal:
- Prevent combinatorial explosion and make execution scheduling deterministic.

Scope:
- Queue operations over the blackboard DB.
- Priority classes and saturation policy.
- Dedupe semantics.

Planned changes:
- Add `internal/queue/` (or keep inside blackboard) implementing `EnqueueTask`, `ClaimTask`, and `CompleteTask`.
- `EnqueueTask`: enforce `dedupe_key` uniqueness per `run_id`.
- `ClaimTask`: atomic compare-and-swap transition with a lease/owner (or `claimed_by`) and timestamp.
- `CompleteTask`: record final status and increment attempt counters.
- Encode priority classes `P0..P3`.
- Never drop `P0` or `P1` tasks.
- Saturation policy when `MAX_QUEUED_TASKS` reached (drop/defer lowest priority first).
- `MAX_NEW_TASKS_PER_JUDGE_TURN` is enforced at orchestrator layer later, but queue should expose helpers to count tasks created by an actor in a time window.

Verification:
- Unit tests: dedupe works, claim is atomic under contention, priority ordering is stable.
- Property test (optional): claiming the same task concurrently never yields two owners.

Exit criteria:
- Execution harness can reliably drain work while the system stays bounded.
