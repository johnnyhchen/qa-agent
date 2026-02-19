# 0019 - Reporting + Bundling (Evidence bundle, verdict summary)

Commit message:
- `feat: add reporting and evidence bundle generation`

Goal:
- Emit a human-readable verdict report and a portable evidence bundle for audit/review.

Scope:
- Report generator.
- Bundle manifest.
- Redaction/truncation enforcement at packaging time.

Planned changes:
- Add `internal/report/` generating a markdown or JSON report and a machine-readable manifest for the evidence bundle.
- Add `qa-agent report --run-id ...` that writes the report into the run artifact directory.
- Add `qa-agent bundle --run-id ...` that packages artifacts into a zip/tar.

Verification:
- Unit test: manifest references only files that exist.
- Golden test: report output contains required sections and stable ordering.

Exit criteria:
- A single run can be reviewed offline without re-executing automation.
