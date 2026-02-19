# 0013 - Stability Wrapper (Budgets, Flake Classification, Quarantine)

Commit message:
- `feat: add stability wrapper to prevent flake-driven infinite loops`

Goal:
- Ensure nondeterministic runners and flaky checks do not cause unbounded retries.

Scope:
- Retry and time budgets.
- Flake classification.
- Quarantine behavior.

Planned changes:
- Add `internal/stability/`.
- Budget model: per-assertion, per-task, per-run, global.
- Outcome classification: `stable_pass`, `stable_fail`, `flaky`, `blocked`, `error`.
- Retry policy: when to retry and when to stop.
- Persist stability labels onto `Run` and/or `Evidence.summary_fields`.
- Add stop conditions needed by the judge.
- For flaky surfaces, require two consecutive stable passes for proof.
- For fail, require one stable repro run with minimal repro evidence.

Verification:
- Unit tests: retry stops at limits; flake classification behaves as expected across sequences.

Exit criteria:
- The executor can run against web/macos without risk of infinite looping.
