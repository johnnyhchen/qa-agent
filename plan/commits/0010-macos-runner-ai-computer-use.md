# 0010 - macOS Runner Adapter (ai-computer-use)

Commit message:
- `feat: add macos runner adapter backed by ai-computer-use`

Goal:
- Validate macOS desktop app journeys locally using `ai-computer-use` and persist evidence.

Scope:
- macOS runner adapter using `SubprocessRunner`.
- Evidence conventions for desktop UI.
- Explicitly budgeted (LLM-driven) execution.

Planned changes:
- Add `internal/runner/macos/` implementing the generic `Runner` interface.
- Define a macOS task schema subset (app launch instructions, UI steps, assertions).
- Map task JSON into the `ai-computer-use` invocation contract.
- Normalize evidence outputs.
- Evidence: screenshots and optional screen recording.
- Evidence: runner transcript and stdout/stderr.
- Evidence: app logs and crash dumps when available.
- Add strict budgets for this runner (max steps, max wall time) and classify runner non-determinism as a stability concern.

Verification:
- Smoke test: run a trivial task against a simple app (or a fixture app) and capture a screenshot.

Exit criteria:
- The system can execute at least one end-to-end desktop task and produce evidence, even if this surface remains experimental.
