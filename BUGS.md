# Open Bugs

7 confirmed bugs remaining after QA validation. Each has a reproducing test in `internal/integration/qa_validation_test.go`.

Run all reproductions: `go test -v -run TestBug ./internal/integration/`

---

## B1 — Failed tasks never retried (HIGH)

**File:** `internal/orchestrator/orchestrator.go:120-159`

**Problem:** When a task fails, the orchestrator marks it with a terminal status immediately at line 155-158. It never checks `task.MaxAttempts` or `task.AttemptCount`. The `stability.Policy.Decide()` function exists and returns `Retry: true` for flaky/error outcomes, but nothing in the orchestrator calls it.

**Current code (line 155-158):**
```go
finalStatus := mapTaskStatus(runRecord.Outcome)
if err := o.queue.CompleteTask(ctx, request.RunID, task.TaskID, finalStatus); err != nil {
    return model.Verdict{}, err
}
```

**Fix:** Before calling `CompleteTask`, check whether the task should be retried:
```go
finalStatus := mapTaskStatus(runRecord.Outcome)
if finalStatus == model.TaskStatusFailed || finalStatus == model.TaskStatusErrored {
    if task.AttemptCount < task.MaxAttempts {
        // Re-queue instead of completing
        // Reset status to queued in the DB
        continue
    }
}
if err := o.queue.CompleteTask(...) { ... }
```

You'll need to add a `RequeueTask` method to the queue manager that sets `status = 'queued'` and clears the claim fields on a task that's currently `claimed`. Then wire in `stability.Policy.Decide()` to determine whether to retry or finalize.

**Reproducing test:** `TestBug_B1_NoRetryOnFailure`

---

## B2 — Judge treats flaky criteria as failures (HIGH)

**File:** `internal/agents/judge/judge.go:113-151`

**Problem:** For each acceptance criterion, the judge sets `passFound` and `failFound` independently. If a criterion has **both** a passing run and a failing run (flaky), `failFound` is true, so it goes into `failedCriteria` at line 136. The verdict becomes `VerdictFail`. The passing evidence is silently ignored.

**Current code (line 135-147):**
```go
if failFound {
    failedCriteria = append(failedCriteria, criterionID)
    findings = append(findings, model.Finding{...})
}
```

**Fix:** Add a flaky check before the fail path:
```go
if failFound && passFound {
    // This criterion is flaky, not a stable failure.
    // Either: add to a "flaky" list and use stability.Classify(),
    // or: add to missingProof to request more runs.
    missingProof = append(missingProof, criterionID)
} else if failFound {
    failedCriteria = append(failedCriteria, criterionID)
    findings = append(findings, model.Finding{...})
}
```

This way, a criterion that has both pass and fail evidence triggers more investigation rather than an immediate fail verdict.

**Reproducing test:** `TestBug_B2_JudgeIgnoresFlakiness`

---

## B3 — SQLITE_BUSY under concurrent writes (HIGH)

**File:** `internal/blackboard/store.go:574-592`

**Problem:** `dbForRun()` opens SQLite with WAL mode but sets no busy timeout. The `Store` methods `CreateTask`, `CreateRun`, `CreateEvidence`, `UpsertVerdict`, and `UpsertFeatureSpec` execute SQL directly on the `*sql.DB` without transactions. Under concurrent access, SQLite returns `SQLITE_BUSY` immediately. In testing, 42/50 concurrent enqueues fail.

**Current code (line 590):**
```go
db, err := sql.Open("sqlite", dsn)
```

**Fix — option A (simple):** Add a busy timeout to the DSN:
```go
db, err := sql.Open("sqlite", dsn+"?_busy_timeout=5000")
```

**Fix — option B (robust):** Wrap all write methods in `WithRunTx` the same way the queue manager already does. The queue's `EnqueueTask` and `ClaimTask` use `WithRunTx` and work correctly. Apply the same pattern to `CreateRun`, `CreateEvidence`, `UpsertVerdict`, `UpsertFeatureSpec`.

Option A is a one-line fix. Option B is safer for correctness.

**Reproducing test:** `TestBug_B3_ConcurrentSQLiteBusy`

---

## B5 — Judge multi-round loop is dead code (MEDIUM)

**File:** `internal/agents/judge/judge.go:42-53`

