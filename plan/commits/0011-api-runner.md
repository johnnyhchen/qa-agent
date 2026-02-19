# 0011 - API Runner (HTTP + gRPC Transcript Evidence)

Commit message:
- `feat: add deterministic api runner with transcript evidence`

Goal:
- Validate API acceptance criteria locally with deterministic, replayable evidence.

Scope:
- HTTP runner.
- gRPC runner (basic).
- Transcript evidence format.

Planned changes:
- Add `internal/runner/api/` implementing the generic `Runner` interface.
- Define API task schema subset: requests, auth setup, sequencing, and assertions.
- Record evidence as a structured transcript.
- Transcript includes request metadata, headers (with redaction), and body.
- Transcript includes response status, headers, and body.
- Transcript includes timing and retry metadata.
- Optionally ingest server-side logs from the sandbox provider as correlated evidence.

Verification:
- Integration test against a tiny local HTTP server.
- If gRPC is included, integration test against a tiny local gRPC server.

Exit criteria:
- API validation is reliable enough to serve as a baseline surface for the whole system.
