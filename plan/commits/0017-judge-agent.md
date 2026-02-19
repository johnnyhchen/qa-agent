# 0017 - Judge Agent (Adversarial Protocol + Verdict/Findings Contract)

Commit message:
- `feat: add judge agent with adversarial protocol and evidence-only verdict`

Goal:
- Generate counterexample tasks, enforce evidence sufficiency, and produce a final `Verdict` + `Finding`s.

Scope:
- Judge prompt/instructions.
- Adversarial protocol (validator vs breaker vs judge roles).
- Structured output schema.

Planned changes:
- Add `internal/agents/judge/` defining judge instructions and output schema.
- Encode the adversarial protocol in the judge wrapper (bounded rounds) rather than leaving it implicit.
- Enforce evidence sufficiency policy.
- `pass` requires proof evidence for every acceptance criterion and no pending `P0` or `P1` tasks.
- `fail` requires at least one stable, replayable counterexample run per failed criterion.
- `cannot_verify` when blocked, missing prerequisites, ambiguous spec, or irreducible flakes within budget.
- Judge can output either `next_tasks` or `verdict` (mutually exclusive) to keep orchestration deterministic.

Verification:
- Unit tests: judge output schema enforcement and mutual exclusivity (`next_tasks` xor `verdict`).
- Offline test: run judge against a fixed evidence set in the blackboard and confirm it produces a valid structured output.

Exit criteria:
- The system can converge to a bounded verdict instead of endlessly proposing work.
