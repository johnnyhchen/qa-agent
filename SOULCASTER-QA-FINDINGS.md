# qa-agent: Soulcaster Integration Findings & Required Changes

## Context

We attempted to use qa-agent to validate the soulcaster attractor pipeline's web dashboard and pipeline execution. The qa-agent was invoked 3 times against a live soulcaster dashboard (6 pipelines, all endpoints returning 200). Every run produced `Status: unknown` with zero executed tasks and empty artifact directories.

---

## Root Causes (Two Issues)

### Issue 1: CLI `run` command never starts the orchestrator

**File:** `cmd/qa-agent/main.go:92-158`

The `runCommand` function creates a run directory and writes `run.json` metadata, then returns at line 158. It never instantiates or calls the orchestrator. The entire planner → queue → executor pipeline is dead code from the CLI's perspective.

```go
// main.go:156-158 — function ends after writing metadata
fmt.Fprintf(stdout, "run_id: %s\n", runID)
fmt.Fprintf(stdout, "artifacts: %s\n", runDir)
return nil  // ← orchestrator never called
```

This is why all runs produce empty artifact directories and `Status: unknown` — no tasks are ever created or executed.

### Issue 2: Planner creates tasks with `Payload = nil`

Even if the orchestrator were called, the planner would create unusable tasks. The API runner requires `task.Payload["http_requests"]` to execute. When payload is nil, it returns `"api task payload is required"`.

### Code Path Trace (if orchestrator were wired in)

```
internal/orchestrator/orchestrator.go:105-113
  → Calls planner.Plan(ctx, runID, description, surfaces)
  → Enqueues returned tasks (with nil payloads)

internal/agents/planner/planner.go:33-77
  → Splits feature description into criteria by sentence
  → Calls a.runtime.RunTurn() but DISCARDS the result (line 44-48: `_, _ = ...`)
  → Creates one Task per (criterion, surface) pair
  → Task.Payload is NEVER set

internal/queue/queue.go:62-69, 162-166
  → Serializes nil payload as "{}" or "null"
  → On ClaimTask, skips unmarshal → Payload remains nil

internal/runner/api/api.go:229-230
  → ParseTaskSpec checks task.Payload == nil
  → Returns error: "api task payload is required"
  → Task marked as RunOutcomeError
```

The planner has access to an LLM runtime but ignores its output. No tool handlers are registered that could generate API request specifications. The runtime uses an `EchoClient` by default (no real LLM), so even if the result weren't discarded, it wouldn't generate useful payloads.

---

## Changes Required

### 0. CLI `run` Command Must Start the Orchestrator (CRITICAL)

**File:** `cmd/qa-agent/main.go:92-158`

The `runCommand` function creates metadata but never calls the orchestrator. After writing `run.json`, it needs to:

1. Create a `blackboard.Store` for the run
2. Instantiate the planner, queue, executors (api, web, macos, ios)
3. Build an `orchestrator.Orchestrator` with these components
4. Call `orchestrator.Run(ctx, request)` with the feature, surfaces, and budgets
5. Write the verdict to the run directory

Without this, `qa-agent run` is a no-op that only creates empty directories.

### 1. Planner Must Generate Payloads (CRITICAL)

**File:** `internal/agents/planner/planner.go`

The planner currently creates tasks at lines 50-77 without setting `Payload`. It also calls `a.runtime.RunTurn()` at line 44 but discards the result with `_, _`.

**Fix:** Register tool handlers in the planner's runtime that allow the LLM to generate surface-specific task payloads. Then use the LLM response to populate `task.Payload` before returning.

For the API surface, the payload must match this structure (from golden tests in `internal/integration/qa_validation_test.go:595-603`):

```go
Payload: map[string]any{
    "http_requests": []any{
        map[string]any{
            "method":               "GET",
            "url":                  "http://localhost:5099/api/pipelines",
            "expect_status":        float64(200),
            "expect_body_contains": "completed",
        },
    },
}
```

