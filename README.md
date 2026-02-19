# qa-agent

`qa-agent` validates feature behavior in local environments and records evidence for audit.

## What It Does

- Accepts a feature description and target surfaces.
- Plans and executes validation tasks with deterministic queueing.
- Stores run metadata, traces, and artifacts locally.
- Produces a verdict (`pass`, `fail`, `cannot_verify`) and report bundle.

## Local-Only Assumptions

- Runs only against local stacks and fixtures.
- No production/staging targeting.
- Evidence is written to local artifact directories.

## Surface Support

- Supported: `web`, `api`
- Experimental: `macos`, `ios`

## Quick Start

Run a new validation shell:

```bash
go run ./cmd/qa-agent run \
  --feature "User can sign in with valid credentials." \
  --surfaces web,api
```

Generate report and bundle:

```bash
go run ./cmd/qa-agent report --run-id <run_id>
go run ./cmd/qa-agent bundle --run-id <run_id>
```

Inspect traces and replay one task:

```bash
go run ./cmd/qa-agent trace --run-id <run_id>
go run ./cmd/qa-agent replay --run-id <run_id> --task-id <task_id>
```

## Storage Layout

Default output root: `.qa-agent/runs`.

Each run contains:

- `run.json`
- `db.sqlite` (blackboard metadata)
- `artifacts/` (traces, logs, evidence, replays)
- `report.md` and `manifest.json` after report generation

## Troubleshooting

See `docs/troubleshooting.md` for common setup and environment issues.

