package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
	"qa-agent/internal/sandbox"
	"qa-agent/internal/trace"
)

func TestSubprocessRunnerExecutesFakeRunnerAndStoresTrace(t *testing.T) {
	store, err := blackboard.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	runID := "run_1"
	if err := store.CreateValidationRun(context.Background(), blackboard.ValidationRun{
		ID:              runID,
		RetentionPolicy: blackboard.RetentionKeepAll,
	}); err != nil {
		t.Fatalf("CreateValidationRun() error = %v", err)
	}

	fakeRunner := writeFakeRunner(t)
	recorder := trace.NewRecorder(store, trace.NewLogger(new(bytes.Buffer), true))
	subprocessRunner := &SubprocessRunner{
		Name:          "fake",
		Binary:        fakeRunner,
		BaseArgs:      nil,
		Timeout:       2 * time.Second,
		TraceRecorder: recorder,
	}

	task := model.Task{
		SchemaVersion: model.CurrentSchemaVersion,
		TaskID:        "task_1",
		RunID:         runID,
		Surface:       model.SurfaceWeb,
		Kind:          model.TaskKindProof,
		Priority:      model.PriorityP1,
		Status:        model.TaskStatusQueued,
		DedupeKey:     "dedupe_1",
		MaxAttempts:   1,
	}
	artifactDir := filepath.Join(store.ArtifactDir(runID), "runner")

	result, err := subprocessRunner.Run(context.Background(), task, sandbox.Sandbox{
		ID:    "sandbox_1",
		RunID: runID,
	}, artifactDir)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Outcome != model.RunOutcomePass {
		t.Fatalf("result.Outcome = %s, want %s", result.Outcome, model.RunOutcomePass)
	}
	if result.ActionTraceRef == "" {
		t.Fatal("result.ActionTraceRef is empty")
	}
	if len(result.EvidenceFiles) != 1 {
		t.Fatalf("len(result.EvidenceFiles) = %d, want 1", len(result.EvidenceFiles))
	}

	evidenceRows, err := store.EvidenceList(context.Background(), blackboard.EvidenceFilter{
		RunID: runID,
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("EvidenceList() error = %v", err)
	}
	if len(evidenceRows) < 3 {
		t.Fatalf("expected >=3 evidence entries, got %d", len(evidenceRows))
	}

	outputPath := filepath.Join(artifactDir, "task_1-output.json")
	outputRaw, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile(output) error = %v", err)
	}
	if !strings.Contains(string(outputRaw), `"outcome":"pass"`) {
		t.Fatalf("output json did not contain pass outcome: %s", string(outputRaw))
	}
}

func writeFakeRunner(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-runner.sh")
	script := `#!/bin/sh
input=""
output=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --input) input="$2"; shift 2;;
    --output) output="$2"; shift 2;;
    *) shift;;
  esac
done
if [ -z "$output" ]; then
  echo "missing output path" >&2
  exit 2
fi
echo '{"outcome":"pass","summary":"ok","evidence_files":["artifact.log"],"stability_hints":["stable"]}' > "$output"
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake runner) error = %v", err)
	}
	return path
}
