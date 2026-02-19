# 0014 - Evidence Processor (Normalization + Summaries)

Commit message:
- `feat: add deterministic evidence processor`

Goal:
- Normalize heterogeneous runner outputs into a consistent envelope the judge can query without reading raw blobs.

Scope:
- Evidence normalization.
- Summary field extraction.
- Redaction/truncation policy (basic).

Planned changes:
- Add `internal/evidence/processor/` that takes runner results and emits `Run.summary`, `Evidence.summary_fields`, and evidence file registration into the blackboard.
- Add truncation rules for oversized logs and transcripts.
- Add redaction hooks for secrets (headers, tokens, cookies) with a conservative default.

Verification:
- Unit tests for normalization and redaction.
- Integration test: run fake runner producing raw artifacts and ensure evidence processor registers them correctly.

Exit criteria:
- The judge agent can operate mostly on summaries and selectively drill into raw evidence.
