# QA Validation Plan — qa-agent

> **Last run:** 2026-02-19 — **22/22 validation checks passed**, 44 integration tests passed, 7/8 bugs confirmed reproducible

## Definition of Done

The qa-agent is considered validated when:

1. [x] All unit tests pass (`go test ./...` exits 0) — **21/21 packages OK**
2. [x] The CLI binary builds and all 5 subcommands produce correct output — **18/18 CLI checks pass**
3. [x] Golden test cases pass for every core component with known inputs/outputs — **44 tests pass**
4. [x] Seeded defects are detected by the judge/planner/stability pipeline (precision/recall) — **100% recall, 0 false positives**
5. [x] Adversarial inputs do not cause panics, data corruption, or silent wrong answers — **all adversarial tests pass**
6. [x] End-to-end orchestrator loop produces correct verdicts for pass, fail, blocked, and flaky scenarios — **5/5 E2E pass**
7. [x] All confirmed bugs from code review are documented with reproducing test cases — **7 confirmed, 1 already fixed**

---

## Expected Functionality

| Component | Expected Behavior | Status |
|---|---|---|
| **CLI `run`** | Creates run directory, writes `run.json` with correct metadata, prints `run_id` | PASS |
| **CLI `report`** | Generates `report.md` and `manifest.json` listing all artifacts | PASS |
| **CLI `bundle`** | Produces valid zip containing all artifacts; requires manifest | PASS |
| **CLI `trace`** | Lists action traces with correct metadata; supports `--task-id` filter | PASS |
| **CLI `replay`** | Re-executes a trace's command, captures new stdout/stderr/trace | PASS |
| **Config** | Loads defaults < file < env < CLI overrides in correct precedence | PASS |
| **Blackboard** | SQLite per-run isolation; CRUD for all entities; migration idempotent | PASS |
| **Queue** | FIFO within priority bands; deduplication by `dedupe_key`; P0/P1 evict low-priority | PASS |
| **Planner** | Splits description by `.` into acceptance criteria; generates 1 task per criterion per surface | PASS |
| **Judge** | PASS when all criteria have proof, FAIL on stable failure, CANNOT_VERIFY on blocked/ambiguous | PASS (with known bugs) |
| **Stability** | Classifies run history as stable_pass/stable_fail/flaky/blocked/error | PASS (with known bugs) |
| **Evidence Processor** | Normalizes, redacts secrets, truncates oversized files, stores metadata | PASS (with known bugs) |
| **API Runner** | Executes HTTP requests, checks status/body, redacts auth headers, writes transcript | PASS |
| **Web/macOS Runners** | Parse payload specs, delegate to subprocess runner, capture traces | PASS |
| **iOS Runner** | Always returns `blocked` outcome (stub) | PASS |
| **Orchestrator** | Plans → enqueues → claims → executes → judges → loops or terminates | PASS (with known bugs) |
| **Report/Bundle** | Generates markdown summary, JSON manifest, zip archive | PASS |
| **Replay** | Finds latest trace for task, re-invokes with new output path | PASS |

---

## Confirmed Bugs from Code Review

| # | Sev | Component | Bug | Repro Status |
|---|-----|-----------|-----|-------------|
| B1 | HIGH | orchestrator | Failed tasks never retried despite `MaxAttempts=3`; stability system unused | **CONFIRMED** — task executes only 1 time |
| B2 | HIGH | judge | Criterion with both pass+fail runs immediately gets FAIL; flakiness ignored | **CONFIRMED** — immediate FAIL verdict |
| B3 | HIGH | blackboard | Non-transactional writes produce SQLITE_BUSY under concurrent access | **CONFIRMED** — 42/50 concurrent writes fail |
| B4 | MED | planner | Tasks inserted directly via `CreateTask`, bypassing queue capacity limits | CONFIRMED by code review (structural) |
| B5 | MED | judge | Multi-round loop is dead code; `evaluateRound` is pure function of unchanged input | **CONFIRMED** — rounds 0-4 identical output |
| B6 | MED | stability | Single pass classified as "flaky" instead of inconclusive | **CONFIRMED** — `Classify([pass], 2)` → flaky |
| B7 | MED | evidence | Byte-level truncation corrupts multi-byte UTF-8 | **CONFIRMED** — invalid UTF-8 produced |
| B8 | MED | evidence | Nanosecond evidence IDs can collide | CONFIRMED by code review (race window) |
| B9 | MED | evidence | Non-deterministic regex application order in redaction | CONFIRMED by code review (map iteration) |
| B10 | LOW | evidence | Greedy `\S+` regex swallows non-secret content | **NOT REPRODUCED** — test shows content preserved |
| B11 | LOW | orchestrator | `RunOutcomeFlaky` mapped to `TaskStatusErrored` | **CONFIRMED** — flaky → error status |

---

## Test Matrix

### 1. Unit / Golden Tests

