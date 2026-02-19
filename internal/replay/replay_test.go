package replay

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"qa-agent/internal/model"
)

func TestReplayTaskBestEffort(t *testing.T) {
	runDir := t.TempDir()
	traceDir := filepath.Join(runDir, "artifacts", "traces", "trace_original")
	if err := os.MkdirAll(traceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(traceDir) error = %v", err)
	}

	runnerPath := writeFakeReplayRunner(t)
	originalOutput := filepath.Join(traceDir, "original-output.json")
	originalTrace := model.ActionTrace{
		SchemaVersion: model.CurrentSchemaVersion,
		TraceID:       "trace_original",
		RunID:         "run_1",
		TaskID:        "task_1",
		Runner:        "fake",
		Command:       []string{runnerPath, "--output", originalOutput},
		StdoutPath:    filepath.Join(traceDir, "stdout.log"),
		StderrPath:    filepath.Join(traceDir, "stderr.log"),
		ExitCode:      0,
		StartedAt:     time.Now().UTC().Add(-time.Second),
		FinishedAt:    time.Now().UTC(),
	}
	raw, err := json.MarshalIndent(originalTrace, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(trace) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(traceDir, "action-trace.json"), raw, 0o644); err != nil {
		t.Fatalf("WriteFile(action-trace.json) error = %v", err)
	}

	result, err := ReplayTask(context.Background(), runDir, "task_1", 5*time.Second)
	if err != nil {
		t.Fatalf("ReplayTask() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("result.ExitCode = %d, want 0", result.ExitCode)
	}
	if _, err := os.Stat(result.TracePath); err != nil {
		t.Fatalf("replay trace missing: %v", err)
	}
	if _, err := os.Stat(result.StdoutPath); err != nil {
		t.Fatalf("replay stdout missing: %v", err)
	}
}

func TestListTraces(t *testing.T) {
	runDir := t.TempDir()
	traceDir := filepath.Join(runDir, "artifacts", "traces", "trace1")
	if err := os.MkdirAll(traceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(traceDir) error = %v", err)
	}
	trace := model.ActionTrace{
		SchemaVersion: model.CurrentSchemaVersion,
		TraceID:       "trace1",
		RunID:         "run_1",
		TaskID:        "task_1",
		Runner:        "fake",
		Command:       []string{"echo"},
		ExitCode:      0,
		StartedAt:     time.Now().UTC().Add(-time.Second),
		FinishedAt:    time.Now().UTC(),
	}
	raw, _ := json.Marshal(trace)
	if err := os.WriteFile(filepath.Join(traceDir, "action-trace.json"), raw, 0o644); err != nil {
		t.Fatalf("WriteFile(action-trace.json) error = %v", err)
	}

	traces, err := ListTraces(runDir)
	if err != nil {
		t.Fatalf("ListTraces() error = %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("len(traces) = %d, want 1", len(traces))
	}
	if traces[0].TaskID != "task_1" {
		t.Fatalf("trace task id = %s, want task_1", traces[0].TaskID)
	}
}

func writeFakeReplayRunner(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-replay-runner.sh")
	script := `#!/bin/sh
output=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output) output="$2"; shift 2;;
    *) shift;;
  esac
done
if [ -z "$output" ]; then
  echo "missing output path" >&2
  exit 2
fi
echo '{"outcome":"pass","summary":"replayed"}' > "$output"
echo "replay stdout"
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake replay runner) error = %v", err)
	}
	return path
}
