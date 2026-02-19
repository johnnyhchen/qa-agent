package planner

import (
	"context"
	"testing"

	"qa-agent/internal/agents/runtime"
	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
)

func TestValidateOutputRejectsMalformed(t *testing.T) {
	err := ValidateOutput(Output{})
	if err == nil {
		t.Fatal("ValidateOutput() expected error for empty output")
	}
}

func TestPlanGoldenShape(t *testing.T) {
	store, err := blackboard.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	runID := "run_plan"
	if err := store.CreateValidationRun(context.Background(), blackboard.ValidationRun{
		ID:              runID,
		RetentionPolicy: blackboard.RetentionKeepAll,
	}); err != nil {
		t.Fatalf("CreateValidationRun() error = %v", err)
	}

	agentRuntime := runtime.New(store, runtime.NewToolRegistry(), nil, runtime.Config{
		TokenCap:   20000,
		CostCapUSD: 1,
	})
	plannerAgent := New(agentRuntime, store)

	output, err := plannerAgent.Plan(context.Background(), runID, "User can login. Failed login shows error.", []model.Surface{model.SurfaceWeb, model.SurfaceAPI})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if err := ValidateOutput(output); err != nil {
		t.Fatalf("ValidateOutput() error = %v", err)
	}
	if len(output.FeatureSpec.AcceptanceCriteria) < 2 {
		t.Fatalf("criteria count = %d, want >=2", len(output.FeatureSpec.AcceptanceCriteria))
	}
	if len(output.Tasks) != len(output.FeatureSpec.AcceptanceCriteria)*2 {
		t.Fatalf("task count = %d, want %d", len(output.Tasks), len(output.FeatureSpec.AcceptanceCriteria)*2)
	}

	for _, task := range output.Tasks {
		if task.Priority == "" || task.DedupeKey == "" {
			t.Fatalf("task missing priority or dedupe key: %+v", task)
		}
	}
}
