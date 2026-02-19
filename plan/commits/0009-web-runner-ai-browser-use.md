# 0009 - Web Runner Adapter (ai-browser-use)

Commit message:
- `feat: add web runner adapter backed by ai-browser-use`

Goal:
- Validate web journeys locally using `ai-browser-use` and persist evidence in a standard envelope.

Scope:
- Web runner adapter using `SubprocessRunner`.
- Evidence capture conventions.
- Minimal web task format.

Planned changes:
- Add `internal/runner/web/` implementing the generic `Runner` interface.
- Define a web task schema subset (URL(s), user steps, assertions, test data).
- Map web task JSON into the `ai-browser-use` invocation contract.
- Normalize evidence outputs.
- Evidence: screenshots.
- Evidence: page reads (DOM or accessibility snapshot).
- Evidence: console logs.
- Evidence: network transcript or HAR when available.
- Add a tool detection/doctor helper to validate `ai-browser-use` is installed and callable.

Verification:
- Add an integration test that runs against a local static site or a tiny local web server.
- Confirm evidence artifacts land in the blackboard artifact store and are referenced from the DB.

Exit criteria:
- The system can execute at least one end-to-end web task and produce evidence.
