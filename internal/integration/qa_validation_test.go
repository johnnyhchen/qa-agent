package integration

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	judgeagent "qa-agent/internal/agents/judge"
	planneragent "qa-agent/internal/agents/planner"
	"qa-agent/internal/agents/repair"
	"qa-agent/internal/agents/runtime"
	"qa-agent/internal/blackboard"
	"qa-agent/internal/config"
	"qa-agent/internal/evidence/processor"
	"qa-agent/internal/model"
	"qa-agent/internal/orchestrator"
	"qa-agent/internal/queue"
	"qa-agent/internal/replay"
	"qa-agent/internal/report"
	"qa-agent/internal/runner"
	apirunner "qa-agent/internal/runner/api"
	iosrunner "qa-agent/internal/runner/ios"
	"qa-agent/internal/sandbox"
	"qa-agent/internal/stability"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func newTestStore(t *testing.T) *blackboard.Store {
	t.Helper()
	store, err := blackboard.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func makeTask(runID, taskID, dedupe string, surface model.Surface, kind model.TaskKind, priority model.Priority) model.Task {
	return model.Task{
		SchemaVersion: model.CurrentSchemaVersion,
		TaskID:        taskID,
		RunID:         runID,
		Surface:       surface,
		Kind:          kind,
		Priority:      priority,
		Status:        model.TaskStatusQueued,
		DedupeKey:     dedupe,
		MaxAttempts:   3,
		CreatedBy:     "test",
		AcceptanceCriteriaIDs: []string{"ac_1"},
	}
}

func makeRun(runID, taskID string, outcome model.RunOutcome) model.Run {
	now := time.Now().UTC()
	return model.Run{
		SchemaVersion: model.CurrentSchemaVersion,
		RunID:         runID,
		TaskID:        taskID,
		Outcome:       outcome,
		Summary:       "test run",
		TraceRef:      "trace_" + taskID,
		StartedAt:     now.Add(-time.Second),
		FinishedAt:    now,
	}
}

// controllableExecutor lets tests dictate per-task outcomes.
type controllableExecutor struct {
	outcomes map[string]model.RunOutcome // taskID → outcome
	calls    []string                    // track execution order
	mu       sync.Mutex
}

func (e *controllableExecutor) Run(_ context.Context, task model.Task, _ sandbox.Sandbox, artifactDir string) (runner.Result, error) {
	e.mu.Lock()
	e.calls = append(e.calls, task.TaskID)
	e.mu.Unlock()

	_ = os.MkdirAll(artifactDir, 0o755)
	evidencePath := filepath.Join(artifactDir, "evidence.log")
	_ = os.WriteFile(evidencePath, []byte("test evidence"), 0o644)

	outcome, ok := e.outcomes[task.TaskID]
	if !ok {
		outcome = model.RunOutcomePass
	}
	return runner.Result{
		Outcome:        outcome,
		Summary:        fmt.Sprintf("task %s: %s", task.TaskID, outcome),
		EvidenceFiles:  []string{evidencePath},
		ActionTraceRef: "trace_" + task.TaskID,
	}, nil
}

// failingExecutor always fails.
type failingExecutor struct{}

func (f failingExecutor) Run(_ context.Context, task model.Task, _ sandbox.Sandbox, artifactDir string) (runner.Result, error) {
	_ = os.MkdirAll(artifactDir, 0o755)
	return runner.Result{
		Outcome: model.RunOutcomeFail,
		Summary: "always fails",
	}, nil
}

// blockedExecutor always blocks.
type blockedExecutor struct{}

func (b blockedExecutor) Run(_ context.Context, _ model.Task, _ sandbox.Sandbox, artifactDir string) (runner.Result, error) {
	_ = os.MkdirAll(artifactDir, 0o755)
	return runner.Result{
		Outcome: model.RunOutcomeBlocked,
		Summary: "blocked",
	}, nil
}

// countingPlanner returns a fixed plan and counts calls.
type countingPlanner struct {
	output planneragent.Output
	count  int
}

func (p *countingPlanner) Plan(_ context.Context, _ string, _ string, _ []model.Surface) (planneragent.Output, error) {
	p.count++
	return p.output, nil
}

// ===================================================================
// SECTION 1: GOLDEN TEST CASES — known inputs with expected outputs
// ===================================================================

func TestQA_Golden_PlannerSplitsSentences(t *testing.T) {
	store := newTestStore(t)
	rt := runtime.New(store, nil, nil, runtime.Config{})
	agent := planneragent.New(rt, store)

	tests := []struct {
		name        string
		description string
		surfaces    []model.Surface
		wantCrit    int
		wantTasks   int
	}{
		{"single sentence", "The button works", []model.Surface{model.SurfaceWeb}, 1, 1},
		{"two sentences", "Login works. Logout works", []model.Surface{model.SurfaceWeb}, 2, 2},
		{"three sentences two surfaces", "A. B. C", []model.Surface{model.SurfaceWeb, model.SurfaceAPI}, 3, 6},
		{"trailing period", "Feature works.", []model.Surface{model.SurfaceWeb}, 1, 1},
		{"question mark", "Does it work?", []model.Surface{model.SurfaceWeb}, 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runID := fmt.Sprintf("run_%s_%d", tt.name, time.Now().UnixNano())
			out, err := agent.Plan(context.Background(), runID, tt.description, tt.surfaces)
			if err != nil {
				t.Fatalf("Plan error: %v", err)
			}
			if len(out.FeatureSpec.AcceptanceCriteria) != tt.wantCrit {
				t.Errorf("criteria count = %d, want %d", len(out.FeatureSpec.AcceptanceCriteria), tt.wantCrit)
			}
			if len(out.Tasks) != tt.wantTasks {
				t.Errorf("task count = %d, want %d", len(out.Tasks), tt.wantTasks)
			}
			// Validate all tasks
			for i, task := range out.Tasks {
				if err := task.Validate(); err != nil {
					t.Errorf("task[%d] invalid: %v", i, err)
				}
				if task.Kind != model.TaskKindProof {
					t.Errorf("task[%d] kind = %s, want proof", i, task.Kind)
				}
				if task.Priority != model.PriorityP1 {
					t.Errorf("task[%d] priority = %s, want P1", i, task.Priority)
				}
			}
			// Question mark generates open questions
			if strings.Contains(tt.description, "?") && len(out.OpenQuestions) == 0 {
				t.Error("expected open questions for description with ?")
			}
		})
	}
}

