# 0007 - Sandbox Interface + Local Stack Provider (Docker)

Commit message:
- `feat: add sandbox interface and docker-based local environment provider`

Goal:
- Give the execution harness a uniform way to create and destroy isolated local environments.

Scope:
- Sandbox abstraction.
- Docker-based sandbox provider for local stacks.
- Environment snapshot capture (metadata only).

Planned changes:
- Add `internal/sandbox/` with a `Manager` interface: `Create(ctx)`, `Destroy(ctx, id)`, and `Exec(ctx, id, cmd)`.
- Define `Sandbox` metadata: id, workspace dir, container IDs, ports, environment vars.
- Implement a Docker provider.
- Docker provider brings up a compose stack (or minimal containers) for a run.
- Docker provider tears down the stack and collects logs.
- Docker provider captures image digests, container IDs, and key config as an environment snapshot artifact.
- Add timeouts and hard-kill behavior for stuck Docker operations.

Verification:
- Integration smoke test with a tiny docker-compose fixture (e.g., a hello HTTP service).
- Confirm logs and environment snapshot are stored as evidence artifacts.

Exit criteria:
- Later runner commits can depend on `sandbox.Manager` without committing to one backend.