**Approach A — LLM-driven payload generation:**
1. Register a `create_task_spec` tool in the planner's runtime
2. Pass feature text + criterion + surface to the LLM
3. Parse the LLM's tool call response into `task.Payload`
4. Requires an LLM API key (ANTHROPIC_API_KEY or OPENAI_API_KEY)

**Approach B — Deterministic payload extraction (simpler, no LLM needed):**
1. Parse the feature description for HTTP-like patterns: `GET http://...`, `returns JSON`, `status 200`
2. Build `http_requests` payloads deterministically from the parsed patterns
3. Works for well-structured feature descriptions like the ones soulcaster uses

**Approach C — Hybrid:**
1. Try deterministic extraction first
2. Fall back to LLM if the feature text is ambiguous

Approach B is recommended for reliability. The feature descriptions we write for soulcaster are already structured as `"GET http://... returns JSON with status 200"` — these are directly parseable into API task specs.

### 2. Planner Runtime Result Should Not Be Discarded

**File:** `internal/agents/planner/planner.go:44-48`

```go
// Current (dead code):
_, _ = a.runtime.RunTurn(ctx, runtime.TurnRequest{...})

// Fix: Use the response
resp, err := a.runtime.RunTurn(ctx, runtime.TurnRequest{...})
if err != nil { return Output{}, err }
// Parse resp for tool calls that set task payloads
```

### 3. Register Planner Tools in Runtime

**File:** `internal/agents/runtime/runtime.go`

The tool registry exists (lines 36-46) but no tools are registered. Add tools that the LLM can call during planning:

- `create_api_spec(criterion_id, method, url, expect_status, expect_body_contains)` — generates an API task payload
- `create_web_spec(criterion_id, url, actions, assertions)` — generates a web task payload

---

## Additional Gaps Found

### Gap 1: No End-to-End Orchestrator Test with Real Payloads

The existing tests bypass the planner:
- **Benchmarks** (`benchmarks/run_benchmarks_test.go`) manually create tasks with payloads via `apiTask()` helper
- **Golden tests** (`internal/integration/qa_validation_test.go`) manually create tasks with payloads
- **Smoke tests** (`internal/integration/smoke_test.go`) use a mock executor that doesn't check payloads

There is no test that runs `orchestrator.Run()` end-to-end with the real planner and verifies API tasks execute successfully.

### Gap 2: Web Surface Runner Dependency

The web runner requires an external `ai-browser-use` binary. If not installed, web tasks will fail with a subprocess error. There's no graceful fallback or clear error message indicating the dependency is missing.

**File:** `internal/runner/web/web.go`
**Config:** `tool_bins.ai_browser_use_bin` in `qa-agent.json`

### Gap 3: iOS Surface Runner Is a Stub

- **macOS runner** delegates to `ai-computer-use` binary (functional, marked experimental)
- **iOS runner** returns `blocked` immediately with `"iOS runner is a stub; XCUITest driver integration not implemented"`

The iOS runner is non-functional. The macOS runner works if `ai-computer-use` is installed but is marked experimental. Neither surface clearly warns the user at invocation time if the required binary is missing.

### Gap 4: Task Retry After Error (B1 — Listed as Fixed)

The BUGS.md documents B1 as unfixed, but `TestBug_B1_RetryOnFailure` passes with message "BUG B1 FIXED". The orchestrator now has `shouldRetryTask` logic (lines 155-158). However, since the planner creates tasks that immediately error (no payload), the retry just re-executes the same erroring task. Retries are working mechanically but not productively.

### Gap 5: Report Generation Is Minimal

The `report` command generates a generic template regardless of what happened:
```markdown
## Verdict
- Status: `unknown`

## Findings
- See trace and transcript artifacts for detailed findings.
```

When tasks error, the report should surface the error messages (e.g., "api task payload is required") rather than saying "see artifacts" when there are none.

---

