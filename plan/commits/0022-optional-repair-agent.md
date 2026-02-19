# 0022 - Optional Repair Agent (ai-codergen-sdk, gated)

Commit message:
- `feat: add optional gated repair agent using ai-codergen-sdk`

Goal:
- Provide an escape hatch when the local stack is blocked (build failures, missing fixtures, harness gaps), without making the main loop unsafe.

Scope:
- Optional component behind a feature flag.
- Scratch workspace patch proposals.
- Explicit approval gate before applying changes.

Planned changes:
- Add `internal/agents/repair/` implemented with `ai-codergen-sdk`.
- Repair agent inputs are strictly bounded: error diagnostics, logs, and minimal context.
- Repair agent outputs a patch proposal (diff) and a rationale.
- Orchestrator invokes repair only when the execution harness reports `blocked` with repairable diagnostics.
- Orchestrator never applies patches automatically; require an explicit `--apply-repair` opt-in.

Verification:
- Unit test: repair agent is never invoked unless the gate is enabled.
- Smoke test: run repair agent in dry-run mode and confirm it emits a patch proposal artifact.

Exit criteria:
- You can debug blocked runs faster without turning the system into an auto-modifying black box.
