# QA-Agent Commit Plan Progress

This tracks execution of `plan/commits/README.md` with mandatory QA gates before advancing phases.

## Overall

- [x] Phase 0001: Repo Scaffold
- [x] Phase 0002: CLI and Config
- [x] Phase 0003: Core Types and Schemas
- [x] Phase 0004: Blackboard v1
- [x] Phase 0005: Task Queue
- [x] Phase 0006: Tracing + ActionTrace
- [x] Phase 0007: Sandbox Interface
- [x] Phase 0008: Runner Contracts
- [x] Phase 0009: Web Runner Adapter
- [x] Phase 0010: macOS Runner Adapter
- [x] Phase 0011: API Runner
- [x] Phase 0012: iOS Runner Stub
- [x] Phase 0013: Stability Wrapper
- [x] Phase 0014: Evidence Processor
- [x] Phase 0015: Agent Runtime
- [x] Phase 0016: Planner Agent
- [x] Phase 0017: Judge Agent
- [x] Phase 0018: Orchestrator Loop
- [x] Phase 0019: Reporting + Bundling
- [x] Phase 0020: Replay + Audit
- [x] Phase 0021: Tests
- [x] Phase 0022: Optional Repair Agent
- [x] Phase 0023: Docs + UX Polish

## QA Log

### 0001
- Status: complete
- QA: `go test ./...` and `go run ./cmd/qa-agent --help` passed.

### 0002
- Status: complete
- QA: `go run ./cmd/qa-agent run --help` and `go test ./internal/config -run TestLoadWithEnv_Precedence` passed.

### 0003
- Status: complete
- QA: `go test ./internal/model -run Validate` and `go test ./...` passed.

### 0004
- Status: complete
- QA: `go test ./internal/blackboard` passed (migrations/CRUD + concurrent evidence smoke).

### 0005
- Status: complete
- QA: `go test ./internal/queue` passed (dedupe, atomic claim contention, priority ordering, saturation behavior).

### 0006
- Status: complete
- QA: `go test ./internal/trace -run TestCaptureSubprocess_WritesTraceAndEvidence` and `go test ./...` passed.

### 0007
- Status: complete
- QA: `go test ./internal/sandbox` passed (docker provider smoke + timeout behavior + snapshot/log artifacts).

### 0008
- Status: complete
- QA: `go test ./internal/runner` passed (fake CLI runner integration + subprocess trace/evidence capture).

### 0009
- Status: complete
- QA: `go test ./internal/runner/web` passed (doctor check + local fixture integration via fake `ai-browser-use`).

### 0010
- Status: complete
- QA: `go test ./internal/runner/macos` passed (doctor + smoke task + budget guardrails/stability hints).

### 0011
- Status: complete
- QA: `go test ./internal/runner/api` passed (HTTP + gRPC health transcript integration).

### 0012
- Status: complete
- QA: `go test ./internal/runner/ios` passed (blocked classification + diagnostics validation).

### 0013
- Status: complete
- QA: `go test ./internal/stability` passed (budget stop rules + flake/stable classification sequences).

### 0014
- Status: complete
- QA: `go test ./internal/evidence/processor` passed (normalization, truncation, redaction, and DB registration).

### 0015
- Status: complete
- QA: `go test ./internal/agents/runtime` passed (tool allowlist enforcement + runtime artifact/usage persistence smoke).

### 0016
- Status: complete
- QA: `go test ./internal/agents/planner` passed (schema validation + fixed-input golden-shape output checks).

### 0017
- Status: complete
- QA: `go test ./internal/agents/judge` passed (schema xor enforcement + offline evidence-set verdict generation).

### 0018
- Status: complete
- QA: `go test ./internal/orchestrator` passed (deterministic pass/fail/cannot_verify outcomes with fake planner/judge/executor loop).

### 0019
- Status: complete
- QA: `go test ./internal/report` passed (manifest integrity, report section ordering, bundle archive generation).

### 0020
- Status: complete
- QA: `go test ./internal/replay` passed (trace listing + best-effort replay that appends new trace artifacts).

### 0021
- Status: complete
- QA: `go test ./internal/integration` passed (deterministic orchestrator smoke with report artifact assertions).

### 0022
- Status: complete
- QA: `go test ./internal/agents/repair` passed (gating enforcement + dry-run proposal artifact smoke).

### 0023
- Status: complete
- QA: `go run ./cmd/qa-agent --help` output aligned with README command surface and examples.