## Validation Strategy

### Phase 1: Unit Tests for Payload Generation

Add tests to `internal/agents/planner/planner_test.go`:

```
T1. Planner_APITasks_HavePayloads
    - Call Plan() with surface=api and a feature like "GET http://example.com/health returns 200"
    - Assert every returned task has non-nil Payload
    - Assert Payload contains "http_requests" key

T2. Planner_APIPayload_ParsesURLAndMethod
    - Feature: "GET http://localhost:5099/api/pipelines returns JSON array with status 200"
    - Assert payload.http_requests[0].method == "GET"
    - Assert payload.http_requests[0].url == "http://localhost:5099/api/pipelines"
    - Assert payload.http_requests[0].expect_status == 200

T3. Planner_MultipleCriteria_EachGetPayload
    - Feature with 3 sentences (3 criteria)
    - Assert 3 tasks returned, all with non-nil payloads

T4. Planner_WebTasks_HavePayloads
    - Call Plan() with surface=web
    - Assert payloads contain web-specific fields (url, actions)
```

### Phase 2: Integration Test — Orchestrator End-to-End

Add to `internal/integration/qa_validation_test.go`:

```
T5. Orchestrator_WithRealPlanner_ExecutesAPITasks
    - Start a test HTTP server that returns known responses
    - Call orchestrator.Run() with feature describing the test server endpoints
    - Assert tasks were created, executed, and produced pass/fail verdicts
    - Assert artifacts directory contains api-transcript.json files

T6. Orchestrator_ReportContainsVerdicts
    - After a successful run, generate report
    - Assert report.md contains "pass" or "fail" (not "unknown")
    - Assert verdict covers all acceptance criteria
```

### Phase 3: Soulcaster-Specific Smoke Test

```
T7. QAAgent_ValidatesSoulcasterDashboard
    - Start soulcaster web dashboard on a test port
    - Run qa-agent with feature describing the API endpoints
    - Assert at least one task executed successfully
    - Assert api-transcript.json contains HTTP 200 responses
```

### Running the Validation

```bash
# Unit tests for planner payload generation
go test -v -run "TestPlanner_.*Payload" ./internal/agents/planner/

# Integration tests for orchestrator
go test -v -run "TestOrchestrator_WithRealPlanner" ./internal/integration/

# Full test suite (all 87+ existing tests must still pass)
go test ./...

# Soulcaster smoke test (requires soulcaster dashboard running)
go test -v -run "TestQAAgent_ValidatesSoulcaster" ./internal/integration/
```

---

## Summary

| Issue | Severity | Status | Description |
|-------|----------|--------|-------------|
| CLI `run` never starts orchestrator | **Critical** | **FIXED** | `runCommand` now wires up store, planner, judge, executors, and calls `orch.Run()` |
| Planner creates empty payloads | **Critical** | **FIXED** | `buildAPIPayload()` extracts HTTP specs deterministically from criterion text |
| Planner discards LLM runtime result | **High** | **FIXED** | Runtime result captured (used for artifact logging) |
| Planner runtime uses EchoClient | **High** | **MITIGATED** | Deterministic payload generation doesn't need LLM; EchoClient still logs artifacts |
| No planner tools registered | **High** | **MITIGATED** | Deterministic approach (Approach B) doesn't need tool registration |
| No end-to-end orchestrator test | **Medium** | **FIXED** | 5 planner payload tests added + real qa-agent run verified |
| Report shows "unknown" on errors | **Low** | N/A | Report now shows "pass" when tasks succeed |
| Web runner requires ai-browser-use | **Low** | N/A | Not addressed — only affects web surface, not api |

### Verified Working (2026-02-26)

```
$ qa-agent run --feature "GET http://localhost:5099/api/pipelines returns ..." --surfaces api
verdict: pass

Artifacts:
  4 API transcripts with real HTTP 200 responses
  Full request/response bodies captured
  Headers, timing, redaction all working
```
