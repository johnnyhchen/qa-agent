package runtime

import (
	"context"
	"testing"
	"time"

	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
)

func TestToolAllowlistEnforced(t *testing.T) {
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

	registry := NewToolRegistry()
	runtime := New(store, registry, nil, Config{})
	_, err = runtime.RunTurn(context.Background(), TurnRequest{
		RunID:     runID,
		AgentName: "planner",
		Prompt:    "hello",
		ToolCalls: []ToolCall{
			{Name: "not-allowed", Args: map[string]any{}},
		},
	})
	if err == nil {
		t.Fatal("RunTurn() expected allowlist error")
	}
}

func TestRunTurnSmokeWritesArtifacts(t *testing.T) {
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

	registry := NewToolRegistry()
	registry.Register("blackboard.write_note", func(ctx context.Context, runID string, args map[string]any) (ToolResult, error) {
		note := "note"
		if value, ok := args["note"].(string); ok {
			note = value
		}
		err := store.CreateEvidence(ctx, model.Evidence{
			SchemaVersion: model.CurrentSchemaVersion,
			EvidenceID:    "note_" + runID,
			RunID:         runID,
			Kind:          model.EvidenceKindLog,
			Path:          note,
			MIME:          "text/plain",
			Bytes:         int64(len(note)),
			CreatedAt:     time.Now().UTC(),
		})
		if err != nil {
			return ToolResult{}, err
		}
		return ToolResult{Output: map[string]any{"written": true}}, nil
	})

	runtime := New(store, registry, nil, Config{
		TokenCap:   5000,
		CostCapUSD: 1,
	})
	result, err := runtime.RunTurn(context.Background(), TurnRequest{
		RunID:     runID,
		AgentName: "planner",
		Prompt:    "create initial plan",
		ToolCalls: []ToolCall{
			{Name: "blackboard.write_note", Args: map[string]any{"note": "hello"}},
		},
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if result.Output == "" {
		t.Fatal("RunTurn() output should not be empty")
	}

	evidenceRows, err := store.EvidenceList(context.Background(), blackboard.EvidenceFilter{
		RunID: runID,
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("EvidenceList() error = %v", err)
	}
	if len(evidenceRows) < 4 {
		t.Fatalf("expected at least 4 evidence rows, got %d", len(evidenceRows))
	}
}
