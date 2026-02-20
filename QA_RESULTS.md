# QA Results — Post-Fix Validation

> Run date: 2026-02-19
> All 21 Go packages pass. All 8 bugs verified fixed. Eval pipeline: 15/15 correct, 100% precision, 100% recall.

## Summary

| Metric | Before fixes | After fixes |
|--------|-------------|-------------|
| Unit tests (21 packages) | 21/21 pass | 21/21 pass |
| CLI smoke tests | 18/18 pass | 18/18 pass |
| Integration tests | 44/44 pass | 44/44 pass |
| Eval pipeline (real HTTP) | 0/15 (broken — B12) | 15/15 (100%) |
| Bug repro tests | 7/8 confirmed broken | 8/8 verified fixed |
| Precision | N/A | 100% |
| Recall | N/A | 100% |

## Bug Fix Verification

| Bug | Description | Status | Evidence |
|-----|-------------|--------|----------|
| B1 | Failed tasks never retried | **FIXED** | Task retried 3 times (was 1) |
| B2 | Pass+fail criterion → immediate FAIL | **FIXED** | Judge requests more evidence instead |
| B3 | SQLITE_BUSY under concurrency | **FIXED** | 50/50 concurrent enqueues succeed (was 8/50) |
| B5 | Judge multi-round loop dead code | **FIXED** | Different round counts produce different outputs |
| B6 | Single pass → "flaky" | **FIXED** | Classified as "inconclusive" |
| B7 | UTF-8 truncation corruption | **FIXED** | Truncated file is valid UTF-8 (254 bytes) |
| B11 | RunOutcomeFlaky → TaskStatusErrored | **FIXED** | Mapped to TaskStatusFailed |
| B12 | Task payload not persisted | **FIXED** | Full E2E pipeline works |

## Eval Pipeline — Real HTTP Against Seeded Defects

```
╔══════════════════════════════════════════════════════════════════╗
║              QA-AGENT EVAL PIPELINE RESULTS                    ║
╠══════════════════════════════════════════════════════════════════╣
║ [PASS] health_check_clean                 expected=pass  got=pass
║ [PASS] login_valid_creds_clean            expected=pass  got=pass
║ [PASS] login_bad_creds_returns_401_clean  expected=pass  got=pass
║ [PASS] get_users_with_auth_clean          expected=pass  got=pass
║ [PASS] get_users_no_auth_returns_401      expected=pass  got=pass
║ [PASS] get_user_exists_clean              expected=pass  got=pass
║ [PASS] get_user_missing_returns_404       expected=pass  got=pass
║ [PASS] create_user_returns_201_clean      expected=pass  got=pass
║ [PASS] search_returns_results_clean       expected=pass  got=pass
║ [PASS] health_check_buggy (clean ep)      expected=pass  got=pass
║ [PASS] BUG1: login accepts any creds      expected=fail  got=fail
║ [PASS] BUG2: /users missing auth check    expected=fail  got=fail
║ [PASS] BUG4: missing user returns 200     expected=fail  got=fail
║ [PASS] BUG5: create returns 200 not 201   expected=fail  got=fail
║ [PASS] BUG7: search returns broken JSON   expected=fail  got=fail
╠══════════════════════════════════════════════════════════════════╣
║ Total:          15/15 correct
║ True positives: 5       True negatives: 10
║ False positives: 0      False negatives: 0
║ Precision: 100%         Recall: 100%
╚══════════════════════════════════════════════════════════════════╝
```

## What Was Fixed

### B1 — Orchestrator retry logic (`orchestrator.go`)
Added `shouldRetryTask()` that checks `AttemptCount < MaxAttempts`, consults `stability.Policy.Decide()`, and calls `queue.RequeueTask()` to put the task back in the queue. Previously failed tasks were immediately marked terminal.

### B2 — Judge flakiness handling (`judge.go`)
Added `failFound && passFound` branch at line 141 that routes flaky criteria to `missingProof` instead of `failedCriteria`. The judge now requests more evidence for criteria with mixed results instead of issuing an immediate FAIL.

### B3 — SQLite busy timeout (`store.go`)
Added `PRAGMA busy_timeout=5000` and `SetMaxOpenConns(1)` per-database in `dbForRun()`. Eliminates writer lock contention.

### B5 — Judge multi-round state propagation (`judge.go`)
Changed `Evaluate()` to feed round N's `NextTasks` back into round N+1's input (`rolling.Tasks`). Rounds now accumulate state instead of re-evaluating identical input.

### B6 — Stability inconclusive outcome (`stability.go`)
Added `OutcomeInconclusive` for the case where `hasPass && !hasFail && consecutivePasses < required`. Single pass is no longer misclassified as flaky.

### B7 — UTF-8-safe truncation (`processor.go`)
After byte-level truncation, walks backward to find valid UTF-8 boundary: `for len(redacted) > 0 && !utf8.Valid(redacted) { redacted = redacted[:len(redacted)-1] }`.

### B11 — Flaky status mapping (`orchestrator.go`)
Added `model.RunOutcomeFlaky` to the `model.RunOutcomeFail` case in `mapTaskStatus()`.

### B12 — Task payload persistence (`store.go`, `queue.go`)
Added `payload_json` column to tasks table. Updated `CreateTask`, `EnqueueTask`, `ClaimTask`, and `TaskList` to marshal/unmarshal the `Payload` field.

## How to Run

```bash
# Full validation suite (build + unit + CLI + integration + bug repros)
./scripts/validate.sh

# Eval pipeline only (real HTTP against clean + buggy servers)
go test -v -run TestEval_FullReport ./internal/integration/

# Bug reproduction tests only
go test -v -run TestBug ./internal/integration/

# All tests
go test ./...
```

## Files Modified (fixes)

| File | Change |
|------|--------|
| `internal/orchestrator/orchestrator.go` | Retry logic, flaky mapping, `shouldRetryTask()` |
| `internal/agents/judge/judge.go` | Flaky criterion handling, multi-round state |
| `internal/stability/stability.go` | `OutcomeInconclusive` |
| `internal/evidence/processor/processor.go` | UTF-8-safe truncation |
| `internal/blackboard/store.go` | `payload_json` column, busy timeout |
| `internal/queue/queue.go` | `payload_json` persistence, `RequeueTask()` |

## Files Modified (tests)

| File | Content |
|------|---------|
| `internal/integration/qa_validation_test.go` | 44 tests: golden, seeded, adversarial, bug repros |
| `internal/integration/eval_pipeline_test.go` | E2E eval: clean server, buggy server, precision/recall |
| `scripts/validate.sh` | Automated validation runner |
| `QA_PLAN.md` | Test matrix with checkboxes |
| `FINDINGS.md` | Initial findings before fixes |
| `BUGS.md` | Bug specs for devs (now all resolved) |
