package judge

import (
	"context"
	"testing"
	"time"

	"qa-agent/internal/model"
)

func TestValidateOutputMutualExclusivity(t *testing.T) {
	err := ValidateOutput(Output{})
	if err == nil {
		t.Fatal("ValidateOutput() expected error for missing next_tasks and verdict")
	}

	err = ValidateOutput(Output{
		NextTasks: []model.Task{
			{
				SchemaVersion: model.CurrentSchemaVersion,
				TaskID:        "task_1",
				RunID:         "run_1",
				Surface:       model.SurfaceWeb,
				Kind:          model.TaskKindProof,
				Priority:      model.PriorityP1,
				Status:        model.TaskStatusQueued,
				DedupeKey:     "d1",
				MaxAttempts:   1,
			},
		},
		Verdict: &model.Verdict{
			SchemaVersion: model.CurrentSchemaVersion,
			VerdictID:     "v1",
			RunID:         "run_1",
			Status:        model.VerdictPass,
			Reasons:       []string{"ok"},
			Coverage:      map[string][]string{"ac_1": {"ev_1"}},
		},
	})
	if err == nil {
		t.Fatal("ValidateOutput() expected xor error")
	}
}

func TestEvaluateOfflineEvidenceSet(t *testing.T) {
	agent := New()
	input := Input{
		RunID: "run_1",
		FeatureSpec: model.FeatureSpec{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         "run_1",
			Description:   "login works",
			AcceptanceCriteria: []model.AcceptanceCriterion{
				{ID: "ac_1", Text: "user logs in"},
			},
			Surfaces: []model.Surface{model.SurfaceWeb},
		},
		Tasks: []model.Task{
			{
				SchemaVersion: model.CurrentSchemaVersion,
				TaskID:        "task_1",
				RunID:         "run_1",
				Surface:       model.SurfaceWeb,
				Kind:          model.TaskKindProof,
				Priority:      model.PriorityP1,
				Status:        model.TaskStatusPassed,
				DedupeKey:     "d1",
				MaxAttempts:   2,
				AcceptanceCriteriaIDs: []string{
					"ac_1",
				},
			},
		},
		Runs: []model.Run{
			{
				SchemaVersion: model.CurrentSchemaVersion,
				RunID:         "run_1",
				TaskID:        "task_1",
				Outcome:       model.RunOutcomePass,
				TraceRef:      "trace_1",
				StartedAt:     time.Now().UTC().Add(-time.Second),
				FinishedAt:    time.Now().UTC(),
			},
		},
		Evidence: []model.Evidence{
			{
				SchemaVersion: model.CurrentSchemaVersion,
				EvidenceID:    "ev_1",
				RunID:         "run_1",
				Kind:          model.EvidenceKindTrace,
				Path:          "trace.json",
				Bytes:         10,
				CreatedAt:     time.Now().UTC(),
			},
		},
		MaxRounds: 2,
	}

	output, err := agent.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if err := ValidateOutput(output); err != nil {
		t.Fatalf("ValidateOutput() error = %v", err)
	}
	if output.Verdict == nil {
		t.Fatal("expected final verdict")
	}
	if output.Verdict.Status != model.VerdictPass {
		t.Fatalf("verdict status = %s, want %s", output.Verdict.Status, model.VerdictPass)
	}
}

func TestEvaluateFlakyCriterionRequestsMoreProof(t *testing.T) {
	agent := New()
	input := Input{
		RunID: "run_flaky",
		FeatureSpec: model.FeatureSpec{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         "run_flaky",
			Description:   "flaky criterion",
			AcceptanceCriteria: []model.AcceptanceCriterion{
				{ID: "ac_1", Text: "criterion should pass"},
			},
			Surfaces: []model.Surface{model.SurfaceWeb},
		},
		Tasks: []model.Task{
			{
				SchemaVersion: model.CurrentSchemaVersion,
				TaskID:        "task_1",
				RunID:         "run_flaky",
				Surface:       model.SurfaceWeb,
				Kind:          model.TaskKindProof,
				Priority:      model.PriorityP1,
				Status:        model.TaskStatusPassed,
				DedupeKey:     "d1",
				MaxAttempts:   2,
				AcceptanceCriteriaIDs: []string{
					"ac_1",
				},
			},
		},
		Runs: []model.Run{
			{
				SchemaVersion: model.CurrentSchemaVersion,
				RunID:         "run_flaky",
				TaskID:        "task_1",
				Outcome:       model.RunOutcomePass,
				TraceRef:      "trace_pass",
				StartedAt:     time.Now().UTC().Add(-2 * time.Second),
				FinishedAt:    time.Now().UTC().Add(-time.Second),
			},
			{
				SchemaVersion: model.CurrentSchemaVersion,
				RunID:         "run_flaky",
				TaskID:        "task_1",
				Outcome:       model.RunOutcomeFail,
				TraceRef:      "trace_fail",
				StartedAt:     time.Now().UTC().Add(-time.Second),
				FinishedAt:    time.Now().UTC(),
			},
		},
	}

	output, err := agent.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if output.Verdict != nil && output.Verdict.Status == model.VerdictFail {
		t.Fatal("expected flaky criterion to request more proof instead of failing immediately")
	}
	if len(output.NextTasks) == 0 {
		t.Fatal("expected judge to request follow-up tasks for flaky criterion")
	}
}
