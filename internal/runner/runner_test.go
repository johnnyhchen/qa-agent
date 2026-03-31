package runner

import (
	"bytes"
	"context"
	"fmt"
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

// TestFakeRunnerHelper is the helper process that acts as a fake runner binary.
// It is invoked as a subprocess by the real test below.
func TestFakeRunnerHelper(t *testing.T) {
	if os.Getenv("QA_AGENT_TEST_HELPER") != "1" {
		return
	}
	var outputPath string
	args := os.Args
	for i, arg := range args {
		if arg == "--output" && i+1 < len(args) {
			outputPath = args[i+1]
		}
	}
	if outputPath == "" {
		fmt.Fprintln(os.Stderr, "missing output path")
		os.Exit(2)
	}
	payload := `{"outcome":"pass","summary":"ok","evidence_files":["artifact.log"],"stability_hints":["stable"]}`
	if err := os.WriteFile(outputPath, []byte(payload), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

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

	recorder := trace.NewRecorder(store, trace.NewLogger(new(bytes.Buffer), true))
	subprocessRunner := &SubprocessRunner{
		Name:   "fake",
		Binary: os.Args[0],
		BaseArgs: []string{
			"-test.run=TestFakeRunnerHelper", "--",
		},
		Timeout:       10 * time.Second,
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

	// Set the env var so the helper process knows to act as a runner.
	t.Setenv("QA_AGENT_TEST_HELPER", "1")

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