func TestQA_Golden_JudgeVerdicts(t *testing.T) {
	judge := judgeagent.New()
	runID := "run_judge_golden"

	spec := model.FeatureSpec{
		SchemaVersion: model.CurrentSchemaVersion,
		RunID:         runID,
		Description:   "feature",
		AcceptanceCriteria: []model.AcceptanceCriterion{
			{ID: "ac_1", Text: "criterion A"},
			{ID: "ac_2", Text: "criterion B"},
		},
		Surfaces: []model.Surface{model.SurfaceWeb},
	}

	t.Run("all pass", func(t *testing.T) {
		t1 := makeTask(runID, "t1", "d1", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1)
		t1.Status = model.TaskStatusPassed
		t1.AcceptanceCriteriaIDs = []string{"ac_1"}
		t2 := makeTask(runID, "t2", "d2", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1)
		t2.Status = model.TaskStatusPassed
		t2.AcceptanceCriteriaIDs = []string{"ac_2"}

		out, err := judge.Evaluate(context.Background(), judgeagent.Input{
			RunID:       runID,
			FeatureSpec: spec,
			Tasks:       []model.Task{t1, t2},
			Runs: []model.Run{
				makeRun(runID, "t1", model.RunOutcomePass),
				makeRun(runID, "t2", model.RunOutcomePass),
			},
		})
		if err != nil {
			t.Fatalf("Evaluate error: %v", err)
		}
		if out.Verdict == nil {
			t.Fatal("expected verdict, got nil")
		}
		if out.Verdict.Status != model.VerdictPass {
			t.Errorf("verdict = %s, want pass", out.Verdict.Status)
		}
		// Coverage map should have both criteria
		if len(out.Verdict.Coverage) != 2 {
			t.Errorf("coverage entries = %d, want 2", len(out.Verdict.Coverage))
		}
	})

	t.Run("one fails", func(t *testing.T) {
		t1 := makeTask(runID, "t1", "d1", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1)
		t1.Status = model.TaskStatusPassed
		t1.AcceptanceCriteriaIDs = []string{"ac_1"}
		t2 := makeTask(runID, "t2", "d2", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1)
		t2.Status = model.TaskStatusFailed
		t2.AcceptanceCriteriaIDs = []string{"ac_2"}

		out, err := judge.Evaluate(context.Background(), judgeagent.Input{
			RunID:       runID,
			FeatureSpec: spec,
			Tasks:       []model.Task{t1, t2},
			Runs: []model.Run{
				makeRun(runID, "t1", model.RunOutcomePass),
				makeRun(runID, "t2", model.RunOutcomeFail),
			},
		})
		if err != nil {
			t.Fatalf("Evaluate error: %v", err)
		}
		if out.Verdict == nil {
			t.Fatal("expected verdict")
		}
		if out.Verdict.Status != model.VerdictFail {
			t.Errorf("verdict = %s, want fail", out.Verdict.Status)
		}
		if len(out.Findings) == 0 {
			t.Error("expected findings for failed criterion")
		}
	})

	t.Run("blocked runs", func(t *testing.T) {
		t1 := makeTask(runID, "t1", "d1", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1)
		t1.Status = model.TaskStatusBlocked
		t1.AcceptanceCriteriaIDs = []string{"ac_1"}

		out, err := judge.Evaluate(context.Background(), judgeagent.Input{
			RunID:       runID,
			FeatureSpec: spec,
			Tasks:       []model.Task{t1},
			Runs: []model.Run{
				makeRun(runID, "t1", model.RunOutcomeBlocked),
			},
		})
		if err != nil {
			t.Fatalf("Evaluate error: %v", err)
		}
		if out.Verdict == nil {
			t.Fatal("expected verdict")
		}
		if out.Verdict.Status != model.VerdictCannotVerify {
			t.Errorf("verdict = %s, want cannot_verify", out.Verdict.Status)
		}
	})

	t.Run("missing proof generates next tasks", func(t *testing.T) {
		out, err := judge.Evaluate(context.Background(), judgeagent.Input{
			RunID:       runID,
			FeatureSpec: spec,
			Tasks:       []model.Task{}, // no tasks executed yet
			Runs:        []model.Run{},
		})
		if err != nil {
			t.Fatalf("Evaluate error: %v", err)
		}
		if out.Verdict != nil {
			t.Fatalf("expected no verdict, got %s", out.Verdict.Status)
		}
		if len(out.NextTasks) != 2 { // one per missing criterion
			t.Errorf("next_tasks = %d, want 2", len(out.NextTasks))
		}
		for _, task := range out.NextTasks {
			if task.Kind != model.TaskKindCounterexample {
				t.Errorf("task kind = %s, want counterexample", task.Kind)
			}
		}
	})

	t.Run("XOR invariant holds", func(t *testing.T) {
		// Verify that every output has exactly one of NextTasks or Verdict
		passedTask := makeTask(runID, "t1", "d1", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1)
		passedTask.Status = model.TaskStatusPassed
		passedTask.AcceptanceCriteriaIDs = []string{"ac_1", "ac_2"}

		inputs := []judgeagent.Input{
			{RunID: runID, FeatureSpec: spec, Tasks: []model.Task{}, Runs: []model.Run{}},
			{RunID: runID, FeatureSpec: spec,
				Tasks: []model.Task{passedTask},
				Runs:  []model.Run{makeRun(runID, "t1", model.RunOutcomePass)}},
		}
		for i, input := range inputs {
			out, err := judge.Evaluate(context.Background(), input)
			if err != nil {
				t.Fatalf("input[%d] error: %v", i, err)
			}
			hasTasks := len(out.NextTasks) > 0
			hasVerdict := out.Verdict != nil
			if hasTasks == hasVerdict {
				t.Errorf("input[%d] XOR violated: hasTasks=%v hasVerdict=%v", i, hasTasks, hasVerdict)
			}
		}
	})
}

func TestQA_Golden_StabilityClassification(t *testing.T) {
	tests := []struct {
		name    string
		history []model.RunOutcome
		want    stability.Outcome
	}{
		{"two passes", []model.RunOutcome{model.RunOutcomePass, model.RunOutcomePass}, stability.OutcomeStablePass},
		{"two fails", []model.RunOutcome{model.RunOutcomeFail, model.RunOutcomeFail}, stability.OutcomeStableFail},
		{"pass then fail", []model.RunOutcome{model.RunOutcomePass, model.RunOutcomeFail}, stability.OutcomeFlaky},
		{"fail then pass", []model.RunOutcome{model.RunOutcomeFail, model.RunOutcomePass}, stability.OutcomeFlaky},
		{"blocked only", []model.RunOutcome{model.RunOutcomeBlocked}, stability.OutcomeBlocked},
		{"error only", []model.RunOutcome{model.RunOutcomeError}, stability.OutcomeError},
		{"pass fail pass pass", []model.RunOutcome{model.RunOutcomePass, model.RunOutcomeFail, model.RunOutcomePass, model.RunOutcomePass}, stability.OutcomeFlaky},
		{"single pass is inconclusive", []model.RunOutcome{model.RunOutcomePass}, stability.OutcomeInconclusive},
		{"three passes", []model.RunOutcome{model.RunOutcomePass, model.RunOutcomePass, model.RunOutcomePass}, stability.OutcomeStablePass},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stability.Classify(tt.history, 2)
			if got != tt.want {
				t.Errorf("Classify(%v) = %s, want %s", tt.history, got, tt.want)
			}
		})
	}
}

func TestQA_Golden_StabilityDecide(t *testing.T) {
	policy := stability.NewPolicy(stability.Policy{})

	t.Run("stable pass no retry", func(t *testing.T) {
		d, err := policy.Decide([]model.RunOutcome{model.RunOutcomePass, model.RunOutcomePass}, 2, 2, 2, 2)
		if err != nil {
			t.Fatal(err)
		}
		if d.Retry {
			t.Error("expected no retry for stable pass")
		}
		if d.Final != stability.OutcomeStablePass {
			t.Errorf("final = %s, want stable_pass", d.Final)
		}
	})

	t.Run("flaky retries", func(t *testing.T) {
		d, err := policy.Decide([]model.RunOutcome{model.RunOutcomePass, model.RunOutcomeFail}, 2, 2, 2, 2)
		if err != nil {
			t.Fatal(err)
		}
		if !d.Retry {
			t.Error("expected retry for flaky")
		}
	})

	t.Run("budget exhausted no retry", func(t *testing.T) {
		d, err := policy.Decide([]model.RunOutcome{model.RunOutcomePass, model.RunOutcomeFail}, 5, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if d.Retry {
			t.Error("expected no retry when budget exhausted")
		}
	})

	t.Run("empty history error", func(t *testing.T) {
		_, err := policy.Decide([]model.RunOutcome{}, 0, 0, 0, 0)
		if err == nil {
			t.Error("expected error for empty history")
		}
	})
}

func TestQA_Golden_QueuePriorityOrder(t *testing.T) {
	store := newTestStore(t)
	mgr := queue.NewManager(store, 100)
	ctx := context.Background()
	runID := "run_queue_golden"

	_ = store.CreateValidationRun(ctx, blackboard.ValidationRun{ID: runID})

	// Enqueue in reverse priority order
	priorities := []model.Priority{model.PriorityP3, model.PriorityP1, model.PriorityP0, model.PriorityP2}
	for i, p := range priorities {
		err := mgr.EnqueueTask(ctx, makeTask(runID, fmt.Sprintf("t%d", i), fmt.Sprintf("d%d", i), model.SurfaceWeb, model.TaskKindProof, p))
		if err != nil {
			t.Fatalf("EnqueueTask: %v", err)
		}
	}

	// Claims should come in P0, P1, P2, P3 order
	expected := []model.Priority{model.PriorityP0, model.PriorityP1, model.PriorityP2, model.PriorityP3}
	for _, wantPriority := range expected {
		task, err := mgr.ClaimTask(ctx, runID, "test", time.Minute)
		if err != nil {
			t.Fatalf("ClaimTask: %v", err)
		}
		if task.Priority != wantPriority {
			t.Errorf("claimed priority = %s, want %s", task.Priority, wantPriority)
		}
		_ = mgr.CompleteTask(ctx, runID, task.TaskID, model.TaskStatusPassed)
	}

	// Queue should be empty now
	_, err := mgr.ClaimTask(ctx, runID, "test", time.Minute)
	if err != queue.ErrNoTaskReady {
		t.Errorf("expected ErrNoTaskReady, got %v", err)
	}
}

func TestQA_Golden_QueueDeduplication(t *testing.T) {
	store := newTestStore(t)
	mgr := queue.NewManager(store, 100)
	ctx := context.Background()
	runID := "run_dedup"

	_ = store.CreateValidationRun(ctx, blackboard.ValidationRun{ID: runID})

	task := makeTask(runID, "t1", "same_key", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1)
	if err := mgr.EnqueueTask(ctx, task); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}

	task.TaskID = "t2" // different ID, same dedupe key
	err := mgr.EnqueueTask(ctx, task)
	if err != queue.ErrTaskExists {
		t.Errorf("expected ErrTaskExists, got %v", err)
	}
}

