package trace

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
)

func TestCaptureSubprocess_WritesTraceAndEvidence(t *testing.T) {
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

	loggerBuffer := new(bytes.Buffer)
	recorder := NewRecorder(store, NewLogger(loggerBuffer, true))

	trace, err := recorder.CaptureSubprocess(context.Background(), CaptureRequest{
		RunID:      runID,
		TaskID:     "task_1",
		Runner:     "stub",
		Command:    []string{"sh", "-c", "cat >/dev/null; echo ok; echo err >&2"},
		InputJSON:  []byte(`{"hello":"world"}`),
		Timeout:    0,
		WorkingDir: "",
	})
	if err != nil {
		t.Fatalf("CaptureSubprocess() error = %v", err)
	}

	if trace.ExitCode != 0 {
		t.Fatalf("trace.ExitCode = %d, want 0", trace.ExitCode)
	}

	for _, path := range []string{trace.StdinPath, trace.StdoutPath, trace.StderrPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("os.Stat(%s) error = %v", path, err)
		}
	}

	evidenceRows, err := store.EvidenceList(context.Background(), blackboard.EvidenceFilter{
		RunID: runID,
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("EvidenceList() error = %v", err)
	}
	if len(evidenceRows) != 3 {
		t.Fatalf("len(evidenceRows) = %d, want 3", len(evidenceRows))
	}
	if !containsKind(evidenceRows, model.EvidenceKindTrace) {
		t.Fatal("expected trace evidence entry")
	}

	logOutput := loggerBuffer.String()
	if !strings.Contains(logOutput, `"trace_id"`) {
		t.Fatalf("logger output missing trace id: %s", logOutput)
	}
}

func containsKind(evidence []model.Evidence, kind model.EvidenceKind) bool {
	for _, item := range evidence {
		if item.Kind == kind {
			return true
		}
	}
	return false
}
