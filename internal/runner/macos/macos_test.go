package macos

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
	"qa-agent/internal/sandbox"
	"qa-agent/internal/trace"
)

func TestAdapterSmoke(t *testing.T) {
	store, err := blackboard.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	runID := "run_macos"
	if err := store.CreateValidationRun(context.Background(), blackboard.ValidationRun{
		ID:              runID,
		RetentionPolicy: blackboard.RetentionKeepAll,
	}); err != nil {
		t.Fatalf("CreateValidationRun() error = %v", err)
	}

	binary := writeFakeComputerUse(t)
	recorder := trace.NewRecorder(store, trace.NewLogger(new(bytes.Buffer), true))
	adapter := NewAdapter(binary, 2*time.Minute, 100, recorder)
	if err := adapter.Doctor(context.Background()); err != nil {
		t.Fatalf("Doctor() error = %v", err)
	}

	task := model.Task{
		SchemaVersion: model.CurrentSchemaVersion,
		TaskID:        "task_macos_1",
		RunID:         runID,
		Surface:       model.SurfaceMacOS,
		Kind:          model.TaskKindProof,
		Priority:      model.PriorityP1,
		Status:        model.TaskStatusQueued,
		DedupeKey:     "macos-launch",
		MaxAttempts:   1,
		Payload: map[string]any{
			"app_bundle_id":         "com.example.app",
			"steps":                 []any{"launch app"},
			"assertions":            []any{"window appears"},
			"max_steps":             float64(10),
			"max_wall_time_seconds": float64(60),
		},
	}

	result, err := adapter.Run(context.Background(), task, sandbox.Sandbox{
		ID:    "sandbox_macos",
		RunID: runID,
	}, filepath.Join(store.ArtifactDir(runID), "macos-task"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Outcome != model.RunOutcomePass {
		t.Fatalf("result.Outcome = %s, want %s", result.Outcome, model.RunOutcomePass)
	}
	if len(result.StabilityHints) == 0 {
		t.Fatal("expected stability hints for macos surface")
	}
}

func TestAdapterBudgetValidation(t *testing.T) {
	adapter := NewAdapter("fake", 10*time.Second, 5, &trace.Recorder{})
	_, err := adapter.Run(context.Background(), model.Task{
		Payload: map[string]any{
			"app_bundle_id":         "com.example.app",
			"steps":                 []any{"launch app"},
			"assertions":            []any{"window appears"},
			"max_steps":             float64(10),
			"max_wall_time_seconds": float64(10),
		},
	}, sandbox.Sandbox{}, t.TempDir())
	if err == nil {
		t.Fatal("Run() expected budget validation error")
	}
}

func writeFakeComputerUse(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-ai-computer-use.sh")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "fake-ai-computer-use 0.0.1"
  exit 0
fi
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
echo '{"outcome":"pass","summary":"macos ok","evidence_files":["screen.png","runner.log"]}' > "$output"
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake computer use) error = %v", err)
	}
	return path
}
