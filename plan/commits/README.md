# Commit-By-Commit Implementation Plan (qa-agent)

This directory breaks the design into small, reviewable commits.

Constraints (carried through every commit):
- Everything except surface automation tools is Go.
- `ai-browser-use` and `ai-computer-use` are invoked as external CLIs with a strict JSON in/out contract.
- Local-only execution (no real staging).
- Evidence-only judgment, budgeted loops, and deterministic control plane.

## Sequence

1. [0001 - Repo Scaffold (Go Module, Lint, CI Skeleton)](0001-repo-scaffold.md)
2. [0002 - CLI and Config (run, replay, report)](0002-cli-and-config.md)
3. [0003 - Core Types and Schemas (FeatureSpec, Task, Run, Evidence)](0003-core-types-and-schemas.md)
4. [0004 - Blackboard v1 (SQLite + Artifact Store)](0004-blackboard-v1.md)
5. [0005 - Task Queue (Atomic Claim, Priorities, Saturation)](0005-task-queue.md)
6. [0006 - Tracing + ActionTrace (Structured Logs, Runner I/O Capture)](0006-tracing-actiontrace.md)
7. [0007 - Sandbox Interface + Local Stack Provider (Docker)](0007-sandbox-interface.md)
8. [0008 - Runner Interfaces + Subprocess Contract (Generic CLI Runner)](0008-runner-contracts.md)
9. [0009 - Web Runner Adapter (ai-browser-use)](0009-web-runner-ai-browser-use.md)
10. [0010 - macOS Runner Adapter (ai-computer-use)](0010-macos-runner-ai-computer-use.md)
11. [0011 - API Runner (HTTP + gRPC Transcript Evidence)](0011-api-runner.md)
12. [0012 - iOS Runner Stub (Simulator Hook Points, Experimental)](0012-ios-runner-stub.md)
13. [0013 - Stability Wrapper (Budgets, Flake Classification, Quarantine)](0013-stability-wrapper.md)
14. [0014 - Evidence Processor (Normalization + Summaries)](0014-evidence-processor.md)
15. [0015 - Agent Runtime (ai-agents-sdk wiring + tool registry)](0015-agent-runtime-ai-agents-sdk.md)
16. [0016 - Planner Agent (FeatureSpec/TestPlan/Tasks Output Contract)](0016-planner-agent.md)
17. [0017 - Judge Agent (Adversarial Protocol + Verdict/Findings Contract)](0017-judge-agent.md)
18. [0018 - Orchestrator Loop (Non-linear scheduling + stop rules)](0018-orchestrator-loop.md)
19. [0019 - Reporting + Bundling (Evidence bundle, verdict summary)](0019-reporting-bundling.md)
20. [0020 - Replay + Audit (best-effort re-run, trace inspection)](0020-replay-audit.md)
21. [0021 - Tests (unit + fake runners + integration smoke)](0021-tests.md)
22. [0022 - Optional Repair Agent (ai-codergen-sdk, gated)](0022-optional-repair-agent.md)
23. [0023 - Docs + UX Polish (README, examples, troubleshooting)](0023-docs-ux.md)
