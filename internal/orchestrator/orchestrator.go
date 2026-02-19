package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	judgeagent "qa-agent/internal/agents/judge"
	planneragent "qa-agent/internal/agents/planner"
	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
	"qa-agent/internal/queue"
	"qa-agent/internal/runner"
	"qa-agent/internal/sandbox"
	"qa-agent/internal/stability"
)

type Planner interface {
	Plan(ctx context.Context, runID, description string, surfaces []model.Surface) (planneragent.Output, error)
}

type Judge interface {
	Evaluate(ctx context.Context, input judgeagent.Input) (judgeagent.Output, error)
}

type Executor interface {
	Run(ctx context.Context, task model.Task, env sandbox.Sandbox, artifactDir string) (runner.Result, error)
}

type Budget struct {
	MaxQueuedTasks          int
	MaxNewTasksPerJudgeTurn int
	MaxJudgeTurns           int
	MaxWallTime             time.Duration
	MaxRetriesPerTask       int
}

type Request struct {
	RunID       string
	Description string
	Surfaces    []model.Surface
}

type Orchestrator struct {
	store     *blackboard.Store
	queue     *queue.Manager
	planner   Planner
	judge     Judge
	executors map[model.Surface]Executor
	budget    Budget
}

func New(store *blackboard.Store, planner Planner, judge Judge, executors map[model.Surface]Executor, budget Budget) *Orchestrator {
	if budget.MaxQueuedTasks <= 0 {
		budget.MaxQueuedTasks = 200
	}
	if budget.MaxNewTasksPerJudgeTurn <= 0 {
		budget.MaxNewTasksPerJudgeTurn = 25
	}
	if budget.MaxJudgeTurns <= 0 {
		budget.MaxJudgeTurns = 5
	}
	if budget.MaxWallTime <= 0 {
		budget.MaxWallTime = 10 * time.Minute
	}
	if budget.MaxRetriesPerTask <= 0 {
		budget.MaxRetriesPerTask = 3
	}
	return &Orchestrator{
		store:     store,
		queue:     queue.NewManager(store, budget.MaxQueuedTasks),
		planner:   planner,
		judge:     judge,
		executors: executors,
		budget:    budget,
	}
}

func (o *Orchestrator) Run(ctx context.Context, request Request) (model.Verdict, error) {
	if request.RunID == "" {
		return model.Verdict{}, errors.New("run id is required")
	}
	if request.Description == "" {
		return model.Verdict{}, errors.New("description is required")
	}
	if len(request.Surfaces) == 0 {
		request.Surfaces = []model.Surface{model.SurfaceWeb}
	}

	if err := o.store.CreateValidationRun(ctx, blackboard.ValidationRun{
		ID:              request.RunID,
		RetentionPolicy: blackboard.RetentionKeepAll,
		Budgets: map[string]int{
			"max_queued_tasks":        o.budget.MaxQueuedTasks,
			"max_new_tasks_per_judge": o.budget.MaxNewTasksPerJudgeTurn,
			"max_judge_turns":         o.budget.MaxJudgeTurns,
			"max_retries_per_task":    o.budget.MaxRetriesPerTask,
		},
	}); err != nil {
		return model.Verdict{}, err
	}

	plan, err := o.planner.Plan(ctx, request.RunID, request.Description, request.Surfaces)
	if err != nil {
		return model.Verdict{}, err
	}
	for _, task := range plan.Tasks {
		if err := o.queue.EnqueueTask(ctx, task); err != nil && !errors.Is(err, queue.ErrTaskExists) {
			return model.Verdict{}, err
		}
	}

	started := time.Now().UTC()
	for judgeTurn := 0; judgeTurn < o.budget.MaxJudgeTurns; judgeTurn++ {
		if time.Since(started) > o.budget.MaxWallTime {
			return o.cannotVerify(ctx, request.RunID, "wall time budget exhausted")
		}

		for {
			task, err := o.queue.ClaimTask(ctx, request.RunID, "execution-harness", 2*time.Minute)
			if err != nil {
				if errors.Is(err, queue.ErrNoTaskReady) {
					break
				}
				return model.Verdict{}, err
			}

			executor, ok := o.executors[task.Surface]
			if !ok {
				if err := o.queue.CompleteTask(ctx, request.RunID, task.TaskID, model.TaskStatusBlocked); err != nil {
					return model.Verdict{}, err
				}
				continue
			}

			artifactDir := filepath.Join(o.store.ArtifactDir(request.RunID), "tasks", task.TaskID)
			result, runErr := executor.Run(ctx, task, sandbox.Sandbox{RunID: request.RunID}, artifactDir)

			finishedAt := time.Now().UTC()
			runRecord := model.Run{
				SchemaVersion: model.CurrentSchemaVersion,
				RunID:         request.RunID,
				TaskID:        task.TaskID,
				Outcome:       mapOutcome(result.Outcome, runErr),
				Summary:       result.Summary,
				TraceRef:      result.ActionTraceRef,
				StartedAt:     finishedAt.Add(-time.Millisecond),
				FinishedAt:    finishedAt,
			}
			if err := o.store.CreateRun(ctx, runRecord); err != nil {
				return model.Verdict{}, err
			}

			retry, err := o.shouldRetryTask(ctx, request.RunID, task, runRecord.Outcome)
			if err != nil {
				return model.Verdict{}, err
			}
			if retry {
				if err := o.queue.RequeueTask(ctx, request.RunID, task.TaskID); err != nil {
					return model.Verdict{}, err
				}
				continue
			}

			finalStatus := mapTaskStatus(runRecord.Outcome)
			if err := o.queue.CompleteTask(ctx, request.RunID, task.TaskID, finalStatus); err != nil {
				return model.Verdict{}, err
			}
		}

		tasks, err := o.store.TaskList(ctx, blackboard.TaskFilter{RunID: request.RunID, Limit: o.budget.MaxQueuedTasks})
		if err != nil {
			return model.Verdict{}, err
		}
		runs, err := o.store.RunList(ctx, blackboard.RunFilter{RunID: request.RunID, Limit: 1000})
		if err != nil {
			return model.Verdict{}, err
		}
		evidence, err := o.store.EvidenceList(ctx, blackboard.EvidenceFilter{RunID: request.RunID, Limit: 2000})
		if err != nil {
			return model.Verdict{}, err
		}

		judgeOutput, err := o.judge.Evaluate(ctx, judgeagent.Input{
			RunID:       request.RunID,
			FeatureSpec: plan.FeatureSpec,
			Tasks:       tasks,
			Runs:        runs,
			Evidence:    evidence,
			MaxRounds:   2,
		})
		if err != nil {
			return model.Verdict{}, err
		}
		if judgeOutput.Verdict != nil {
			if err := o.store.UpsertVerdict(ctx, *judgeOutput.Verdict); err != nil {
				return model.Verdict{}, err
			}
			return *judgeOutput.Verdict, nil
		}
		if len(judgeOutput.NextTasks) == 0 {
			return o.cannotVerify(ctx, request.RunID, "judge returned no verdict and no tasks")
		}

		newTasks := judgeOutput.NextTasks
		if len(newTasks) > o.budget.MaxNewTasksPerJudgeTurn {
			newTasks = newTasks[:o.budget.MaxNewTasksPerJudgeTurn]
		}
		for _, task := range newTasks {
			if task.MaxAttempts <= 0 {
				task.MaxAttempts = o.budget.MaxRetriesPerTask
			}
			if err := o.queue.EnqueueTask(ctx, task); err != nil && !errors.Is(err, queue.ErrTaskExists) {
				return model.Verdict{}, err
			}
		}
	}

	return o.cannotVerify(ctx, request.RunID, "judge turn budget exhausted")
}

