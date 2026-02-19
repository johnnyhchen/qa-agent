package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	judgeagent "qa-agent/internal/agents/judge"
	planneragent "qa-agent/internal/agents/planner"
	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
	"qa-agent/internal/orchestrator"
	"qa-agent/internal/report"
	"qa-agent/internal/runner"
	"qa-agent/internal/sandbox"
)

type smokePlanner struct {
	output planneragent.Output
}

func (s smokePlanner) Plan(_ context.Context, _ string, _ string, _ []model.Surface) (planneragent.Output, error) {
	return s.output, nil
}

type smokeExecutor struct{}

func (s smokeExecutor) Run(_ context.Context, task model.Task, _ sandbox.Sandbox, artifactDir string) (runner.Result, error) {
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return runner.Result{}, err
	}
	evidencePath := filepath.Join(artifactDir, "evidence.log")
	if err := os.WriteFile(evidencePath, []byte("pass"), 0o644); err != nil {
		return runner.Result{}, err
	}
	return runner.Result{
		Outcome:        model.RunOutcomePass,
		Summary:        "smoke pass",
		EvidenceFiles:  []string{evidencePath},
		ActionTraceRef: "trace_smoke",
	}, nil
}

func TestOrchestratorSmokeWithReportArtifacts(t *testing.T) {
	store, err := blackboard.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	runID := "run_smoke"
	plannerOutput := planneragent.Output{
		FeatureSpec: model.FeatureSpec{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Description:   "feature works",
			AcceptanceCriteria: []model.AcceptanceCriterion{
				{ID: "ac_1", Text: "criterion"},
			},
			Surfaces: []model.Surface{model.SurfaceWeb},
		},
		TestPlan: model.TestPlan{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Journeys:      []string{"journey_1"},
			Assertions:    []string{"criterion"},
		},
		Tasks: []model.Task{
			{
				SchemaVersion: model.CurrentSchemaVersion,
				TaskID:        "task_smoke_1",
				RunID:         runID,
				Surface:       model.SurfaceWeb,
				Kind:          model.TaskKindProof,
				Priority:      model.PriorityP1,
				Status:        model.TaskStatusQueued,
				DedupeKey:     "smoke:1",
				MaxAttempts:   1,
				CreatedBy:     "planner",
				AcceptanceCriteriaIDs: []string{
					"ac_1",
				},
			},
		},
	}

	orch := orchestrator.New(
		store,
		smokePlanner{output: plannerOutput},
		judgeagent.New(),
		map[model.Surface]orchestrator.Executor{
			model.SurfaceWeb: smokeExecutor{},
		},
		orchestrator.Budget{
			MaxQueuedTasks:          20,
			MaxNewTasksPerJudgeTurn: 5,
			MaxJudgeTurns:           2,
		},
	)

	verdict, err := orch.Run(context.Background(), orchestrator.Request{
		RunID:       runID,
		Description: "feature works",
		Surfaces:    []model.Surface{model.SurfaceWeb},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if verdict.Status != model.VerdictPass {
		t.Fatalf("verdict status = %s, want %s", verdict.Status, model.VerdictPass)
	}

	runDir := store.RunDir(runID)
	reportPath, manifestPath, err := report.NewGenerator().Write(runID, runDir)
	if err != nil {
		t.Fatalf("Write(report) error = %v", err)
	}
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatalf("report missing: %v", err)
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
}
