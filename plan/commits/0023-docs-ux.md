# 0023 - Docs + UX Polish (README, examples, troubleshooting)

Commit message:
- `docs: add README, examples, and troubleshooting`

Goal:
- Make the project usable by someone other than the author.

Scope:
- README and examples.
- Troubleshooting for local-only constraints.
- Surface maturity notes.

Planned changes:
- Add `README.md` describing what the tool does, local-only setup expectations, supported vs experimental surfaces, and how evidence and verdicts are stored.
- Add `docs/examples/` with example feature descriptions and expected outputs (shape only).
- Add troubleshooting notes for Docker issues, browser extension connectivity, and macOS automation permissions.

Verification:
- `qa-agent --help` matches README examples.

Exit criteria:
- A new engineer can run a small validation end-to-end with minimal guidance.
