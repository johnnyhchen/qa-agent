package ios

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"qa-agent/internal/model"
	"qa-agent/internal/sandbox"
)

func TestAdapterReturnsBlockedWithDiagnosticsWhenToolingMissing(t *testing.T) {
	adapter := NewAdapter("definitely-not-installed-xcrun")
	artifactDir := t.TempDir()
	task := model.Task{
		SchemaVersion: model.CurrentSchemaVersion,
		TaskID:        "task_ios",
		RunID:         "run_1",
		Surface:       model.SurfaceIOS,
		Kind:          model.TaskKindProof,
		Priority:      model.PriorityP2,
		Status:        model.TaskStatusQueued,
		DedupeKey:     "ios-login",
		MaxAttempts:   1,
		Payload: map[string]any{
			"app_bundle_id":  "com.example.app",
			"device_profile": "iPhone 15",
			"steps":          []any{"launch app"},
			"assertions":     []any{"home screen visible"},
		},
	}

	result, err := adapter.Run(context.Background(), task, sandbox.Sandbox{}, artifactDir)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Outcome != model.RunOutcomeBlocked {
		t.Fatalf("result.Outcome = %s, want %s", result.Outcome, model.RunOutcomeBlocked)
	}
	if !strings.Contains(strings.ToLower(result.Summary), "not configured") {
		t.Fatalf("result.Summary = %q, expected not configured message", result.Summary)
	}
	diagPath := filepath.Join(artifactDir, "ios-diagnostics.json")
	if len(result.EvidenceFiles) != 1 || result.EvidenceFiles[0] != diagPath {
		t.Fatalf("result.EvidenceFiles = %#v, want [%s]", result.EvidenceFiles, diagPath)
	}
}

func TestParseTaskSpecValidation(t *testing.T) {
	_, err := ParseTaskSpec(model.Task{})
	if err == nil {
		t.Fatal("ParseTaskSpec() expected validation error")
	}
}
