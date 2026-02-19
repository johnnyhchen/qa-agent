# 0016 - Planner Agent (FeatureSpec/TestPlan/Tasks Output Contract)

Commit message:
- `feat: add planner agent for requirements + planning`

Goal:
- Convert the user’s feature description into a concrete `FeatureSpec`, `TestPlan`, and initial prioritized tasks.

Scope:
- Planner prompt/instructions.
- Structured output schema.
- Write artifacts into the blackboard.

Planned changes:
- Add `internal/agents/planner/` defining the planner instructions, the output schema, and a wrapper that writes `FeatureSpec`, `TestPlan`, and initial `Task`s.
- Planner must emit `open_questions` instead of guessing. Orchestrator behavior for unanswered questions is implemented later.
- Planner outputs must include per-surface task priority (`P0..P3`) and a `dedupe_key` per task.

Verification:
- Unit tests on schema validation (planner output rejected if malformed).
- Golden tests: given a fixed input description, ensure the planner output is shape-correct (content will vary by model).

Exit criteria:
- The system can bootstrap work from only a general text description.
