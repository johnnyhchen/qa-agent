# 0015 - Agent Runtime (ai-agents-sdk wiring + tool registry)

Commit message:
- `feat: wire ai-agents-sdk runner and tool registry`

Goal:
- Establish the Go agent runtime used by the planner and judge, with strict tool scoping and structured outputs.

Scope:
- `ai-agents-sdk` integration.
- Tool registry (blackboard, queue, sandbox, runner invocation via execution harness).
- Usage/cost tracking plumbing.

Planned changes:
- Add `internal/agents/` that wraps `ai-agents-sdk` initialization and configuration.
- Add a tool registry implementation that exposes only domain tools (no raw shell access).
- Add provider/model selection config (planner model, judge model, token and time and cost caps per run).
- Persist agent inputs/outputs and usage to the blackboard as trace artifacts.

Verification:
- Unit test: tool allowlist enforcement.
- Smoke test: run a trivial agent that writes a known artifact to the blackboard.

Exit criteria:
- Planner/judge commits only need to supply prompts and output schemas; runtime is shared.
