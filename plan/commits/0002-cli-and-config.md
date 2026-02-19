# 0002 - CLI and Config (run, replay, report)

Commit message:
- `feat: add CLI commands and config loading`

Goal:
- Provide a stable user entrypoint and configuration model for local runs.

Scope:
- CLI subcommands.
- Config file and env var parsing.
- No orchestration logic yet.

Planned changes:
- Add `run` command. Inputs: feature description text, surface(s), budgets, output directory. Output: prints the created `run_id` and where artifacts go.
- Add `replay` command (stub): accepts `--run-id` and prints “not implemented” but validates run exists.
- Add `report` command (stub): loads a run and emits a placeholder report.
- Add config struct `internal/config/config.go`. Precedence: CLI flags > env vars > config file defaults.
- Add external tool location knobs: `AI_BROWSER_USE_BIN`, `AI_COMPUTER_USE_BIN`, `DOCKER_BIN`.

Verification:
- `qa-agent run --help` shows required flags.
- Config precedence tests (unit tests for config parsing only).

Exit criteria:
- The CLI surface is stable enough that later commits only fill in implementations.
