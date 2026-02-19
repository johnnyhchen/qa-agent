# 0008 - Runner Interfaces + Subprocess Contract (Generic CLI Runner)

Commit message:
- `feat: add runner interface and generic subprocess runner adapter`

Goal:
- Standardize how the execution harness invokes surface automation tools as CLIs.

Scope:
- Runner interface.
- JSON in/out contract.
- Subprocess management (timeouts, kill, logs).

Planned changes:
- Add `internal/runner/`.
- Define `Runner` interface: `Run(ctx, task, sandbox, artifactDir) (Result, error)`.
- Define `Result` (outcome, summary, action trace refs, evidence file refs, stability hints).
- Define the CLI contract for external runners.
- CLI input: JSON file containing the task spec and sandbox coordinates.
- CLI output: JSON file containing result and evidence paths, with well-defined exit codes.
- Implement `SubprocessRunner`.
- `SubprocessRunner` writes input JSON, executes the external binary with timeouts, captures stdout/stderr as evidence, parses output JSON, and returns `Result`.

Verification:
- Add a tiny fake runner binary in tests that echoes a valid output JSON.
- Integration test: `SubprocessRunner` executes the fake runner and stores traces.

Exit criteria:
- Web/macOS/iOS runners can plug into the same execution harness without special-case glue.