**Problem:** `Evaluate()` calls `evaluateRound(input, round)` in a loop, but `evaluateRound` is a pure function of `input` and `round`. The `input` is never modified between rounds. The `round` parameter only changes ID suffixes, not evaluation logic. If round 0 produces no output, rounds 1-4 won't either.

**Current code (line 42-53):**
```go
var final Output
for round := 0; round < input.MaxRounds; round++ {
    out := evaluateRound(input, round)
    if out.Verdict != nil || len(out.NextTasks) > 0 {
        final = out
        break
    }
}
```

**Fix — option A (remove dead code):** Delete the loop, just call `evaluateRound(input, 0)` once.

**Fix — option B (make it useful):** Feed the output of round N back as input to round N+1. For example, if round 0 produces `NextTasks`, simulate their execution or merge them into the input tasks for round 1.

**Reproducing test:** `TestBug_B5_JudgeMultiRoundNoop`

---

## B6 — Single pass classified as "flaky" (MEDIUM)

**File:** `internal/stability/stability.go:116-128`

**Problem:** When `history` has a single pass `[pass]`, `consecutivePasses` is 1 which is less than `requiredConsecutivePasses` (default 2). So it falls through the `StablePass` check at line 116. The `hasPass && hasFail` check at line 119 is false. The `hasError` check at line 122 is false. Then line 125: `hasPass` is true → returns `OutcomeFlaky`.

A single passing run is not flaky. It's inconclusive / needs more data.

**Current code (line 125-127):**
```go
if hasPass {
    return OutcomeFlaky
}
```

**Fix:** Add an `OutcomeInconclusive` outcome, or return `OutcomeError` to signal "not enough data":
```go
if hasPass && consecutivePasses < requiredConsecutivePasses {
    return OutcomeInconclusive // new: needs more runs
}
```

If you don't want a new outcome type, you could also just keep returning `OutcomeFlaky` but document that it means "needs more runs" when there are no failures. However, any downstream consumer checking `== OutcomeFlaky` to mean "genuinely non-deterministic" will get false positives.

**Reproducing test:** `TestBug_B6_SinglePassIsFlaky`

---

## B7 — UTF-8 truncation corruption (MEDIUM)

**File:** `internal/evidence/processor/processor.go:104-106`

**Problem:** After redaction, files larger than `maxFileSize` are truncated with a byte slice: `redacted = redacted[:p.maxFileSize]`. This can cut in the middle of a multi-byte UTF-8 character (emoji = 4 bytes, CJK = 3 bytes), producing invalid UTF-8. The corrupted bytes are written to disk and hashed.

**Current code (line 104-106):**
```go
if len(redacted) > p.maxFileSize {
    redacted = redacted[:p.maxFileSize]
    truncated = true
}
```

**Fix:** Walk backward from the cut point to find a valid rune boundary:
```go
if len(redacted) > p.maxFileSize {
    redacted = redacted[:p.maxFileSize]
    // Back up to the last valid UTF-8 rune boundary
    for len(redacted) > 0 && !utf8.Valid(redacted) {
        redacted = redacted[:len(redacted)-1]
    }
    truncated = true
}
```

Requires `import "unicode/utf8"`.

**Reproducing test:** `TestBug_B7_TruncationCorruptsUTF8`

---

## B11 — RunOutcomeFlaky mapped to TaskStatusErrored (LOW)

**File:** `internal/orchestrator/orchestrator.go:237-248`

**Problem:** `mapTaskStatus()` has no case for `RunOutcomeFlaky`, so it falls into `default` and returns `TaskStatusErrored`. A flaky run is not an error — it's a signal to retry or investigate.

**Current code (line 237-248):**
```go
func mapTaskStatus(outcome model.RunOutcome) model.TaskStatus {
    switch outcome {
    case model.RunOutcomePass:
        return model.TaskStatusPassed
    case model.RunOutcomeFail:
        return model.TaskStatusFailed
    case model.RunOutcomeBlocked:
        return model.TaskStatusBlocked
    default:
        return model.TaskStatusErrored
    }
}
```

**Fix:** Either add a `TaskStatusFlaky` to the model, or map flaky outcomes to an existing status that triggers retry logic (once B1 is fixed):
```go
case model.RunOutcomeFlaky:
    return model.TaskStatusFailed // or a new TaskStatusFlaky
```

This is low priority on its own but becomes relevant once B1 (retry logic) is implemented.

**Reproducing test:** `TestBug_B11_FlakyMappedToErrored`