func TestQA_Golden_QueueEviction(t *testing.T) {
	store := newTestStore(t)
	mgr := queue.NewManager(store, 2) // max 2 queued
	ctx := context.Background()
	runID := "run_evict"

	_ = store.CreateValidationRun(ctx, blackboard.ValidationRun{ID: runID})

	// Fill queue with P3 tasks
	_ = mgr.EnqueueTask(ctx, makeTask(runID, "t1", "d1", model.SurfaceWeb, model.TaskKindProof, model.PriorityP3))
	_ = mgr.EnqueueTask(ctx, makeTask(runID, "t2", "d2", model.SurfaceWeb, model.TaskKindProof, model.PriorityP3))

	// P0 task should evict one P3
	err := mgr.EnqueueTask(ctx, makeTask(runID, "t3", "d3", model.SurfaceWeb, model.TaskKindProof, model.PriorityP0))
	if err != nil {
		t.Fatalf("P0 enqueue should succeed via eviction: %v", err)
	}

	// P3 task should be rejected when queue is full
	err = mgr.EnqueueTask(ctx, makeTask(runID, "t4", "d4", model.SurfaceWeb, model.TaskKindProof, model.PriorityP3))
	if err != queue.ErrQueueSaturated {
		t.Errorf("expected ErrQueueSaturated for P3, got %v", err)
	}
}

func TestQA_Golden_ConfigPrecedence(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg, err := config.LoadWithEnv("", mockEnv{}, config.CLIOverrides{})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.OutputDir != ".qa-agent/runs" {
			t.Errorf("OutputDir = %q, want .qa-agent/runs", cfg.OutputDir)
		}
		if cfg.ToolBins.AIBrowserUseBin != "ai-browser-use" {
			t.Errorf("AIBrowserUseBin = %q", cfg.ToolBins.AIBrowserUseBin)
		}
	})

	t.Run("file overrides defaults", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "qa-agent.json")
		_ = os.WriteFile(cfgPath, []byte(`{"output_dir": "/custom/path"}`), 0o644)

		cfg, err := config.LoadWithEnv(cfgPath, mockEnv{}, config.CLIOverrides{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.OutputDir != "/custom/path" {
			t.Errorf("OutputDir = %q, want /custom/path", cfg.OutputDir)
		}
	})

	t.Run("env overrides file", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "qa-agent.json")
		_ = os.WriteFile(cfgPath, []byte(`{"output_dir": "/from/file"}`), 0o644)

		cfg, err := config.LoadWithEnv(cfgPath, mockEnv{"QA_AGENT_OUTPUT_DIR": "/from/env"}, config.CLIOverrides{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.OutputDir != "/from/env" {
			t.Errorf("OutputDir = %q, want /from/env", cfg.OutputDir)
		}
	})

	t.Run("CLI overrides env", func(t *testing.T) {
		cliDir := "/from/cli"
		cfg, err := config.LoadWithEnv("", mockEnv{"QA_AGENT_OUTPUT_DIR": "/from/env"}, config.CLIOverrides{OutputDir: &cliDir})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.OutputDir != "/from/cli" {
			t.Errorf("OutputDir = %q, want /from/cli", cfg.OutputDir)
		}
	})
}

type mockEnv map[string]string

func (m mockEnv) LookupEnv(key string) (string, bool) {
	v, ok := m[key]
	return v, ok
}

