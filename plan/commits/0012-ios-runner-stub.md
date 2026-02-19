# 0012 - iOS Runner Stub (Simulator Hook Points, Experimental)

Commit message:
- `feat: add ios runner stub and integration points`

Goal:
- Establish the iOS runner interface and evidence conventions without over-claiming feasibility.

Scope:
- iOS runner stub.
- Explicit `cannot_verify` behavior when not configured.

Planned changes:
- Add `internal/runner/ios/` implementing the generic `Runner` interface.
- Define iOS task schema subset (app bundle id, simulator device profile, steps, assertions).
- Default behavior is explicit and safe.
- If iOS tooling is not present or not configured, return `blocked` with diagnostics and allow the orchestrator to surface `cannot_verify`.
- Document the future direction: XCUITest-style driver, simulator logs, screenshots.

Verification:
- Unit test for the stub’s error classification and diagnostics.

Exit criteria:
- The orchestrator can treat iOS as an experimental surface without breaking the core loop.
