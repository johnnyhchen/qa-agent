package processor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
	"qa-agent/internal/runner"
)

func TestProcessNormalizationAndRedaction(t *testing.T) {
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

	artifactDir := filepath.Join(store.ArtifactDir(runID), "raw")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	rawPath := filepath.Join(artifactDir, "http.log")
	rawBody := "authorization: Bearer abc123\ncookie: session=secret\n" + strings.Repeat("x", 1024)
	if err := os.WriteFile(rawPath, []byte(rawBody), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	proc := New(store, 128)
	result, err := proc.Process(context.Background(), ProcessRequest{
		Run: model.Run{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			TaskID:        "task_1",
			Outcome:       model.RunOutcomeFail,
			Summary:       "raw summary",
			StartedAt:     time.Now().UTC().Add(-time.Second),
			FinishedAt:    time.Now().UTC(),
		},
		RunnerResult: runner.Result{
			Outcome:       model.RunOutcomeFail,
			Summary:       "runner failed",
			EvidenceFiles: []string{rawPath},
		},
		ArtifactDir: artifactDir,
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if err := ValidateProcessResult(result); err != nil {
		t.Fatalf("ValidateProcessResult() error = %v", err)
	}
	if len(result.Evidence) != 1 {
		t.Fatalf("len(result.Evidence) = %d, want 1", len(result.Evidence))
	}
	if result.Evidence[0].SummaryFields["truncated"] != "true" {
		t.Fatalf("truncated summary = %q, want true", result.Evidence[0].SummaryFields["truncated"])
	}

	normalizedRaw, err := os.ReadFile(result.Evidence[0].Path)
	if err != nil {
		t.Fatalf("ReadFile(normalized) error = %v", err)
	}
	if strings.Contains(strings.ToLower(string(normalizedRaw)), "bearer abc123") {
		t.Fatal("normalized file still contains unredacted authorization value")
	}
}

func TestProcessRegistersEvidenceRows(t *testing.T) {
	store, err := blackboard.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	runID := "run_2"
	if err := store.CreateValidationRun(context.Background(), blackboard.ValidationRun{
		ID:              runID,
		RetentionPolicy: blackboard.RetentionKeepAll,
	}); err != nil {
		t.Fatalf("CreateValidationRun() error = %v", err)
	}

	artifactDir := filepath.Join(store.ArtifactDir(runID), "runner")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	transcriptPath := filepath.Join(artifactDir, "api-transcript.json")
	if err := os.WriteFile(transcriptPath, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatalf("WriteFile(transcript) error = %v", err)
	}

	proc := New(store, 1024)
	_, err = proc.Process(context.Background(), ProcessRequest{
		Run: model.Run{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			TaskID:        "task_2",
			Outcome:       model.RunOutcomePass,
			StartedAt:     time.Now().UTC().Add(-time.Second),
			FinishedAt:    time.Now().UTC(),
		},
		RunnerResult: runner.Result{
			Outcome:       model.RunOutcomePass,
			Summary:       "api runner ok",
			EvidenceFiles: []string{transcriptPath},
		},
		ArtifactDir: artifactDir,
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	items, err := store.EvidenceList(context.Background(), blackboard.EvidenceFilter{
		RunID: runID,
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("EvidenceList() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(EvidenceList) = %d, want 1", len(items))
	}
	if items[0].Kind != model.EvidenceKindTranscript {
		t.Fatalf("evidence kind = %s, want %s", items[0].Kind, model.EvidenceKindTranscript)
	}
}

func TestProcessTruncationPreservesUTF8(t *testing.T) {
	store, err := blackboard.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	runID := "run_utf8"
	if err := store.CreateValidationRun(context.Background(), blackboard.ValidationRun{
		ID:              runID,
		RetentionPolicy: blackboard.RetentionKeepAll,
	}); err != nil {
		t.Fatalf("CreateValidationRun() error = %v", err)
	}

	artifactDir := filepath.Join(store.ArtifactDir(runID), "raw")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	rawPath := filepath.Join(artifactDir, "emoji.log")
	rawBody := strings.Repeat("x", 250) + strings.Repeat("\U0001F600", 32)
	if err := os.WriteFile(rawPath, []byte(rawBody), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	proc := New(store, 256)
	result, err := proc.Process(context.Background(), ProcessRequest{
		Run: model.Run{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			TaskID:        "task_utf8",
			Outcome:       model.RunOutcomePass,
			StartedAt:     time.Now().UTC().Add(-time.Second),
			FinishedAt:    time.Now().UTC(),
		},
		RunnerResult: runner.Result{
			Outcome:       model.RunOutcomePass,
			Summary:       "utf8",
			EvidenceFiles: []string{rawPath},
		},
		ArtifactDir: artifactDir,
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	if len(result.Evidence) != 1 {
		t.Fatalf("len(result.Evidence) = %d, want 1", len(result.Evidence))
	}
	normalizedRaw, err := os.ReadFile(result.Evidence[0].Path)
	if err != nil {
		t.Fatalf("ReadFile(normalized) error = %v", err)
	}
	if !utf8.Valid(normalizedRaw) {
		t.Fatal("expected normalized truncated content to remain valid utf-8")
	}
}