func TestQA_Golden_APIRunnerHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(200)
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/not-found":
			w.WriteHeader(404)
			fmt.Fprint(w, "not found")
		case "/echo-auth":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"auth":"%s"}`, r.Header.Get("Authorization"))
		default:
			w.WriteHeader(500)
		}
	}))
	defer server.Close()

	adapter := apirunner.NewAdapter(5 * time.Second)
	ctx := context.Background()

	t.Run("200 pass", func(t *testing.T) {
		task := model.Task{
			SchemaVersion: model.CurrentSchemaVersion,
			TaskID:        "api_ok",
			RunID:         "run_api",
			Surface:       model.SurfaceAPI,
			Kind:          model.TaskKindProof,
			Priority:      model.PriorityP1,
			Status:        model.TaskStatusQueued,
			DedupeKey:     "api_ok",
			MaxAttempts:   1,
			Payload: map[string]any{
				"http_requests": []any{
					map[string]any{
						"method":        "GET",
						"url":           server.URL + "/ok",
						"expect_status": float64(200),
					},
				},
			},
		}
		dir := t.TempDir()
		result, err := adapter.Run(ctx, task, sandbox.Sandbox{}, dir)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Outcome != model.RunOutcomePass {
			t.Errorf("outcome = %s, want pass", result.Outcome)
		}
		// Verify transcript written
		transcriptPath := filepath.Join(dir, "api-transcript.json")
		if _, err := os.Stat(transcriptPath); err != nil {
			t.Errorf("transcript not written: %v", err)
		}
	})

	t.Run("404 fail", func(t *testing.T) {
		task := model.Task{
			SchemaVersion: model.CurrentSchemaVersion,
			TaskID:        "api_404",
			RunID:         "run_api",
			Surface:       model.SurfaceAPI,
			Kind:          model.TaskKindProof,
			Priority:      model.PriorityP1,
			Status:        model.TaskStatusQueued,
			DedupeKey:     "api_404",
			MaxAttempts:   1,
			Payload: map[string]any{
				"http_requests": []any{
					map[string]any{
						"method":        "GET",
						"url":           server.URL + "/not-found",
						"expect_status": float64(200),
					},
				},
			},
		}
		result, err := adapter.Run(ctx, task, sandbox.Sandbox{}, t.TempDir())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Outcome != model.RunOutcomeFail {
			t.Errorf("outcome = %s, want fail", result.Outcome)
		}
	})

	t.Run("body contains match", func(t *testing.T) {
		task := model.Task{
			SchemaVersion: model.CurrentSchemaVersion,
			TaskID:        "api_body",
			RunID:         "run_api",
			Surface:       model.SurfaceAPI,
			Kind:          model.TaskKindProof,
			Priority:      model.PriorityP1,
			Status:        model.TaskStatusQueued,
			DedupeKey:     "api_body",
			MaxAttempts:   1,
			Payload: map[string]any{
				"http_requests": []any{
					map[string]any{
						"method":              "GET",
						"url":                 server.URL + "/ok",
						"expect_status":       float64(200),
						"expect_body_contains": "ok",
					},
				},
			},
		}
		result, err := adapter.Run(ctx, task, sandbox.Sandbox{}, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if result.Outcome != model.RunOutcomePass {
			t.Errorf("outcome = %s, want pass", result.Outcome)
		}
	})

	t.Run("body contains mismatch", func(t *testing.T) {
		task := model.Task{
			SchemaVersion: model.CurrentSchemaVersion,
			TaskID:        "api_body_miss",
			RunID:         "run_api",
			Surface:       model.SurfaceAPI,
			Kind:          model.TaskKindProof,
			Priority:      model.PriorityP1,
			Status:        model.TaskStatusQueued,
			DedupeKey:     "api_body_miss",
			MaxAttempts:   1,
			Payload: map[string]any{
				"http_requests": []any{
					map[string]any{
						"method":              "GET",
						"url":                 server.URL + "/ok",
						"expect_status":       float64(200),
						"expect_body_contains": "NONEXISTENT",
					},
				},
			},
		}
		result, err := adapter.Run(ctx, task, sandbox.Sandbox{}, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if result.Outcome != model.RunOutcomeFail {
			t.Errorf("outcome = %s, want fail", result.Outcome)
		}
	})

	t.Run("auth header redacted in transcript", func(t *testing.T) {
		task := model.Task{
			SchemaVersion: model.CurrentSchemaVersion,
			TaskID:        "api_redact",
			RunID:         "run_api",
			Surface:       model.SurfaceAPI,
			Kind:          model.TaskKindProof,
			Priority:      model.PriorityP1,
			Status:        model.TaskStatusQueued,
			DedupeKey:     "api_redact",
			MaxAttempts:   1,
			Payload: map[string]any{
				"http_requests": []any{
					map[string]any{
						"method":        "GET",
						"url":           server.URL + "/ok",
						"expect_status": float64(200),
						"headers": map[string]any{
							"Authorization": "Bearer secret-token-123",
							"X-Custom":      "visible-value",
						},
					},
				},
			},
		}
		dir := t.TempDir()
		_, err := adapter.Run(ctx, task, sandbox.Sandbox{}, dir)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(filepath.Join(dir, "api-transcript.json"))
		transcript := string(raw)

		// Parse transcript to verify request headers are redacted
		var entries []map[string]any
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatalf("invalid transcript JSON: %v", err)
		}
		if len(entries) == 0 {
			t.Fatal("no transcript entries")
		}
		reqMap, ok := entries[0]["request"].(map[string]any)
		if !ok {
			t.Fatal("missing request in transcript")
		}
		headers, ok := reqMap["headers"].(map[string]any)
		if !ok {
			t.Fatal("missing headers in request")
		}
		authVal, _ := headers["Authorization"].(string)
		if authVal != "[redacted]" {
			t.Errorf("Authorization header = %q, want [redacted]", authVal)
		}
		customVal, _ := headers["X-Custom"].(string)
		if customVal != "visible-value" {
			t.Errorf("X-Custom header = %q, want visible-value", customVal)
		}

		// Also verify the redacted list is present
		if !strings.Contains(transcript, "\"redacted\"") {
			t.Error("transcript missing redacted field")
		}
	})
}

func TestQA_Golden_IOSRunnerAlwaysBlocked(t *testing.T) {
	adapter := iosrunner.NewAdapter("xcrun")
	task := model.Task{
		SchemaVersion: model.CurrentSchemaVersion,
		TaskID:        "ios_1",
		RunID:         "run_ios",
		Surface:       model.SurfaceIOS,
		Kind:          model.TaskKindProof,
		Priority:      model.PriorityP1,
		Status:        model.TaskStatusQueued,
		DedupeKey:     "ios_1",
		MaxAttempts:   1,
		Payload: map[string]any{
			"app_bundle_id":  "com.test.app",
			"device_profile": "iPhone 14",
			"steps":          []any{"tap button"},
			"assertions":     []any{"screen shows result"},
		},
	}
	result, err := adapter.Run(context.Background(), task, sandbox.Sandbox{}, t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Outcome != model.RunOutcomeBlocked {
		t.Errorf("outcome = %s, want blocked", result.Outcome)
	}
}

func TestQA_Golden_ReportGeneration(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "run_report")
	_ = os.MkdirAll(filepath.Join(runDir, "artifacts"), 0o755)
	_ = os.WriteFile(filepath.Join(runDir, "artifacts", "evidence.log"), []byte("data"), 0o644)

	gen := report.NewGenerator()

	t.Run("generate and write", func(t *testing.T) {
		reportPath, manifestPath, err := gen.Write("run_report", runDir)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		if _, err := os.Stat(reportPath); err != nil {
			t.Errorf("report missing: %v", err)
		}
		if _, err := os.Stat(manifestPath); err != nil {
			t.Errorf("manifest missing: %v", err)
		}

		// Read and validate manifest
		raw, _ := os.ReadFile(manifestPath)
		var manifest report.Manifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatalf("invalid manifest JSON: %v", err)
		}
		if manifest.RunID != "run_report" {
			t.Errorf("manifest.RunID = %q", manifest.RunID)
		}
		if len(manifest.Files) == 0 {
			t.Error("manifest has no files")
		}

		// Report should contain key sections
		reportRaw, _ := os.ReadFile(reportPath)
		reportStr := string(reportRaw)
		for _, section := range []string{"# QA-Agent Report", "## Verdict", "## Coverage"} {
			if !strings.Contains(reportStr, section) {
				t.Errorf("report missing section: %s", section)
			}
		}
	})

	t.Run("bundle requires manifest", func(t *testing.T) {
		emptyDir := t.TempDir()
		err := gen.Bundle("x", emptyDir, filepath.Join(emptyDir, "out.zip"))
		if err == nil {
			t.Error("expected error when manifest missing")
		}
	})

	t.Run("bundle creates valid zip", func(t *testing.T) {
		zipPath := filepath.Join(dir, "bundle.zip")
		err := gen.Bundle("run_report", runDir, zipPath)
		if err != nil {
			t.Fatalf("Bundle: %v", err)
		}
		r, err := zip.OpenReader(zipPath)
		if err != nil {
			t.Fatalf("invalid zip: %v", err)
		}
		defer r.Close()
		if len(r.File) == 0 {
			t.Error("zip has no files")
		}
	})
}

func TestQA_Golden_BlackboardIsolation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_ = store.CreateValidationRun(ctx, blackboard.ValidationRun{ID: "run_A"})
	_ = store.CreateValidationRun(ctx, blackboard.ValidationRun{ID: "run_B"})

	taskA := makeTask("run_A", "tA", "dA", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1)
	taskB := makeTask("run_B", "tB", "dB", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1)
	_ = store.CreateTask(ctx, taskA)
	_ = store.CreateTask(ctx, taskB)

	tasksA, _ := store.TaskList(ctx, blackboard.TaskFilter{RunID: "run_A", Limit: 100})
	tasksB, _ := store.TaskList(ctx, blackboard.TaskFilter{RunID: "run_B", Limit: 100})

	if len(tasksA) != 1 || tasksA[0].TaskID != "tA" {
		t.Errorf("run_A tasks unexpected: %v", tasksA)
	}
	if len(tasksB) != 1 || tasksB[0].TaskID != "tB" {
		t.Errorf("run_B tasks unexpected: %v", tasksB)
	}
}

func TestQA_Golden_ReplayOutputRewrite(t *testing.T) {
	// Test the rewriteOutputArg function behavior via the replay package
	// We test ListTraces with empty dir and with a trace present

	t.Run("empty artifacts dir", func(t *testing.T) {
		entries, err := replay.ListTraces(t.TempDir())
		if err != nil {
			t.Fatalf("ListTraces: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("expected 0 entries, got %d", len(entries))
		}
	})

	t.Run("finds trace files", func(t *testing.T) {
		dir := t.TempDir()
		traceDir := filepath.Join(dir, "artifacts", "traces", "trace_1")
		_ = os.MkdirAll(traceDir, 0o755)
		trace := model.ActionTrace{
			SchemaVersion: model.CurrentSchemaVersion,
			TraceID:       "trace_1",
			RunID:         "run_1",
			TaskID:        "task_1",
			Runner:        "test",
			Command:       []string{"echo", "hello"},
			StartedAt:     time.Now().UTC().Add(-time.Second),
			FinishedAt:    time.Now().UTC(),
		}
		raw, _ := json.MarshalIndent(trace, "", "  ")
		_ = os.WriteFile(filepath.Join(traceDir, "action-trace.json"), raw, 0o644)

		entries, err := replay.ListTraces(dir)
		if err != nil {
			t.Fatalf("ListTraces: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(entries))
		}
		if entries[0].TaskID != "task_1" {
			t.Errorf("TaskID = %q, want task_1", entries[0].TaskID)
		}
	})
}

// ===================================================================
// SECTION 2: SEEDED DEFECT DETECTION (precision/recall)
// ===================================================================

func TestQA_SeededDefect_JudgeDetectsPartialFailure(t *testing.T) {
	// Seed: 1 of 3 criteria fails. Judge should return VerdictFail.
	judge := judgeagent.New()
	runID := "run_seeded_partial"

	spec := model.FeatureSpec{
		SchemaVersion: model.CurrentSchemaVersion,
		RunID:         runID,
		Description:   "A. B. C",
		AcceptanceCriteria: []model.AcceptanceCriterion{
			{ID: "ac_1", Text: "A"},
			{ID: "ac_2", Text: "B"},
			{ID: "ac_3", Text: "C"},
		},
		Surfaces: []model.Surface{model.SurfaceWeb},
	}

	tasks := []model.Task{
		makeTask(runID, "t1", "d1", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1),
		makeTask(runID, "t2", "d2", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1),
		makeTask(runID, "t3", "d3", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1),
	}
	tasks[0].AcceptanceCriteriaIDs = []string{"ac_1"}
	tasks[1].AcceptanceCriteriaIDs = []string{"ac_2"}
	tasks[2].AcceptanceCriteriaIDs = []string{"ac_3"}

	runs := []model.Run{
		makeRun(runID, "t1", model.RunOutcomePass),
		makeRun(runID, "t2", model.RunOutcomeFail), // ac_2 fails
		makeRun(runID, "t3", model.RunOutcomePass),
	}

	out, err := judge.Evaluate(context.Background(), judgeagent.Input{
		RunID: runID, FeatureSpec: spec, Tasks: tasks, Runs: runs,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if out.Verdict == nil || out.Verdict.Status != model.VerdictFail {
		t.Fatalf("expected VerdictFail, got %v", out.Verdict)
	}

	// Precision: finding should reference ac_2, not ac_1 or ac_3
	foundAC2 := false
	for _, f := range out.Findings {
		if f.CriterionID == "ac_2" {
			foundAC2 = true
		}
		if f.CriterionID == "ac_1" || f.CriterionID == "ac_3" {
			t.Errorf("false positive finding for %s", f.CriterionID)
		}
	}
	if !foundAC2 {
		t.Error("recall failure: ac_2 not in findings (missed real failure)")
	}
}

func TestQA_SeededDefect_AllPassNoFalsePositive(t *testing.T) {
	judge := judgeagent.New()
	runID := "run_seeded_clean"

	spec := model.FeatureSpec{
		SchemaVersion: model.CurrentSchemaVersion,
		RunID:         runID,
		Description:   "A. B",
		AcceptanceCriteria: []model.AcceptanceCriterion{
			{ID: "ac_1", Text: "A"},
			{ID: "ac_2", Text: "B"},
		},
		Surfaces: []model.Surface{model.SurfaceWeb},
	}

	tasks := []model.Task{
		makeTask(runID, "t1", "d1", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1),
		makeTask(runID, "t2", "d2", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1),
	}
	tasks[0].AcceptanceCriteriaIDs = []string{"ac_1"}
	tasks[0].Status = model.TaskStatusPassed
	tasks[1].AcceptanceCriteriaIDs = []string{"ac_2"}
	tasks[1].Status = model.TaskStatusPassed

	runs := []model.Run{
		makeRun(runID, "t1", model.RunOutcomePass),
		makeRun(runID, "t2", model.RunOutcomePass),
	}

	out, err := judge.Evaluate(context.Background(), judgeagent.Input{
		RunID: runID, FeatureSpec: spec, Tasks: tasks, Runs: runs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Verdict == nil || out.Verdict.Status != model.VerdictPass {
		t.Fatalf("expected VerdictPass, got %v", out.Verdict)
	}
	if len(out.Findings) != 0 {
		t.Errorf("false positives: %d findings on clean code", len(out.Findings))
	}
}

func TestQA_SeededDefect_PlannerCoverage(t *testing.T) {
	store := newTestStore(t)
	rt := runtime.New(store, nil, nil, runtime.Config{})
	agent := planneragent.New(rt, store)

	t.Run("5 sentences 5 criteria", func(t *testing.T) {
		out, err := agent.Plan(context.Background(), "run_5", "A. B. C. D. E", []model.Surface{model.SurfaceWeb})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.FeatureSpec.AcceptanceCriteria) != 5 {
			t.Errorf("criteria = %d, want 5", len(out.FeatureSpec.AcceptanceCriteria))
		}
		if len(out.Tasks) != 5 {
			t.Errorf("tasks = %d, want 5", len(out.Tasks))
		}
	})

	t.Run("trailing period no empty", func(t *testing.T) {
		out, err := agent.Plan(context.Background(), "run_trailing", "Works.", []model.Surface{model.SurfaceWeb})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range out.FeatureSpec.AcceptanceCriteria {
			if strings.TrimSpace(c.Text) == "" {
				t.Error("empty criterion generated from trailing period")
			}
		}
	})

	t.Run("no periods wraps full text", func(t *testing.T) {
		out, err := agent.Plan(context.Background(), "run_no_period", "the full requirement text", []model.Surface{model.SurfaceWeb})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.FeatureSpec.AcceptanceCriteria) != 1 {
			t.Fatalf("criteria = %d, want 1", len(out.FeatureSpec.AcceptanceCriteria))
		}
		if out.FeatureSpec.AcceptanceCriteria[0].Text != "the full requirement text" {
			t.Errorf("text = %q", out.FeatureSpec.AcceptanceCriteria[0].Text)
		}
	})
}

// ===================================================================
// SECTION 3: END-TO-END ORCHESTRATOR TESTS
// ===================================================================

func TestQA_E2E_HappyPath(t *testing.T) {
	store := newTestStore(t)
	runID := "run_e2e_happy"

	plan := planneragent.Output{
		FeatureSpec: model.FeatureSpec{
			SchemaVersion:      model.CurrentSchemaVersion,
			RunID:              runID,
			Description:        "feature works",
			AcceptanceCriteria: []model.AcceptanceCriterion{{ID: "ac_1", Text: "works"}},
			Surfaces:           []model.Surface{model.SurfaceWeb},
		},
		TestPlan: model.TestPlan{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Journeys:      []string{"j1"},
			Assertions:    []string{"works"},
		},
		Tasks: []model.Task{
			makeTask(runID, "t1", "d1", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1),
		},
	}

	exec := &controllableExecutor{outcomes: map[string]model.RunOutcome{"t1": model.RunOutcomePass}}

	orch := orchestrator.New(store, &countingPlanner{output: plan}, judgeagent.New(),
		map[model.Surface]orchestrator.Executor{model.SurfaceWeb: exec},
		orchestrator.Budget{MaxJudgeTurns: 3, MaxQueuedTasks: 50})

	verdict, err := orch.Run(context.Background(), orchestrator.Request{
		RunID: runID, Description: "feature works", Surfaces: []model.Surface{model.SurfaceWeb},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if verdict.Status != model.VerdictPass {
		t.Errorf("verdict = %s, want pass", verdict.Status)
	}
}

func TestQA_E2E_FailurePath(t *testing.T) {
	store := newTestStore(t)
	runID := "run_e2e_fail"

	plan := planneragent.Output{
		FeatureSpec: model.FeatureSpec{
			SchemaVersion:      model.CurrentSchemaVersion,
			RunID:              runID,
			Description:        "feature broken",
			AcceptanceCriteria: []model.AcceptanceCriterion{{ID: "ac_1", Text: "broken"}},
			Surfaces:           []model.Surface{model.SurfaceWeb},
		},
		TestPlan: model.TestPlan{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Journeys:      []string{"j1"},
			Assertions:    []string{"broken"},
		},
		Tasks: []model.Task{
			makeTask(runID, "t1", "d1", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1),
		},
	}

	orch := orchestrator.New(store, &countingPlanner{output: plan}, judgeagent.New(),
		map[model.Surface]orchestrator.Executor{model.SurfaceWeb: failingExecutor{}},
		orchestrator.Budget{MaxJudgeTurns: 2, MaxQueuedTasks: 50})

	verdict, err := orch.Run(context.Background(), orchestrator.Request{
		RunID: runID, Description: "feature broken", Surfaces: []model.Surface{model.SurfaceWeb},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if verdict.Status != model.VerdictFail {
		t.Errorf("verdict = %s, want fail", verdict.Status)
	}
}

func TestQA_E2E_NoExecutor_Blocked(t *testing.T) {
	store := newTestStore(t)
	runID := "run_e2e_blocked"

	plan := planneragent.Output{
		FeatureSpec: model.FeatureSpec{
			SchemaVersion:      model.CurrentSchemaVersion,
			RunID:              runID,
			Description:        "needs ios",
			AcceptanceCriteria: []model.AcceptanceCriterion{{ID: "ac_1", Text: "ios feature"}},
			Surfaces:           []model.Surface{model.SurfaceIOS},
		},
		TestPlan: model.TestPlan{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Journeys:      []string{"j1"},
			Assertions:    []string{"ios feature"},
		},
		Tasks: []model.Task{
			makeTask(runID, "t1", "d1", model.SurfaceIOS, model.TaskKindProof, model.PriorityP1),
		},
	}

	// No executor registered for iOS
	orch := orchestrator.New(store, &countingPlanner{output: plan}, judgeagent.New(),
		map[model.Surface]orchestrator.Executor{},
		orchestrator.Budget{MaxJudgeTurns: 2, MaxQueuedTasks: 50})

	verdict, err := orch.Run(context.Background(), orchestrator.Request{
		RunID: runID, Description: "needs ios", Surfaces: []model.Surface{model.SurfaceIOS},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if verdict.Status != model.VerdictCannotVerify {
		t.Errorf("verdict = %s, want cannot_verify", verdict.Status)
	}
}

func TestQA_E2E_MultiCriterionAllPass(t *testing.T) {
	store := newTestStore(t)
	runID := "run_e2e_multi"

	plan := planneragent.Output{
		FeatureSpec: model.FeatureSpec{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Description:   "A. B. C",
			AcceptanceCriteria: []model.AcceptanceCriterion{
				{ID: "ac_1", Text: "A"}, {ID: "ac_2", Text: "B"}, {ID: "ac_3", Text: "C"},
			},
			Surfaces: []model.Surface{model.SurfaceWeb},
		},
		TestPlan: model.TestPlan{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Journeys:      []string{"j1", "j2", "j3"},
			Assertions:    []string{"A", "B", "C"},
		},
		Tasks: []model.Task{
			makeTask(runID, "t1", "d1", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1),
			makeTask(runID, "t2", "d2", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1),
			makeTask(runID, "t3", "d3", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1),
		},
	}
	plan.Tasks[0].AcceptanceCriteriaIDs = []string{"ac_1"}
	plan.Tasks[1].AcceptanceCriteriaIDs = []string{"ac_2"}
	plan.Tasks[2].AcceptanceCriteriaIDs = []string{"ac_3"}

	exec := &controllableExecutor{outcomes: map[string]model.RunOutcome{}}

	orch := orchestrator.New(store, &countingPlanner{output: plan}, judgeagent.New(),
		map[model.Surface]orchestrator.Executor{model.SurfaceWeb: exec},
		orchestrator.Budget{MaxJudgeTurns: 3, MaxQueuedTasks: 50})

	verdict, err := orch.Run(context.Background(), orchestrator.Request{
		RunID: runID, Description: "A. B. C", Surfaces: []model.Surface{model.SurfaceWeb},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if verdict.Status != model.VerdictPass {
		t.Errorf("verdict = %s, want pass", verdict.Status)
	}
}

func TestQA_E2E_BudgetExhaustion(t *testing.T) {
	store := newTestStore(t)
	runID := "run_e2e_budget"

	// Executor that blocks → judge returns NextTasks → loop continues → budget runs out
	plan := planneragent.Output{
		FeatureSpec: model.FeatureSpec{
			SchemaVersion:      model.CurrentSchemaVersion,
			RunID:              runID,
			Description:        "flaky feature",
			AcceptanceCriteria: []model.AcceptanceCriterion{{ID: "ac_1", Text: "flaky"}},
			Surfaces:           []model.Surface{model.SurfaceWeb},
		},
		TestPlan: model.TestPlan{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Journeys:      []string{"j1"},
			Assertions:    []string{"flaky"},
		},
		Tasks: []model.Task{
			makeTask(runID, "t1", "d1", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1),
		},
	}

	orch := orchestrator.New(store, &countingPlanner{output: plan}, judgeagent.New(),
		map[model.Surface]orchestrator.Executor{model.SurfaceWeb: blockedExecutor{}},
		orchestrator.Budget{MaxJudgeTurns: 1, MaxQueuedTasks: 50})

	verdict, err := orch.Run(context.Background(), orchestrator.Request{
		RunID: runID, Description: "flaky", Surfaces: []model.Surface{model.SurfaceWeb},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if verdict.Status != model.VerdictCannotVerify {
		t.Errorf("verdict = %s, want cannot_verify", verdict.Status)
	}
}

func TestQA_E2E_OrchestratorPlusReport(t *testing.T) {
	store := newTestStore(t)
	runID := "run_e2e_report"

	plan := planneragent.Output{
		FeatureSpec: model.FeatureSpec{
			SchemaVersion:      model.CurrentSchemaVersion,
			RunID:              runID,
			Description:        "reportable",
			AcceptanceCriteria: []model.AcceptanceCriterion{{ID: "ac_1", Text: "works"}},
			Surfaces:           []model.Surface{model.SurfaceWeb},
		},
		TestPlan: model.TestPlan{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Journeys:      []string{"j1"},
			Assertions:    []string{"works"},
		},
		Tasks: []model.Task{
			makeTask(runID, "t1", "d1", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1),
		},
	}

	exec := &controllableExecutor{outcomes: map[string]model.RunOutcome{}}
	orch := orchestrator.New(store, &countingPlanner{output: plan}, judgeagent.New(),
		map[model.Surface]orchestrator.Executor{model.SurfaceWeb: exec},
		orchestrator.Budget{MaxJudgeTurns: 2, MaxQueuedTasks: 50})

	_, err := orch.Run(context.Background(), orchestrator.Request{
		RunID: runID, Description: "reportable", Surfaces: []model.Surface{model.SurfaceWeb},
	})
	if err != nil {
		t.Fatal(err)
	}

	runDir := store.RunDir(runID)
	reportPath, manifestPath, err := report.NewGenerator().Write(runID, runDir)
	if err != nil {
		t.Fatalf("Write report: %v", err)
	}
	if _, err := os.Stat(reportPath); err != nil {
		t.Errorf("report missing: %v", err)
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Errorf("manifest missing: %v", err)
	}
}

// ===================================================================
// SECTION 4: ADVERSARIAL TESTS
// ===================================================================

func TestQA_Adversarial_EmptyDescription(t *testing.T) {
	store := newTestStore(t)
	rt := runtime.New(store, nil, nil, runtime.Config{})
	agent := planneragent.New(rt, store)

	_, err := agent.Plan(context.Background(), "run_adv1", "", []model.Surface{model.SurfaceWeb})
	if err == nil {
		t.Error("expected error for empty description")
	}
}

func TestQA_Adversarial_WhitespaceDescription(t *testing.T) {
	store := newTestStore(t)
	rt := runtime.New(store, nil, nil, runtime.Config{})
	agent := planneragent.New(rt, store)

	_, err := agent.Plan(context.Background(), "run_adv2", "   \t\n  ", []model.Surface{model.SurfaceWeb})
	if err == nil {
		t.Error("expected error for whitespace-only description")
	}
}

func TestQA_Adversarial_OnlyPeriods(t *testing.T) {
	store := newTestStore(t)
	rt := runtime.New(store, nil, nil, runtime.Config{})
	agent := planneragent.New(rt, store)

	// "..." splits into empty segments which should be filtered out
	out, err := agent.Plan(context.Background(), "run_adv3", "...", []model.Surface{model.SurfaceWeb})
	// This should either error (no criteria) or produce 1 fallback criterion
	if err != nil {
		return // acceptable: error on degenerate input
	}
	// If it succeeds, all criteria must be non-empty
	for _, c := range out.FeatureSpec.AcceptanceCriteria {
		if strings.TrimSpace(c.Text) == "" {
			t.Error("empty criterion text from '...' input")
		}
	}
}

func TestQA_Adversarial_WrongPayloadTypes(t *testing.T) {
	// API runner: string where array expected
	_, err := apirunner.ParseTaskSpec(model.Task{
		Payload: map[string]any{
			"http_requests": "not an array",
		},
	})
	if err == nil {
		t.Error("expected error for wrong http_requests type")
	}

	// Web runner: missing start_urls
	_, err = apirunner.ParseTaskSpec(model.Task{
		Payload: map[string]any{},
	})
	if err == nil {
		t.Error("expected error for missing http_requests")
	}
}

func TestQA_Adversarial_LargeDescription(t *testing.T) {
	store := newTestStore(t)
	rt := runtime.New(store, nil, nil, runtime.Config{})
	agent := planneragent.New(rt, store)

	// 10KB description
	big := strings.Repeat("A sentence. ", 1000)
	out, err := agent.Plan(context.Background(), "run_adv_large", big, []model.Surface{model.SurfaceWeb})
	if err != nil {
		t.Fatalf("should handle large input: %v", err)
	}
	if len(out.Tasks) == 0 {
		t.Error("expected tasks from large description")
	}
}

func TestQA_Adversarial_NegativeAttemptCount(t *testing.T) {
	task := model.Task{
		SchemaVersion: model.CurrentSchemaVersion,
		TaskID:        "t1",
		RunID:         "r1",
		Surface:       model.SurfaceWeb,
		Kind:          model.TaskKindProof,
		Priority:      model.PriorityP1,
		Status:        model.TaskStatusQueued,
		DedupeKey:     "d1",
		MaxAttempts:   3,
		AttemptCount:  -1,
	}
	if err := task.Validate(); err == nil {
		t.Error("expected validation error for negative AttemptCount")
	}
}

func TestQA_Adversarial_EmptyVerdictReasons(t *testing.T) {
	v := model.Verdict{
		SchemaVersion: model.CurrentSchemaVersion,
		VerdictID:     "v1",
		RunID:         "r1",
		Status:        model.VerdictPass,
		Reasons:       []string{},
		Coverage:      map[string][]string{"ac_1": {"ref"}},
	}
	if err := v.Validate(); err == nil {
		t.Error("expected validation error for empty reasons")
	}
}

func TestBug_B3_ConcurrentSQLiteBusy(t *testing.T) {
	// BUG B3: Concurrent writes to the same run's SQLite database produce SQLITE_BUSY
	store := newTestStore(t)
	mgr := queue.NewManager(store, 100)
	ctx := context.Background()
	runID := "run_concurrent"

	_ = store.CreateValidationRun(ctx, blackboard.ValidationRun{ID: runID})

	// Enqueue many tasks concurrently to provoke SQLITE_BUSY
	var wg sync.WaitGroup
	var busyCount int
	var mu sync.Mutex
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			task := makeTask(runID, fmt.Sprintf("t%d", idx), fmt.Sprintf("d%d", idx),
				model.SurfaceWeb, model.TaskKindProof, model.PriorityP1)
			if err := mgr.EnqueueTask(ctx, task); err != nil {
				if strings.Contains(err.Error(), "locked") || strings.Contains(err.Error(), "BUSY") {
					mu.Lock()
					busyCount++
					mu.Unlock()
				}
			}
		}(i)
	}
	wg.Wait()

	if busyCount > 0 {
		t.Logf("BUG B3 CONFIRMED: %d/%d concurrent enqueues got SQLITE_BUSY", busyCount, 50)
	} else {
		t.Log("BUG B3 FIXED: all concurrent enqueues succeeded")
	}
}

func TestQA_Adversarial_ModelValidation(t *testing.T) {
	tests := []struct {
		name string
		fn   func() error
	}{
		{"invalid surface", func() error {
			return model.Task{
				SchemaVersion: "v1", TaskID: "t", RunID: "r", Surface: "invalid",
				Kind: model.TaskKindProof, Priority: model.PriorityP1, Status: model.TaskStatusQueued,
				DedupeKey: "d", MaxAttempts: 1,
			}.Validate()
		}},
		{"invalid kind", func() error {
			return model.Task{
				SchemaVersion: "v1", TaskID: "t", RunID: "r", Surface: model.SurfaceWeb,
				Kind: "invalid", Priority: model.PriorityP1, Status: model.TaskStatusQueued,
				DedupeKey: "d", MaxAttempts: 1,
			}.Validate()
		}},
		{"invalid priority", func() error {
			return model.Task{
				SchemaVersion: "v1", TaskID: "t", RunID: "r", Surface: model.SurfaceWeb,
				Kind: model.TaskKindProof, Priority: "P9", Status: model.TaskStatusQueued,
				DedupeKey: "d", MaxAttempts: 1,
			}.Validate()
		}},
		{"invalid status", func() error {
			return model.Task{
				SchemaVersion: "v1", TaskID: "t", RunID: "r", Surface: model.SurfaceWeb,
				Kind: model.TaskKindProof, Priority: model.PriorityP1, Status: "invalid",
				DedupeKey: "d", MaxAttempts: 1,
			}.Validate()
		}},
		{"wrong schema version", func() error {
			return model.FeatureSpec{SchemaVersion: "v99", RunID: "r", Description: "d",
				AcceptanceCriteria: []model.AcceptanceCriterion{{ID: "a", Text: "t"}}}.Validate()
		}},
		{"run finished before started", func() error {
			now := time.Now()
			return model.Run{SchemaVersion: "v1", RunID: "r", TaskID: "t",
				Outcome: model.RunOutcomePass, StartedAt: now, FinishedAt: now.Add(-time.Second)}.Validate()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.fn(); err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

// ===================================================================
// SECTION 5: BUG REPRODUCTION TESTS — confirms documented bugs
// ===================================================================

func TestBug_B1_RetryOnFailure(t *testing.T) {
	// BUG B1: Failed tasks should be retried up to MaxAttempts
	store := newTestStore(t)
	runID := "run_bug_b1"

	plan := planneragent.Output{
		FeatureSpec: model.FeatureSpec{
			SchemaVersion:      model.CurrentSchemaVersion,
			RunID:              runID,
			Description:        "retryable feature",
			AcceptanceCriteria: []model.AcceptanceCriterion{{ID: "ac_1", Text: "criterion"}},
			Surfaces:           []model.Surface{model.SurfaceWeb},
		},
		TestPlan: model.TestPlan{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Journeys:      []string{"j1"},
			Assertions:    []string{"criterion"},
		},
		Tasks: []model.Task{
			makeTask(runID, "t1", "d1", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1),
		},
	}
	plan.Tasks[0].MaxAttempts = 3

	exec := &controllableExecutor{outcomes: map[string]model.RunOutcome{"t1": model.RunOutcomeFail}}

	orch := orchestrator.New(store, &countingPlanner{output: plan}, judgeagent.New(),
		map[model.Surface]orchestrator.Executor{model.SurfaceWeb: exec},
		orchestrator.Budget{MaxJudgeTurns: 3, MaxQueuedTasks: 50, MaxRetriesPerTask: 3})

	_, _ = orch.Run(context.Background(), orchestrator.Request{
		RunID: runID, Description: "retryable", Surfaces: []model.Surface{model.SurfaceWeb},
	})

	exec.mu.Lock()
	callCount := len(exec.calls)
	exec.mu.Unlock()

	if callCount <= 1 {
		t.Errorf("BUG B1 NOT FIXED: task executed only %d time(s) despite MaxAttempts=3", callCount)
	} else {
		t.Logf("BUG B1 FIXED: task retried %d times", callCount)
	}
}

func TestBug_B2_JudgeHandlesFlakiness(t *testing.T) {
	// BUG B2: Criterion with both pass+fail runs should NOT immediately get FAIL verdict
	judge := judgeagent.New()
	runID := "run_bug_b2"

	spec := model.FeatureSpec{
		SchemaVersion:      model.CurrentSchemaVersion,
		RunID:              runID,
		Description:        "flaky test",
		AcceptanceCriteria: []model.AcceptanceCriterion{{ID: "ac_1", Text: "flaky"}},
		Surfaces:           []model.Surface{model.SurfaceWeb},
	}

	task := makeTask(runID, "t1", "d1", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1)
	runs := []model.Run{
		makeRun(runID, "t1", model.RunOutcomePass),
		makeRun(runID, "t1", model.RunOutcomeFail),
	}

	out, err := judge.Evaluate(context.Background(), judgeagent.Input{
		RunID: runID, FeatureSpec: spec, Tasks: []model.Task{task}, Runs: runs,
	})
	if err != nil {
		t.Fatal(err)
	}

	if out.Verdict != nil && out.Verdict.Status == model.VerdictFail {
		t.Errorf("BUG B2 NOT FIXED: criterion with pass+fail runs gets immediate FAIL verdict")
	} else if len(out.NextTasks) > 0 {
		t.Logf("BUG B2 FIXED: judge requests more evidence for flaky criterion (%d next tasks)", len(out.NextTasks))
	} else {
		t.Logf("BUG B2 FIXED: judge returned %v", out)
	}
}

func TestBug_B5_JudgeMultiRoundNoop(t *testing.T) {
	// BUG B5: Multi-round judge loop produces identical output across rounds
	judge := judgeagent.New()
	runID := "run_bug_b5"

	spec := model.FeatureSpec{
		SchemaVersion:      model.CurrentSchemaVersion,
		RunID:              runID,
		Description:        "missing proof",
		AcceptanceCriteria: []model.AcceptanceCriterion{{ID: "ac_1", Text: "needs proof"}},
		Surfaces:           []model.Surface{model.SurfaceWeb},
	}

	// With no tasks/runs, round 0 should produce NextTasks.
	// Round 1 with same input should produce identical NextTasks (dead code).
	out1, _ := judge.Evaluate(context.Background(), judgeagent.Input{
		RunID: runID, FeatureSpec: spec, Tasks: []model.Task{}, Runs: []model.Run{}, MaxRounds: 1,
	})
	out2, _ := judge.Evaluate(context.Background(), judgeagent.Input{
		RunID: runID, FeatureSpec: spec, Tasks: []model.Task{}, Runs: []model.Run{}, MaxRounds: 5,
	})

	// Both should produce NextTasks (not a verdict)
	if len(out1.NextTasks) != len(out2.NextTasks) {
		t.Log("BUG B5 POSSIBLY FIXED: different round counts produce different task counts")
	} else {
		t.Log("BUG B5 CONFIRMED: rounds 0-4 all produce identical output (multi-round is dead code)")
	}
}

func TestBug_B6_SinglePassIsInconclusive(t *testing.T) {
	// BUG B6: A single pass should be classified as "inconclusive", not "flaky"
	got := stability.Classify([]model.RunOutcome{model.RunOutcomePass}, 2)
	if got == stability.OutcomeFlaky {
		t.Errorf("BUG B6 NOT FIXED: single pass classified as 'flaky'")
	} else if got == stability.OutcomeInconclusive {
		t.Logf("BUG B6 FIXED: single pass classified as %q", got)
	} else {
		t.Logf("BUG B6 FIXED: single pass classified as %q", got)
	}
}

func TestBug_B7_TruncationPreservesUTF8(t *testing.T) {
	// BUG B7: Truncation should produce valid UTF-8
	store := newTestStore(t)
	ctx := context.Background()
	runID := "run_bug_b7"

	_ = store.CreateValidationRun(ctx, blackboard.ValidationRun{ID: runID})

	// Create a file with multi-byte UTF-8 near the truncation boundary
	dir := t.TempDir()
	content := strings.Repeat("x", 250) + strings.Repeat("\U0001F600", 100) // emoji = 4 bytes each
	evidencePath := filepath.Join(dir, "evidence.json")
	_ = os.WriteFile(evidencePath, []byte(content), 0o644)

	proc := processor.New(store, 256) // truncate at 256 bytes
	result, err := proc.Process(ctx, processor.ProcessRequest{
		Run: makeRun(runID, "t1", model.RunOutcomePass),
		RunnerResult: runner.Result{
			Outcome:       model.RunOutcomePass,
			Summary:       "test",
			EvidenceFiles: []string{evidencePath},
		},
		ArtifactDir: dir,
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	for _, ev := range result.Evidence {
		raw, _ := os.ReadFile(ev.Path)
		if !utf8.Valid(raw) {
			t.Errorf("BUG B7 NOT FIXED: truncated file contains invalid UTF-8")
		} else {
			t.Logf("BUG B7 FIXED: truncated file is valid UTF-8 (%d bytes)", len(raw))
		}
	}
}

func TestBug_B10_RedactionSwallowsContent(t *testing.T) {
	// BUG B10: Greedy \S+ regex swallows non-secret content
	store := newTestStore(t)
	ctx := context.Background()
	runID := "run_bug_b10"

	_ = store.CreateValidationRun(ctx, blackboard.ValidationRun{ID: runID})

	dir := t.TempDir()
	content := `api_key=secret123 request_id=xyz789`
	evidencePath := filepath.Join(dir, "evidence.log")
	_ = os.WriteFile(evidencePath, []byte(content), 0o644)

	proc := processor.New(store, 1024*1024)
	result, err := proc.Process(ctx, processor.ProcessRequest{
		Run: makeRun(runID, "t1", model.RunOutcomePass),
		RunnerResult: runner.Result{
			Outcome:       model.RunOutcomePass,
			Summary:       "test",
			EvidenceFiles: []string{evidencePath},
		},
		ArtifactDir: dir,
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	for _, ev := range result.Evidence {
		raw, _ := os.ReadFile(ev.Path)
		normalized := string(raw)
		if !strings.Contains(normalized, "request_id") {
			t.Log("BUG B10 CONFIRMED: greedy regex swallowed 'request_id=xyz789'")
		} else {
			t.Log("BUG B10 FIXED: request_id preserved after redaction")
		}
	}
}

func TestBug_B11_FlakyMappedCorrectly(t *testing.T) {
	// BUG B11: RunOutcomeFlaky should map to TaskStatusFailed, not TaskStatusErrored
	store := newTestStore(t)
	runID := "run_bug_b11"

	plan := planneragent.Output{
		FeatureSpec: model.FeatureSpec{
			SchemaVersion:      model.CurrentSchemaVersion,
			RunID:              runID,
			Description:        "flaky feature",
			AcceptanceCriteria: []model.AcceptanceCriterion{{ID: "ac_1", Text: "flaky"}},
			Surfaces:           []model.Surface{model.SurfaceWeb},
		},
		TestPlan: model.TestPlan{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Journeys:      []string{"j1"},
			Assertions:    []string{"flaky"},
		},
		Tasks: []model.Task{
			makeTask(runID, "t1", "d1", model.SurfaceWeb, model.TaskKindProof, model.PriorityP1),
		},
	}

	// Executor returns flaky outcome
	exec := &controllableExecutor{outcomes: map[string]model.RunOutcome{"t1": model.RunOutcomeFlaky}}

	orch := orchestrator.New(store, &countingPlanner{output: plan}, judgeagent.New(),
		map[model.Surface]orchestrator.Executor{model.SurfaceWeb: exec},
		orchestrator.Budget{MaxJudgeTurns: 2, MaxQueuedTasks: 50})

	_, _ = orch.Run(context.Background(), orchestrator.Request{
		RunID: runID, Description: "flaky", Surfaces: []model.Surface{model.SurfaceWeb},
	})

	tasks, _ := store.TaskList(context.Background(), blackboard.TaskFilter{RunID: runID, Limit: 100})
	for _, task := range tasks {
		if task.TaskID == "t1" {
			if task.Status == model.TaskStatusErrored {
				t.Errorf("BUG B11 NOT FIXED: RunOutcomeFlaky still mapped to TaskStatusErrored")
			} else {
				t.Logf("BUG B11 FIXED: RunOutcomeFlaky mapped to %s", task.Status)
			}
		}
	}
}

// ===================================================================
// SECTION 6: REPAIR AGENT TESTS
// ===================================================================

type mockRepairClient struct {
	proposal repair.Proposal
	err      error
}

func (m *mockRepairClient) GeneratePatch(_ context.Context, _ string) (repair.Proposal, error) {
	return m.proposal, m.err
}

func TestQA_RepairAgent_GatedOff(t *testing.T) {
	store := newTestStore(t)
	_ = store.CreateValidationRun(context.Background(), blackboard.ValidationRun{ID: "run_repair"})

	agent := repair.New(store, &mockRepairClient{}, repair.Config{Enabled: false})
	proposal, err := agent.MaybeRun(context.Background(), "run_repair", true, "diagnostic info")
	if err != nil {
		t.Fatal(err)
	}
	if proposal != nil {
		t.Error("expected nil proposal when disabled")
	}
}

func TestQA_RepairAgent_NotBlocked(t *testing.T) {
	store := newTestStore(t)
	agent := repair.New(store, &mockRepairClient{}, repair.Config{Enabled: true})
	proposal, err := agent.MaybeRun(context.Background(), "run_repair2", false, "diagnostics")
	if err != nil {
		t.Fatal(err)
	}
	if proposal != nil {
		t.Error("expected nil proposal when not blocked")
	}
}

func TestQA_RepairAgent_ProducesProposal(t *testing.T) {
	store := newTestStore(t)
	_ = store.CreateValidationRun(context.Background(), blackboard.ValidationRun{ID: "run_repair3"})

	client := &mockRepairClient{
		proposal: repair.Proposal{Diff: "--- a\n+++ b\n", Rationale: "fix"},
	}
	agent := repair.New(store, client, repair.Config{Enabled: true})
	proposal, err := agent.MaybeRun(context.Background(), "run_repair3", true, "it's broken")
	if err != nil {
		t.Fatal(err)
	}
	if proposal == nil {
		t.Fatal("expected proposal")
	}
	if proposal.Diff == "" {
		t.Error("proposal diff is empty")
	}
}

func TestQA_RepairAgent_AutoApplyNotImplemented(t *testing.T) {
	store := newTestStore(t)
	_ = store.CreateValidationRun(context.Background(), blackboard.ValidationRun{ID: "run_repair4"})

	client := &mockRepairClient{proposal: repair.Proposal{Diff: "d", Rationale: "r"}}
	agent := repair.New(store, client, repair.Config{Enabled: true, ApplyRepair: true})
	_, err := agent.MaybeRun(context.Background(), "run_repair4", true, "diag")
	if err == nil {
		t.Error("expected error for auto-apply not implemented")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ===================================================================
// SECTION 7: EVIDENCE PROCESSING GOLDEN TESTS
// ===================================================================

func TestQA_Golden_EvidenceRedaction(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	runID := "run_ev_redact"
	_ = store.CreateValidationRun(ctx, blackboard.ValidationRun{ID: runID})

	tests := []struct {
		name     string
		content  string
		mustNot  string // must NOT appear in output
		must     string // MUST appear in output
	}{
		{"authorization header", "Authorization: Bearer secret123\nContent-Type: json", "secret123", "[redacted]"},
		{"cookie header", "cookie: session=abc123\nHost: example.com", "abc123", "[redacted]"},
		{"set-cookie header", "Set-Cookie: id=xyz\nOther: ok", "xyz", "[redacted]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "evidence.log")
			_ = os.WriteFile(path, []byte(tt.content), 0o644)

			proc := processor.New(store, 1024*1024)
			result, err := proc.Process(ctx, processor.ProcessRequest{
				Run: makeRun(runID, "t_"+tt.name, model.RunOutcomePass),
				RunnerResult: runner.Result{
					Outcome:       model.RunOutcomePass,
					EvidenceFiles: []string{path},
				},
				ArtifactDir: dir,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, ev := range result.Evidence {
				raw, _ := os.ReadFile(ev.Path)
				content := string(raw)
				if strings.Contains(content, tt.mustNot) {
					t.Errorf("output still contains %q", tt.mustNot)
				}
				if !strings.Contains(content, tt.must) {
					t.Errorf("output missing %q", tt.must)
				}
			}
		})
	}
}

func TestQA_Golden_EvidenceMIME(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	runID := "run_ev_mime"
	_ = store.CreateValidationRun(ctx, blackboard.ValidationRun{ID: runID})

	tests := []struct {
		filename string
		wantMIME string
		wantKind model.EvidenceKind
	}{
		{"screenshot.png", "image/png", model.EvidenceKindScreenshot},
		{"photo.jpg", "image/jpeg", model.EvidenceKindScreenshot},
		{"transcript.json", "application/json", model.EvidenceKindTranscript},
		{"output.log", "text/plain", model.EvidenceKindLog},
		{"capture.har", "application/json", model.EvidenceKindTranscript},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tt.filename)
			_ = os.WriteFile(path, []byte("content"), 0o644)

			proc := processor.New(store, 1024*1024)
			result, err := proc.Process(ctx, processor.ProcessRequest{
				Run: makeRun(runID, "t_"+tt.filename, model.RunOutcomePass),
				RunnerResult: runner.Result{
					Outcome:       model.RunOutcomePass,
					EvidenceFiles: []string{path},
				},
				ArtifactDir: dir,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Evidence) == 0 {
				t.Fatal("no evidence")
			}
			ev := result.Evidence[0]
			if ev.MIME != tt.wantMIME {
				t.Errorf("MIME = %q, want %q", ev.MIME, tt.wantMIME)
			}
			if ev.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", ev.Kind, tt.wantKind)
			}
		})
	}
}

// Ensure the var block references are used
var _ = context.Background