func (o *Orchestrator) cannotVerify(ctx context.Context, runID, reason string) (model.Verdict, error) {
	verdict := model.Verdict{
		SchemaVersion: model.CurrentSchemaVersion,
		VerdictID:     fmt.Sprintf("verdict_cannot_verify_%d", time.Now().UTC().UnixNano()),
		RunID:         runID,
		Status:        model.VerdictCannotVerify,
		Reasons:       []string{reason},
		Coverage:      map[string][]string{"unknown": {"missing"}},
	}
	if err := o.store.UpsertVerdict(ctx, verdict); err != nil {
		return model.Verdict{}, err
	}
	return verdict, nil
}

func mapOutcome(outcome model.RunOutcome, err error) model.RunOutcome {
	if err != nil {
		return model.RunOutcomeError
	}
	if !outcome.IsValid() {
		return model.RunOutcomeError
	}
	return outcome
}

func mapTaskStatus(outcome model.RunOutcome) model.TaskStatus {
	switch outcome {
	case model.RunOutcomePass:
		return model.TaskStatusPassed
	case model.RunOutcomeFail, model.RunOutcomeFlaky:
		return model.TaskStatusFailed
	case model.RunOutcomeBlocked:
		return model.TaskStatusBlocked
	default:
		return model.TaskStatusErrored
	}
}

func (o *Orchestrator) shouldRetryTask(ctx context.Context, runID string, task model.Task, latestOutcome model.RunOutcome) (bool, error) {
	if task.AttemptCount >= task.MaxAttempts {
		return false, nil
	}

	switch latestOutcome {
	case model.RunOutcomePass, model.RunOutcomeBlocked:
		return false, nil
	case model.RunOutcomeFail:
		// Require at least one repro before failing a task.
		return true, nil
	}

	historyRows, err := o.store.RunList(ctx, blackboard.RunFilter{
		RunID:  runID,
		TaskID: task.TaskID,
		Limit:  task.MaxAttempts,
	})
	if err != nil {
		return false, err
	}
	history := make([]model.RunOutcome, 0, len(historyRows))
	for _, row := range historyRows {
		history = append(history, row.Outcome)
	}
	if len(history) == 0 {
		history = append(history, latestOutcome)
	}

	policy := stability.NewPolicy(stability.Policy{
		Budget: stability.Budget{
			PerAssertion: task.MaxAttempts,
			PerTask:      task.MaxAttempts,
			PerRun:       task.MaxAttempts,
			Global:       task.MaxAttempts,
		},
	})
	decision, err := policy.Decide(
		history,
		task.AttemptCount,
		task.AttemptCount,
		task.AttemptCount,
		task.AttemptCount,
	)
	if err != nil {
		return false, err
	}
	return decision.Retry, nil
}
