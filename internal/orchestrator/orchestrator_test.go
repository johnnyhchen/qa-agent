package orchestrator

import (
	"context"
	"sync"
	"testing"

	judgeagent "qa-agent/internal/agents/judge"
	planneragent "qa-agent/internal/agents/planner"
	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
	"qa-agent/internal/runner"
	"qa-agent/internal/sandbox"
)

type stubPlanner struct {
	output planneragent.Output
}

func (s stubPlanner) Plan(_ context.Context, _ string, _ string, _ []model.Surface) (planneragent.Output, error) {
	return s.output, nil
}

type fakeExecutor struct {
	outcome model.RunOutcome
}

func (f fakeExecutor) Run(_ context.Context, _ model.Task, _ sandbox.Sandbox, _ string) (runner.Result, error) {
	return runner.Result{
		Outcome: f.outcome,
		Summary: "fake execution",
	}, nil
}

type countingExecutor struct {
	outcome model.RunOutcome
	mu      sync.Mutex
	calls   int
}

func (c *countingExecutor) Run(_ context.Context, _ model.Task, _ sandbox.Sandbox, _ string) (runner.Result, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return runner.Result{
		Outcome: c.outcome,
		Summary: "counting execution",
	}, nil
}

func (c *countingExecutor) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func TestOrchestratorDeterministicOutcomes(t *testing.T) {
	tests := []struct {
		name        string
		taskOutcome model.RunOutcome
		expected    model.VerdictStatus
	}{
		{
			name:        "pass",
			taskOutcome: model.RunOutcomePass,
			expected:    model.VerdictPass,
		},
		{
			name:        "fail",
			taskOutcome: model.RunOutcomeFail,
			expected:    model.VerdictFail,
		},
		{
			name:        "cannot verify",
			taskOutcome: model.RunOutcomeBlocked,
			expected:    model.VerdictCannotVerify,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store, err := blackboard.NewStore(t.TempDir())
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			t.Cleanup(func() {
				_ = store.Close()
			})

			runID := "run_" + testCase.name
			plannerOutput := planneragent.Output{
				FeatureSpec: model.FeatureSpec{
					SchemaVersion: model.CurrentSchemaVersion,
					RunID:         runID,
					Description:   "login works",
					AcceptanceCriteria: []model.AcceptanceCriterion{
						{ID: "ac_1", Text: "user logs in"},
					},
					Surfaces: []model.Surface{model.SurfaceWeb},
				},
				TestPlan: model.TestPlan{
					SchemaVersion: model.CurrentSchemaVersion,
					RunID:         runID,
					Journeys:      []string{"journey_1"},
					Assertions:    []string{"user logs in"},
				},
				Tasks: []model.Task{
					{
						SchemaVersion: model.CurrentSchemaVersion,
						TaskID:        "task_1",
						RunID:         runID,
						Surface:       model.SurfaceWeb,
						Kind:          model.TaskKindProof,
						Priority:      model.PriorityP1,
						Status:        model.TaskStatusQueued,
						DedupeKey:     "web:proof:1",
						MaxAttempts:   1,
						CreatedBy:     "planner",
						AcceptanceCriteriaIDs: []string{
							"ac_1",
						},
					},
				},
			}

			orchestrator := New(
				store,
				stubPlanner{output: plannerOutput},
				judgeagent.New(),
				map[model.Surface]Executor{
					model.SurfaceWeb: fakeExecutor{outcome: testCase.taskOutcome},
				},
				Budget{
					MaxQueuedTasks:          50,
					MaxNewTasksPerJudgeTurn: 10,
					MaxJudgeTurns:           2,
				},
			)

			verdict, err := orchestrator.Run(context.Background(), Request{
				RunID:       runID,
				Description: "login works",
				Surfaces:    []model.Surface{model.SurfaceWeb},
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if verdict.Status != testCase.expected {
				t.Fatalf("verdict.Status = %s, want %s", verdict.Status, testCase.expected)
			}
		})
	}
}

func TestOrchestratorRetriesFailedTaskUntilMaxAttempts(t *testing.T) {
	store, err := blackboard.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	runID := "run_retry_fails"
	plannerOutput := planneragent.Output{
		FeatureSpec: model.FeatureSpec{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Description:   "login fails",
			AcceptanceCriteria: []model.AcceptanceCriterion{
				{ID: "ac_1", Text: "user logs in"},
			},
			Surfaces: []model.Surface{model.SurfaceWeb},
		},
		TestPlan: model.TestPlan{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Journeys:      []string{"journey_1"},
			Assertions:    []string{"user logs in"},
		},
		Tasks: []model.Task{
			{
				SchemaVersion: model.CurrentSchemaVersion,
				TaskID:        "task_1",
				RunID:         runID,
				Surface:       model.SurfaceWeb,
				Kind:          model.TaskKindProof,
				Priority:      model.PriorityP1,
				Status:        model.TaskStatusQueued,
				DedupeKey:     "web:proof:1",
				MaxAttempts:   3,
				CreatedBy:     "planner",
				AcceptanceCriteriaIDs: []string{
					"ac_1",
				},
			},
		},
	}

	executor := &countingExecutor{outcome: model.RunOutcomeFail}
	orchestrator := New(
		store,
		stubPlanner{output: plannerOutput},
		judgeagent.New(),
		map[model.Surface]Executor{
			model.SurfaceWeb: executor,
		},
		Budget{
			MaxQueuedTasks:          50,
			MaxNewTasksPerJudgeTurn: 10,
			MaxJudgeTurns:           2,
			MaxRetriesPerTask:       3,
		},
	)

	_, err = orchestrator.Run(context.Background(), Request{
		RunID:       runID,
		Description: "login fails",
		Surfaces:    []model.Surface{model.SurfaceWeb},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if executor.CallCount() != 3 {
		t.Fatalf("executor.CallCount() = %d, want 3", executor.CallCount())
	}
}
