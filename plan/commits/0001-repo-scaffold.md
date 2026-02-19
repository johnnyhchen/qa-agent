# 0001 - Repo Scaffold (Go Module, Lint, CI Skeleton)

Commit message:
- `chore: scaffold qa-agent repo`

Goal:
- Create a minimal Go project skeleton that can compile, run, and host the rest of the system.

Scope:
- Go module init.
- Basic `cmd/qa-agent` entrypoint.
- Repo hygiene (gitignore, formatting, lint config placeholders).
- Add docs folder and include the design doc.

Planned changes:
- Create `go.mod` (pick final module path).
- Create `cmd/qa-agent/main.go` that prints version/help.
- Add `internal/` or `pkg/` directory structure (empty packages are fine).
- Add `.gitignore` for build outputs, run artifacts, local DB files.
- Add `docs/validation-design-doc.md` (copy from the design doc source).

Verification:
- `go test ./...` passes (even if there are no tests yet).
- `go run ./cmd/qa-agent --help` works.

Exit criteria:
- A clean baseline commit that other commits can build on without churn.