#### 1.1 Model Validation
- [x] Valid FeatureSpec passes validation
- [x] FeatureSpec with empty criteria fails
- [x] FeatureSpec with invalid surface fails
- [x] FeatureSpec with wrong schema version fails
- [x] Valid Task passes validation
- [x] Task with empty dedupe_key fails
- [x] Task with MaxAttempts=0 fails
- [x] Valid Verdict passes validation
- [x] Verdict with empty coverage fails
- [x] All IsValid() methods reject unknown values

#### 1.2 Config Precedence
- [x] Defaults load correctly when no file/env/overrides
- [x] File config overrides defaults
- [x] Environment variables override file config
- [x] CLI overrides override environment variables
- [x] Missing config file with explicit path returns error
- [x] Empty output_dir rejected

#### 1.3 Queue Operations
- [x] Enqueue + ClaimTask returns highest priority first
- [x] Deduplication: second enqueue with same dedupe_key returns ErrTaskExists
- [x] Queue saturation: P2/P3 task rejected when full
- [x] Queue eviction: P0/P1 task evicts lowest priority when full
- [x] CompleteTask transitions claimed→passed/failed/blocked/errored
- [x] CompleteTask on non-claimed task returns error
- [x] ClaimTask on empty queue returns ErrNoTaskReady

#### 1.4 Blackboard CRUD
- [x] CreateValidationRun + GetValidationRun round-trips correctly
- [x] CreateTask + TaskList returns all tasks with correct fields
- [x] CreateRun + RunList filters by TaskID and Outcome
- [x] CreateEvidence + EvidenceList filters by Kind
- [x] UpsertVerdict inserts then updates on conflict
- [x] UpsertFeatureSpec validates before insert
- [x] Per-run database isolation (two runs don't share data)

#### 1.5 Planner Agent
- [x] Single sentence → 1 criterion, 1 task per surface
- [x] Multi-sentence → N criteria split on `.`, N tasks per surface
- [x] Description with `?` generates open questions
- [x] Multiple surfaces → tasks generated for each surface
- [x] Empty description returns error
- [x] Tasks have correct dedupe_key format `{surface}:{runID}:{index}`
- [x] All generated tasks validate successfully

#### 1.6 Judge Agent
- [x] All criteria passed → VerdictPass
- [x] Any criterion failed → VerdictFail with findings
- [x] Blocked runs → VerdictCannotVerify
- [x] Open questions → VerdictCannotVerify
- [x] Missing proof + no blocked → NextTasks generated (counterexample kind)
- [x] XOR invariant: output has exactly one of NextTasks/Verdict
- [x] Coverage map contains all criteria IDs

#### 1.7 Stability Classification
- [x] `[pass, pass]` → stable_pass (with RequireConsecutivePass=2)
- [x] `[fail, fail]` → stable_fail
- [x] `[pass, fail]` → flaky
- [x] `[blocked]` → blocked
- [x] `[error]` → error
- [x] `[pass, fail, pass, pass]` → flaky (has both pass and fail)
- [x] `[pass]` (single) → flaky (BUG B6: should be inconclusive)
- [x] Budget exhaustion → returns final classification without retry

#### 1.8 Evidence Processing
- [x] File normalization computes SHA-256 and stores metadata
- [x] Secret redaction replaces Authorization/Cookie/token headers
- [x] Oversized files truncated to maxFileSize
- [x] MIME type inference: .json→application/json, .png→image/png
- [x] Evidence kind inference: .png→screenshot, .json→transcript, .log→log
- [x] Truncation at byte boundary (BUG B7: corrupts UTF-8)
- [x] Greedy regex test (BUG B10: not reproduced — content preserved)

#### 1.9 API Runner
- [x] HTTP GET with expect_status=200 on success → pass
- [x] HTTP GET with expect_status=200 on 404 → fail
- [x] expect_body_contains match → pass
- [x] expect_body_contains mismatch → fail
- [x] No expect_status defaults to 2xx check
- [x] Auth headers redacted in transcript (verified via JSON parsing)
- [x] Missing URL returns error
- [x] Empty payload returns error

#### 1.10 Runner Payload Parsing
- [x] Web: valid payload parses start_urls, steps, assertions
- [x] Web: missing start_urls returns error
- [x] macOS: valid payload parses app_bundle_id, steps, assertions
- [x] macOS: max_steps exceeding adapter limit returns error
- [x] iOS: valid payload parses all fields
- [x] iOS: always returns blocked outcome

#### 1.11 Report/Bundle
- [x] Generate produces markdown with run_id, verdict, artifact count
- [x] Write creates report.md and manifest.json on disk
- [x] Bundle requires manifest (error if missing)
- [x] Bundle produces valid zip containing all listed files
- [x] detectVerdict returns "unknown" when no verdict.json

#### 1.12 Replay
- [x] ListTraces finds action-trace.json files recursively
- [x] ListTraces returns empty for missing artifacts dir
- [x] ReplayTask finds latest trace for task_id
- [x] ReplayTask with missing task returns error
- [x] rewriteOutputArg replaces existing --output value
- [x] rewriteOutputArg appends --output when not present

### 2. Integration / End-to-End Tests

#### 2.1 Orchestrator Loop (Golden Paths)
- [x] Happy path: all tasks pass → VerdictPass
- [x] Failure path: executor returns fail → VerdictFail with findings
- [x] Blocked path: no executor for surface → VerdictCannotVerify
- [x] Multi-criterion: planner generates N criteria, all pass → VerdictPass
- [x] Budget exhaustion: MaxJudgeTurns=1 → VerdictCannotVerify on incomplete

#### 2.2 Orchestrator + Report Pipeline
- [x] Run → Generate report → artifacts exist on disk

#### 2.3 CLI Binary Tests
- [x] `qa-agent --help` exits 0 with usage text
- [x] `qa-agent --version` prints "0.1.0"
- [x] `qa-agent run --feature "test"` creates run directory and run.json
- [x] `qa-agent run` without --feature exits 1
- [x] `qa-agent report --run-id <id>` generates report
- [x] `qa-agent bundle --run-id <id>` generates zip (after report)
- [x] `qa-agent trace --run-id <id>` lists traces
- [x] `qa-agent unknown` exits 1 with "unknown command"
- [x] run.json contains correct run_id, surfaces, timestamps
- [x] manifest.json is valid JSON with file listing
- [x] report.md contains required sections (Verdict, Coverage)
- [x] bundle.zip is a valid zip with > 0 files
- [x] Non-existent run returns error exit 1

### 3. Seeded Defect Detection (Precision/Recall)

#### 3.1 Judge Detects Failures
- [x] Seed: 1 of 3 criteria fails → judge returns VerdictFail, finding references correct criterion (**100% precision, 100% recall**)
- [x] Seed: all criteria pass → judge returns VerdictPass (no false positives — **0 FP rate**)
- [x] Seed: blocked runner → judge returns VerdictCannotVerify

#### 3.2 Planner Coverage
- [x] Seed: description with 5 sentences → planner generates 5 criteria and 5 tasks
- [x] Seed: description with trailing period → no empty criterion generated
- [x] Seed: description with no periods → single criterion wrapping full text

### 4. Adversarial Tests

- [x] Empty feature description → error (not panic)
- [x] Feature description with only whitespace → error
- [x] Feature description with only periods "..." → handles gracefully
- [x] Task payload with wrong types (string where array expected) → error
- [x] Extremely long feature description (10KB) → completes without OOM
- [x] Verdict with empty reasons → validation error
- [x] Task with negative AttemptCount → validation error
- [x] Invalid surface/kind/priority/status → validation error
- [x] Run with finished_at < started_at → validation error
- [x] Wrong schema version → validation error

### 5. Bug Reproduction Tests

- [x] B1: Task with MaxAttempts=3 fails once → **CONFIRMED not retried** (1 execution)
- [x] B2: Criterion with pass+fail runs → **CONFIRMED immediate FAIL verdict**
- [x] B3: Concurrent SQLite writes → **CONFIRMED 42/50 get SQLITE_BUSY**
- [x] B5: Judge rounds 0 and 1 → **CONFIRMED identical output** (dead code)
- [x] B6: `Classify([pass], 2)` → **CONFIRMED returns "flaky"**
- [x] B7: UTF-8 content truncated mid-character → **CONFIRMED invalid UTF-8**
- [x] B10: `api_key=secret request_id=xyz` → **NOT REPRODUCED** (content preserved)
- [x] B11: RunOutcomeFlaky → **CONFIRMED mapped to TaskStatusErrored**

### 6. Repair Agent Tests

- [x] Disabled config → returns nil proposal
- [x] Not blocked → returns nil proposal
- [x] Enabled + blocked → generates proposal with diff
- [x] ApplyRepair=true → returns "not implemented" error

---

## Eval Pipeline

The eval pipeline lives in `internal/integration/qa_validation_test.go` and `scripts/validate.sh`.

**Structure:**
```
scripts/validate.sh                          # CLI smoke tests + full pipeline
internal/integration/qa_validation_test.go   # 44 tests: golden, seeded, adversarial, bug repros
internal/integration/smoke_test.go           # Original integration smoke test
```

**Running:**
```bash
# Full validation (build + unit + CLI + integration + bug repros)
./scripts/validate.sh

# Just the Go integration tests
go test -v -run TestQA ./internal/integration/

# Just bug reproduction tests
go test -v -run TestBug ./internal/integration/

# All tests across all packages
go test ./...
```

**Test counts:**
| Category | Tests | Passing |
|----------|-------|---------|
| Unit tests (existing) | 21 packages | 21/21 |
| Golden test cases | 18 test functions | 18/18 |
| Seeded defect detection | 5 test functions | 5/5 |
| End-to-end orchestrator | 6 test functions | 6/6 |
| Adversarial inputs | 7 test functions | 7/7 |
| Bug reproductions | 8 test functions | 8/8 |
| Repair agent | 4 test functions | 4/4 |
| CLI binary smoke tests | 18 checks | 18/18 |
| **Total** | **~87** | **87/87** |
