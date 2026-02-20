package benchmarks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	judgeagent "qa-agent/internal/agents/judge"
	planneragent "qa-agent/internal/agents/planner"
	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
	"qa-agent/internal/orchestrator"
	"qa-agent/internal/runner"
	apirunner "qa-agent/internal/runner/api"
	iosrunner "qa-agent/internal/runner/ios"
	"qa-agent/internal/sandbox"
)

// =========================================================================
// TEST HELPERS
// =========================================================================

func newTestStore(t *testing.T) *blackboard.Store {
	t.Helper()
	store, err := blackboard.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

type fixedPlanner struct {
	output planneragent.Output
}

func (p *fixedPlanner) Plan(_ context.Context, _ string, _ string, _ []model.Surface) (planneragent.Output, error) {
	return p.output, nil
}

// apiExecutor wraps the real API runner.
type apiExecutor struct {
	adapter *apirunner.Adapter
}

func (a *apiExecutor) Run(ctx context.Context, task model.Task, env sandbox.Sandbox, artifactDir string) (runner.Result, error) {
	return a.adapter.Run(ctx, task, env, artifactDir)
}

// iosExecutor wraps the real iOS stub runner.
type iosExecutor struct {
	adapter *iosrunner.Adapter
}

func (a *iosExecutor) Run(ctx context.Context, task model.Task, env sandbox.Sandbox, artifactDir string) (runner.Result, error) {
	return a.adapter.Run(ctx, task, env, artifactDir)
}

// webAdapterExecutor calls the ai-browser-use adapter as a subprocess.
type webAdapterExecutor struct {
	adapterPath string
	pythonPath  string
}

func (w *webAdapterExecutor) Run(_ context.Context, task model.Task, _ sandbox.Sandbox, artifactDir string) (runner.Result, error) {
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return runner.Result{}, err
	}

	// Write CLIInput
	cliInput := runner.CLIInput{
		Task:        task,
		ArtifactDir: artifactDir,
	}
	inputRaw, err := json.MarshalIndent(cliInput, "", "  ")
	if err != nil {
		return runner.Result{}, err
	}
	inputPath := filepath.Join(artifactDir, task.TaskID+"-input.json")
	outputPath := filepath.Join(artifactDir, task.TaskID+"-output.json")
	if err := os.WriteFile(inputPath, inputRaw, 0o644); err != nil {
		return runner.Result{}, err
	}

	// Call adapter
	cmd := exec.Command(w.pythonPath, w.adapterPath, "run", "--input", inputPath, "--output", outputPath)
	cmd.Dir = artifactDir
	cmdOut, cmdErr := cmd.CombinedOutput()

	// Read output
	outputRaw, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		if cmdErr != nil {
			return runner.Result{}, fmt.Errorf("adapter error: %w (output: %s)", cmdErr, string(cmdOut))
		}
		return runner.Result{}, fmt.Errorf("output file not found: %w", readErr)
	}

	var cliOutput runner.CLIOutput
	if err := json.Unmarshal(outputRaw, &cliOutput); err != nil {
		return runner.Result{}, fmt.Errorf("invalid adapter output: %w (raw: %s)", err, string(outputRaw))
	}

	return runner.Result{
		Outcome:        cliOutput.Outcome,
		Summary:        cliOutput.Summary,
		EvidenceFiles:  cliOutput.EvidenceFiles,
		StabilityHints: cliOutput.StabilityHints,
	}, nil
}

// macosAdapterExecutor calls the ai-computer-use-adapter as a subprocess.
type macosAdapterExecutor struct {
	adapterPath string
	pythonPath  string
}

func (m *macosAdapterExecutor) Run(_ context.Context, task model.Task, _ sandbox.Sandbox, artifactDir string) (runner.Result, error) {
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return runner.Result{}, err
	}

	cliInput := runner.CLIInput{
		Task:        task,
		ArtifactDir: artifactDir,
	}
	inputRaw, err := json.MarshalIndent(cliInput, "", "  ")
	if err != nil {
		return runner.Result{}, err
	}
	inputPath := filepath.Join(artifactDir, task.TaskID+"-input.json")
	outputPath := filepath.Join(artifactDir, task.TaskID+"-output.json")
	if err := os.WriteFile(inputPath, inputRaw, 0o644); err != nil {
		return runner.Result{}, err
	}

	cmd := exec.Command(m.pythonPath, m.adapterPath, "run", "--input", inputPath, "--output", outputPath)
	cmd.Dir = artifactDir
	cmd.Env = os.Environ() // inherit env for ANTHROPIC_API_KEY
	cmdOut, cmdErr := cmd.CombinedOutput()

	outputRaw, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		if cmdErr != nil {
			return runner.Result{}, fmt.Errorf("adapter error: %w (output: %s)", cmdErr, string(cmdOut))
		}
		return runner.Result{}, fmt.Errorf("output file not found: %w", readErr)
	}

	var cliOutput runner.CLIOutput
	if err := json.Unmarshal(outputRaw, &cliOutput); err != nil {
		return runner.Result{}, fmt.Errorf("invalid adapter output: %w", err)
	}

	return runner.Result{
		Outcome:        cliOutput.Outcome,
		Summary:        cliOutput.Summary,
		EvidenceFiles:  cliOutput.EvidenceFiles,
		StabilityHints: cliOutput.StabilityHints,
	}, nil
}

// benchScenario describes a single benchmark test case.
type benchScenario struct {
	name          string
	description   string
	tasks         []model.Task
	surface       model.Surface
	expectVerdict model.VerdictStatus
	expectBugs    []string
}

// runPipeline runs a single scenario through the orchestrator.
func runPipeline(t *testing.T, scenario benchScenario, executors map[model.Surface]orchestrator.Executor) model.Verdict {
	t.Helper()

	store := newTestStore(t)
	runID := fmt.Sprintf("bench_%s_%d", scenario.name, time.Now().UnixNano())

	tasks := make([]model.Task, len(scenario.tasks))
	for i, task := range scenario.tasks {
		task.RunID = runID
		tasks[i] = task
	}

	plan := planneragent.Output{
		FeatureSpec: model.FeatureSpec{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Description:   scenario.description,
			AcceptanceCriteria: []model.AcceptanceCriterion{
				{ID: "ac_1", Text: scenario.description},
			},
			Surfaces: []model.Surface{scenario.surface},
		},
		TestPlan: model.TestPlan{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Journeys:      []string{"benchmark_journey"},
			Assertions:    []string{scenario.description},
		},
		Tasks: tasks,
	}

	orch := orchestrator.New(
		store,
		&fixedPlanner{output: plan},
		judgeagent.New(),
		executors,
		orchestrator.Budget{
			MaxJudgeTurns:  3,
			MaxQueuedTasks: 50,
		},
	)

	verdict, err := orch.Run(context.Background(), orchestrator.Request{
		RunID:       runID,
		Description: scenario.description,
		Surfaces:    []model.Surface{scenario.surface},
	})
	if err != nil {
		t.Fatalf("[%s] orchestrator error: %v", scenario.name, err)
	}
	return verdict
}

// =========================================================================
// TASK CONSTRUCTORS
// =========================================================================

func apiTask(taskID, runID, dedupe, url, method string, expectStatus int, expectBody string) model.Task {
	return model.Task{
		SchemaVersion:         model.CurrentSchemaVersion,
		TaskID:                taskID,
		RunID:                 runID,
		Surface:               model.SurfaceAPI,
		Kind:                  model.TaskKindProof,
		Priority:              model.PriorityP1,
		Status:                model.TaskStatusQueued,
		DedupeKey:             dedupe,
		MaxAttempts:           1,
		CreatedBy:             "benchmark",
		AcceptanceCriteriaIDs: []string{"ac_1"},
		Payload: map[string]any{
			"http_requests": []any{
				map[string]any{
					"method":               method,
					"url":                  url,
					"expect_status":        float64(expectStatus),
					"expect_body_contains": expectBody,
				},
			},
		},
	}
}

func apiTaskWithBody(taskID, runID, dedupe, url, method, body string, expectStatus int, expectBody string) model.Task {
	return model.Task{
		SchemaVersion:         model.CurrentSchemaVersion,
		TaskID:                taskID,
		RunID:                 runID,
		Surface:               model.SurfaceAPI,
		Kind:                  model.TaskKindProof,
		Priority:              model.PriorityP1,
		Status:                model.TaskStatusQueued,
		DedupeKey:             dedupe,
		MaxAttempts:           1,
		CreatedBy:             "benchmark",
		AcceptanceCriteriaIDs: []string{"ac_1"},
		Payload: map[string]any{
			"http_requests": []any{
				map[string]any{
					"method":               method,
					"url":                  url,
					"body":                 body,
					"expect_status":        float64(expectStatus),
					"expect_body_contains": expectBody,
					"headers": map[string]any{
						"Content-Type": "application/json",
					},
				},
			},
		},
	}
}

func apiTaskWithAuth(taskID, runID, dedupe, url, method, auth string, expectStatus int, expectBody string) model.Task {
	return model.Task{
		SchemaVersion:         model.CurrentSchemaVersion,
		TaskID:                taskID,
		RunID:                 runID,
		Surface:               model.SurfaceAPI,
		Kind:                  model.TaskKindProof,
		Priority:              model.PriorityP1,
		Status:                model.TaskStatusQueued,
		DedupeKey:             dedupe,
		MaxAttempts:           1,
		CreatedBy:             "benchmark",
		AcceptanceCriteriaIDs: []string{"ac_1"},
		Payload: map[string]any{
			"http_requests": []any{
				map[string]any{
					"method":               method,
					"url":                  url,
					"expect_status":        float64(expectStatus),
					"expect_body_contains": expectBody,
					"headers": map[string]any{
						"Authorization": auth,
					},
				},
			},
		},
	}
}

func webTask(taskID, runID, dedupe string, startURLs, steps, assertions []string) model.Task {
	urls := make([]any, len(startURLs))
	for i, u := range startURLs {
		urls[i] = u
	}
	s := make([]any, len(steps))
	for i, v := range steps {
		s[i] = v
	}
	a := make([]any, len(assertions))
	for i, v := range assertions {
		a[i] = v
	}
	return model.Task{
		SchemaVersion:         model.CurrentSchemaVersion,
		TaskID:                taskID,
		RunID:                 runID,
		Surface:               model.SurfaceWeb,
		Kind:                  model.TaskKindProof,
		Priority:              model.PriorityP1,
		Status:                model.TaskStatusQueued,
		DedupeKey:             dedupe,
		MaxAttempts:           1,
		CreatedBy:             "benchmark",
		AcceptanceCriteriaIDs: []string{"ac_1"},
		Payload: map[string]any{
			"start_urls": urls,
			"steps":      s,
			"assertions": a,
		},
	}
}

func macosTask(taskID, runID, dedupe, bundleID, appName, appPath string, steps, assertions []string, uiContext map[string]interface{}) model.Task {
	s := make([]any, len(steps))
	for i, v := range steps {
		s[i] = v
	}
	a := make([]any, len(assertions))
	for i, v := range assertions {
		a[i] = v
	}
	payload := map[string]any{
		"app_bundle_id":         bundleID,
		"steps":                 s,
		"assertions":            a,
		"max_steps":             float64(50),
		"max_wall_time_seconds": float64(300),
	}
	if appPath != "" {
		payload["app_path"] = appPath
	}
	if appName != "" {
		payload["app_name"] = appName
	}
	if uiContext != nil {
		payload["ui_context"] = uiContext
	}
	return model.Task{
		SchemaVersion:         model.CurrentSchemaVersion,
		TaskID:                taskID,
		RunID:                 runID,
		Surface:               model.SurfaceMacOS,
		Kind:                  model.TaskKindProof,
		Priority:              model.PriorityP1,
		Status:                model.TaskStatusQueued,
		DedupeKey:             dedupe,
		MaxAttempts:           1,
		CreatedBy:             "benchmark",
		AcceptanceCriteriaIDs: []string{"ac_1"},
		Payload:               payload,
	}
}

func iosTask(taskID, runID, dedupe, bundleID, device, appPath string, steps, assertions []string, uiContext map[string]interface{}) model.Task {
	s := make([]any, len(steps))
	for i, v := range steps {
		s[i] = v
	}
	a := make([]any, len(assertions))
	for i, v := range assertions {
		a[i] = v
	}
	payload := map[string]any{
		"app_bundle_id":  bundleID,
		"device_profile": device,
		"steps":          s,
		"assertions":     a,
	}
	if appPath != "" {
		payload["app_path"] = appPath
	}
	if uiContext != nil {
		payload["ui_context"] = uiContext
	}
	return model.Task{
		SchemaVersion:         model.CurrentSchemaVersion,
		TaskID:                taskID,
		RunID:                 runID,
		Surface:               model.SurfaceIOS,
		Kind:                  model.TaskKindSmoke,
		Priority:              model.PriorityP2,
		Status:                model.TaskStatusQueued,
		DedupeKey:             dedupe,
		MaxAttempts:           1,
		CreatedBy:             "benchmark",
		AcceptanceCriteriaIDs: []string{"ac_1"},
		Payload:               payload,
	}
}

// =========================================================================
// HELPERS
// =========================================================================

func findPython(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("python3/python not found in PATH")
	return ""
}

func absPath(t *testing.T, rel string) string {
	t.Helper()
	abs, err := filepath.Abs(rel)
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	return abs
}

// =========================================================================
// API BENCHMARK SERVERS
// =========================================================================

func benchCleanServer() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid json"}`)
			return
		}
		if body.Username == "admin" && body.Password == "correct-password" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			fmt.Fprint(w, `{"token":"valid-jwt-token","expires_in":3600}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"invalid credentials"}`)
	})

	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":"unauthorized"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"users":[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]}`)
	})

	mux.HandleFunc("/users/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"id":1,"name":"Alice","email":"alice@example.com"}`)
	})

	mux.HandleFunc("/users/999", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":"user not found"}`)
	})

	mux.HandleFunc("/users/create", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":3,"name":"Charlie","created":true}`)
	})

	mux.HandleFunc("/users/1/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(405)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"query":"%s","results":[{"id":1,"match":true}],"total":1}`, q)
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"status":"healthy","version":"1.0.0"}`)
	})

	return httptest.NewServer(mux)
}

func benchBuggyServer() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"token":"fake-token","expires_in":3600}`)
	})

	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"users":[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]}`)
	})

	mux.HandleFunc("/users/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"id":1,"name":"Alice","email":"alice@example.com"}`)
	})

	mux.HandleFunc("/users/999", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"id":999,"name":"","email":""}`)
	})

	mux.HandleFunc("/users/create", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"id":3,"name":"Charlie","created":true}`)
	})

	mux.HandleFunc("/users/1/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(405)
			return
		}
		w.WriteHeader(200)
		fmt.Fprint(w, `{"deleted":true}`)
	})

	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"query": "test", "results": [BROKEN JSON`)
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"status":"healthy","version":"1.0.0"}`)
	})

	return httptest.NewServer(mux)
}

// =========================================================================
// API BENCHMARK
// =========================================================================

func buildAPICleanScenarios(baseURL string) []benchScenario {
	return []benchScenario{
		{name: "api_health_clean", surface: model.SurfaceAPI, description: "Health endpoint returns 200 with status healthy",
			tasks: []model.Task{apiTask("health_c", "run", "health_c", baseURL+"/health", "GET", 200, "healthy")}, expectVerdict: model.VerdictPass},
		{name: "api_login_valid_clean", surface: model.SurfaceAPI, description: "Login with valid credentials returns 200 with token",
			tasks: []model.Task{apiTaskWithBody("login_v_c", "run", "login_v_c", baseURL+"/login", "POST", `{"username":"admin","password":"correct-password"}`, 200, "token")}, expectVerdict: model.VerdictPass},
		{name: "api_login_bad_401_clean", surface: model.SurfaceAPI, description: "Login with bad credentials returns 401",
			tasks: []model.Task{apiTaskWithBody("login_b_c", "run", "login_b_c", baseURL+"/login", "POST", `{"username":"admin","password":"wrong"}`, 401, "invalid credentials")}, expectVerdict: model.VerdictPass},
		{name: "api_users_auth_clean", surface: model.SurfaceAPI, description: "GET /users with auth returns user list",
			tasks: []model.Task{apiTaskWithAuth("users_a_c", "run", "users_a_c", baseURL+"/users", "GET", "Bearer valid-token", 200, "Alice")}, expectVerdict: model.VerdictPass},
		{name: "api_users_noauth_401_clean", surface: model.SurfaceAPI, description: "GET /users without auth returns 401",
			tasks: []model.Task{apiTask("users_na_c", "run", "users_na_c", baseURL+"/users", "GET", 401, "unauthorized")}, expectVerdict: model.VerdictPass},
		{name: "api_user1_clean", surface: model.SurfaceAPI, description: "GET /users/1 returns user with JSON content type",
			tasks: []model.Task{apiTask("u1_c", "run", "u1_c", baseURL+"/users/1", "GET", 200, "Alice")}, expectVerdict: model.VerdictPass},
		{name: "api_user999_404_clean", surface: model.SurfaceAPI, description: "GET /users/999 returns 404",
			tasks: []model.Task{apiTask("u999_c", "run", "u999_c", baseURL+"/users/999", "GET", 404, "not found")}, expectVerdict: model.VerdictPass},
		{name: "api_create_201_clean", surface: model.SurfaceAPI, description: "POST /users/create returns 201 Created",
			tasks: []model.Task{apiTaskWithBody("create_c", "run", "create_c", baseURL+"/users/create", "POST", `{"name":"Charlie"}`, 201, "created")}, expectVerdict: model.VerdictPass},
		{name: "api_search_clean", surface: model.SurfaceAPI, description: "GET /search returns valid JSON results",
			tasks: []model.Task{apiTask("search_c", "run", "search_c", baseURL+"/search?q=test", "GET", 200, "total")}, expectVerdict: model.VerdictPass},
	}
}

func buildAPIBuggyScenarios(baseURL string) []benchScenario {
	return []benchScenario{
		{name: "api_health_buggy", surface: model.SurfaceAPI, description: "Health endpoint returns 200 with status healthy",
			tasks: []model.Task{apiTask("health_b", "run", "health_b", baseURL+"/health", "GET", 200, "healthy")}, expectVerdict: model.VerdictPass},
		{name: "API1_login_bad_should_401", surface: model.SurfaceAPI, description: "Login with bad credentials must return 401",
			tasks: []model.Task{apiTaskWithBody("login_b_b", "run", "login_b_b", baseURL+"/login", "POST", `{"username":"admin","password":"wrong"}`, 401, "invalid")}, expectVerdict: model.VerdictFail, expectBugs: []string{"API-1"}},
		{name: "API2_users_noauth_should_401", surface: model.SurfaceAPI, description: "GET /users without auth header must return 401",
			tasks: []model.Task{apiTask("users_na_b", "run", "users_na_b", baseURL+"/users", "GET", 401, "unauthorized")}, expectVerdict: model.VerdictFail, expectBugs: []string{"API-2"}},
		{name: "API4_missing_user_should_404", surface: model.SurfaceAPI, description: "GET /users/999 must return 404 for missing user",
			tasks: []model.Task{apiTask("u999_b", "run", "u999_b", baseURL+"/users/999", "GET", 404, "not found")}, expectVerdict: model.VerdictFail, expectBugs: []string{"API-4"}},
		{name: "API5_create_should_201", surface: model.SurfaceAPI, description: "POST /users/create must return 201 Created",
			tasks: []model.Task{apiTaskWithBody("create_b", "run", "create_b", baseURL+"/users/create", "POST", `{"name":"Charlie"}`, 201, "created")}, expectVerdict: model.VerdictFail, expectBugs: []string{"API-5"}},
		{name: "API7_search_valid_json", surface: model.SurfaceAPI, description: "GET /search returns valid JSON with results",
			tasks: []model.Task{apiTask("search_b", "run", "search_b", baseURL+"/search?q=test", "GET", 200, `"total"`)}, expectVerdict: model.VerdictFail, expectBugs: []string{"API-7"}},
	}
}

func TestBenchmark_API(t *testing.T) {
	clean := benchCleanServer()
	defer clean.Close()
	buggy := benchBuggyServer()
	defer buggy.Close()

	executors := map[model.Surface]orchestrator.Executor{
		model.SurfaceAPI: &apiExecutor{adapter: apirunner.NewAdapter(10 * time.Second)},
	}

	tp, fp, tn, fn := 0, 0, 0, 0

	t.Run("clean", func(t *testing.T) {
		for _, sc := range buildAPICleanScenarios(clean.URL) {
			sc := sc
			t.Run(sc.name, func(t *testing.T) {
				verdict := runPipeline(t, sc, executors)
				if verdict.Status != sc.expectVerdict {
					fp++
					t.Errorf("expected %s, got %s", sc.expectVerdict, verdict.Status)
				} else {
					tn++
				}
			})
		}
	})

	t.Run("buggy", func(t *testing.T) {
		for _, sc := range buildAPIBuggyScenarios(buggy.URL) {
			sc := sc
			t.Run(sc.name, func(t *testing.T) {
				verdict := runPipeline(t, sc, executors)
				if verdict.Status != sc.expectVerdict {
					if sc.expectVerdict == model.VerdictFail {
						fn++
					} else {
						fp++
					}
					t.Errorf("expected %s, got %s", sc.expectVerdict, verdict.Status)
				} else {
					if sc.expectVerdict == model.VerdictFail {
						tp++
					} else {
						tn++
					}
				}
			})
		}
	})

	t.Logf("API: TP=%d TN=%d FP=%d FN=%d", tp, tn, fp, fn)
	if tp+fn > 0 {
		t.Logf("Recall: %.0f%%  Precision: %.0f%%", float64(tp)/float64(tp+fn)*100, float64(tp)/float64(tp+fp)*100)
	}
}

// =========================================================================
// WEB BENCHMARK
// =========================================================================

func TestBenchmark_Web(t *testing.T) {
	python := findPython(t)
	adapterPath := absPath(t, filepath.Join("adapters", "ai-browser-use"))
	if _, err := os.Stat(adapterPath); err != nil {
		t.Fatalf("adapter not found: %s", adapterPath)
	}

	// Check if browser CLI is available
	if _, err := exec.LookPath("browser"); err != nil {
		t.Skip("browser CLI not in PATH; install browser-cli and run `browser serve --backend selenium --headless`")
	}

	// Check if browser server is running with selenium backend (headless)
	// The extension backend cannot navigate to localhost test URLs.
	statusOut, err := exec.Command("browser", "status").CombinedOutput()
	statusStr := strings.ToLower(string(statusOut))
	if err != nil || !strings.Contains(statusStr, "running") {
		t.Skip("browser serve not running; start with: browser serve --backend selenium --headless")
	}

	// Quick smoke test: can we actually navigate to a URL?
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>smoke</body></html>"))
	}))
	gotoOut, gotoErr := exec.Command("browser", "goto", testServer.URL).CombinedOutput()
	testServer.Close()
	if gotoErr != nil {
		t.Skipf("browser cannot navigate to localhost (likely extension backend; need selenium): %s", string(gotoOut))
	}

	// Serve the web app
	appDir := absPath(t, filepath.Join("web", "app"))
	server := httptest.NewServer(http.FileServer(http.Dir(appDir)))
	defer server.Close()

	webExec := &webAdapterExecutor{adapterPath: adapterPath, pythonPath: python}
	executors := map[model.Surface]orchestrator.Executor{
		model.SurfaceWeb: webExec,
	}

	t.Run("clean_login_shows_dashboard", func(t *testing.T) {
		sc := benchScenario{
			name:        "web_clean_login",
			surface:     model.SurfaceWeb,
			description: "Valid login shows the task manager dashboard",
			tasks: []model.Task{webTask("web_login_c", "run", "web_login_c",
				[]string{server.URL + "/index.html"},
				[]string{
					`fill "#username" admin`,
					`fill "#password" password`,
					`click "Sign In"`,
					`wait text "Task Manager"`,
				},
				[]string{
					"Task Manager",
					"Review pull request",
				},
			)},
			expectVerdict: model.VerdictPass,
		}
		verdict := runPipeline(t, sc, executors)
		if verdict.Status != sc.expectVerdict {
			t.Errorf("expected %s, got %s. Reasons: %v", sc.expectVerdict, verdict.Status, verdict.Reasons)
		}
	})

	t.Run("clean_add_task", func(t *testing.T) {
		sc := benchScenario{
			name:        "web_clean_add",
			surface:     model.SurfaceWeb,
			description: "Adding a task updates the list and stats",
			tasks: []model.Task{webTask("web_add_c", "run", "web_add_c",
				[]string{server.URL + "/index.html"},
				[]string{
					`fill "#username" admin`,
					`fill "#password" password`,
					`click "Sign In"`,
					`wait text "Task Manager"`,
					`fill "#new-task-input" "Write benchmark tests"`,
					`click "Add"`,
				},
				[]string{
					"Write benchmark tests",
					"6", // stat-total should show 6
				},
			)},
			expectVerdict: model.VerdictPass,
		}
		verdict := runPipeline(t, sc, executors)
		if verdict.Status != sc.expectVerdict {
			t.Errorf("expected %s, got %s. Reasons: %v", sc.expectVerdict, verdict.Status, verdict.Reasons)
		}
	})

	t.Run("buggy_login_error_missing", func(t *testing.T) {
		sc := benchScenario{
			name:        "WEB1_login_error_missing",
			surface:     model.SurfaceWeb,
			description: "Invalid login must show error message",
			tasks: []model.Task{webTask("web1_b", "run", "web1_b",
				[]string{server.URL + "/buggy.html"},
				[]string{
					`fill "#username" admin`,
					`fill "#password" wrongpass`,
					`click "Sign In"`,
				},
				[]string{
					"Invalid username or password",
				},
			)},
			expectVerdict: model.VerdictFail,
			expectBugs:    []string{"WEB-1"},
		}
		verdict := runPipeline(t, sc, executors)
		if verdict.Status != sc.expectVerdict {
			t.Errorf("expected %s, got %s. Reasons: %v", sc.expectVerdict, verdict.Status, verdict.Reasons)
		} else {
			t.Logf("Correctly detected WEB-1: login error message never appears")
		}
	})

	t.Run("buggy_stats_dont_update", func(t *testing.T) {
		sc := benchScenario{
			name:        "WEB2_stats_stale",
			surface:     model.SurfaceWeb,
			description: "Dashboard stats must update when tasks change",
			tasks: []model.Task{webTask("web2_b", "run", "web2_b",
				[]string{server.URL + "/buggy.html"},
				[]string{
					`fill "#username" admin`,
					`fill "#password" password`,
					// Note: buggy version redirects to /nonexistent.html (WEB-5)
					// so we test stats separately by going directly to a page
					// where the dashboard is visible
				},
				[]string{
					// WEB-5 means we can't even get to the dashboard on buggy
					"Task Manager",
				},
			)},
			expectVerdict: model.VerdictFail,
			expectBugs:    []string{"WEB-5"},
		}
		verdict := runPipeline(t, sc, executors)
		if verdict.Status != sc.expectVerdict {
			t.Errorf("expected %s, got %s. Reasons: %v", sc.expectVerdict, verdict.Status, verdict.Reasons)
		} else {
			t.Logf("Correctly detected WEB-5: login redirects to wrong page")
		}
	})
}

// =========================================================================
// macOS BENCHMARK
// =========================================================================

type macosScenarioFile struct {
	Surface   string                 `json:"surface"`
	UIContext map[string]interface{} `json:"ui_context"`
	Scenarios []macosScenario        `json:"scenarios"`
}

type macosScenario struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	App           string   `json:"app"`
	AppBundleID   string   `json:"app_bundle_id"`
	Build         string   `json:"build"`
	Steps         []string `json:"steps"`
	Assertions    []string `json:"assertions"`
	ExpectOutcome string   `json:"expect_outcome"`
}

// buildMacOSApp builds the UnitConverter into a .app bundle for the given mode.
// Uses a single .app path to avoid LaunchServices collisions when both clean
// and buggy share the same CFBundleName.
func buildMacOSApp(t *testing.T, mode string) string {
	t.Helper()
	pkgPath := absPath(t, filepath.Join("macos", "UnitConverter.swiftpm"))

	var buildArgs []string
	buildArgs = append(buildArgs, "build", "--package-path", pkgPath)
	if mode == "buggy" {
		buildArgs = append(buildArgs, "-Xswiftc", "-DBUGGY")
	}

	// Build the binary
	cmd := exec.Command("swift", buildArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("swift build (%s): %v\n%s", mode, err, string(out))
	}

	// Get bin path
	showArgs := append([]string{"build", "--show-bin-path", "--package-path", pkgPath}, buildArgs[3:]...)
	binPathOut, err := exec.Command("swift", showArgs...).Output()
	if err != nil {
		t.Fatalf("swift build --show-bin-path (%s): %v", mode, err)
	}
	binPath := strings.TrimSpace(string(binPathOut))

	// Use a single .app name to avoid LaunchServices collisions
	appDir := absPath(t, filepath.Join("macos", "UnitConverter.app"))
	macosDir := filepath.Join(appDir, "Contents", "MacOS")
	if err := os.MkdirAll(macosDir, 0o755); err != nil {
		t.Fatalf("mkdir .app: %v", err)
	}

	// Copy binary
	binary, err := os.ReadFile(filepath.Join(binPath, "UnitConverter"))
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(macosDir, "UnitConverter"), binary, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	// Write Info.plist
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key><string>UnitConverter</string>
  <key>CFBundleIdentifier</key><string>com.benchmark.unitconverter</string>
  <key>CFBundleName</key><string>UnitConverter</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleVersion</key><string>1.0</string>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
</dict>
</plist>`
	if err := os.WriteFile(filepath.Join(appDir, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatalf("write Info.plist: %v", err)
	}

	t.Logf("Built %s .app at: %s", mode, appDir)
	return appDir
}

// killMacOSApp kills any running UnitConverter processes.
func killMacOSApp() {
	_ = exec.Command("pkill", "-f", "UnitConverter").Run()
	time.Sleep(500 * time.Millisecond)
}

func TestBenchmark_MacOS(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("GEMINI_API_KEY") == "" {
		t.Skip("no API key set (need ANTHROPIC_API_KEY or GEMINI_API_KEY); skipping macOS benchmark")
	}
	if _, err := exec.LookPath("swift"); err != nil {
		t.Skip("swift not in PATH; skipping macOS benchmark")
	}
	if _, err := exec.LookPath("ai-computer-use"); err != nil {
		t.Skip("ai-computer-use CLI not in PATH")
	}

	python := findPython(t)
	adapterPath := absPath(t, filepath.Join("adapters", "ai-computer-use-adapter"))
	if _, err := os.Stat(adapterPath); err != nil {
		t.Fatalf("adapter not found: %s", adapterPath)
	}

	// Load scenarios
	raw, err := os.ReadFile(filepath.Join("macos", "scenarios.json"))
	if err != nil {
		t.Fatalf("read scenarios: %v", err)
	}
	var sf macosScenarioFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		t.Fatalf("parse scenarios: %v", err)
	}

	// Split scenarios by build mode
	var cleanScenarios, buggyScenarios []macosScenario
	for _, sc := range sf.Scenarios {
		if sc.Build == "buggy" {
			buggyScenarios = append(buggyScenarios, sc)
		} else {
			cleanScenarios = append(cleanScenarios, sc)
		}
	}

	macExec := &macosAdapterExecutor{adapterPath: adapterPath, pythonPath: python}
	executors := map[model.Surface]orchestrator.Executor{
		model.SurfaceMacOS: macExec,
	}

	appPath := absPath(t, filepath.Join("macos", "UnitConverter.app"))
	t.Cleanup(func() {
		killMacOSApp()
		os.RemoveAll(appPath)
	})

	tp, fp, tn, fn := 0, 0, 0, 0

	runScenarios := func(scenarios []macosScenario, mode string) {
		// Kill any prior instance and rebuild
		killMacOSApp()
		buildMacOSApp(t, mode)

		for _, sc := range scenarios {
			sc := sc
			t.Run(sc.Name, func(t *testing.T) {
				scenario := benchScenario{
					name:        sc.Name,
					surface:     model.SurfaceMacOS,
					description: sc.Description,
					tasks: []model.Task{macosTask(
						"mac_"+sc.ID, "run", "mac_"+sc.ID,
						sc.AppBundleID, sc.App, appPath, sc.Steps, sc.Assertions,
						sf.UIContext,
					)},
				}
				switch sc.ExpectOutcome {
				case "pass":
					scenario.expectVerdict = model.VerdictPass
				case "fail":
					scenario.expectVerdict = model.VerdictFail
				default:
					scenario.expectVerdict = model.VerdictCannotVerify
				}

				verdict := runPipeline(t, scenario, executors)
				t.Logf("[%s] verdict=%s (expected=%s)", sc.ID, verdict.Status, scenario.expectVerdict)
				if verdict.Status != scenario.expectVerdict {
					if scenario.expectVerdict == model.VerdictFail {
						fn++
					} else {
						fp++
					}
					t.Errorf("expected %s, got %s", scenario.expectVerdict, verdict.Status)
				} else {
					if scenario.expectVerdict == model.VerdictFail {
						tp++
					} else {
						tn++
					}
				}
			})
		}
	}

	// Run clean first, then buggy — one build at a time to avoid
	// LaunchServices collisions from two apps with the same CFBundleName.
	t.Run("clean", func(t *testing.T) { runScenarios(cleanScenarios, "clean") })
	t.Run("buggy", func(t *testing.T) { runScenarios(buggyScenarios, "buggy") })

	t.Logf("macOS: TP=%d TN=%d FP=%d FN=%d", tp, tn, fp, fn)
	if tp+fn > 0 {
		t.Logf("Recall: %.0f%%  Precision: %.0f%%", float64(tp)/float64(tp+fn)*100, float64(tp)/float64(tp+fp)*100)
	}
}

// =========================================================================
// iOS BENCHMARK
// =========================================================================

type iosScenarioFile struct {
	Surface   string                 `json:"surface"`
	Status    string                 `json:"status"`
	UIContext map[string]interface{} `json:"ui_context"`
	Scenarios []iosScenario          `json:"scenarios"`
}

type iosScenario struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	AppBundleID   string   `json:"app_bundle_id"`
	DeviceProfile string   `json:"device_profile"`
	Build         string   `json:"build"`
	Steps         []string `json:"steps"`
	Assertions    []string `json:"assertions"`
	ExpectOutcome string   `json:"expect_outcome"`
}

// iosSimulatorExecutor calls the ai-ios-simulator-adapter as a subprocess.
type iosSimulatorExecutor struct {
	adapterPath string
	pythonPath  string
}

func (s *iosSimulatorExecutor) Run(_ context.Context, task model.Task, _ sandbox.Sandbox, artifactDir string) (runner.Result, error) {
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return runner.Result{}, err
	}

	cliInput := runner.CLIInput{
		Task:        task,
		ArtifactDir: artifactDir,
	}
	inputRaw, err := json.MarshalIndent(cliInput, "", "  ")
	if err != nil {
		return runner.Result{}, err
	}
	inputPath := filepath.Join(artifactDir, task.TaskID+"-input.json")
	outputPath := filepath.Join(artifactDir, task.TaskID+"-output.json")
	if err := os.WriteFile(inputPath, inputRaw, 0o644); err != nil {
		return runner.Result{}, err
	}

	cmd := exec.Command(s.pythonPath, s.adapterPath, "run", "--input", inputPath, "--output", outputPath)
	cmd.Dir = artifactDir
	cmd.Env = os.Environ()
	cmdOut, cmdErr := cmd.CombinedOutput()

	outputRaw, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		if cmdErr != nil {
			return runner.Result{}, fmt.Errorf("adapter error: %w (output: %s)", cmdErr, string(cmdOut))
		}
		return runner.Result{}, fmt.Errorf("output file not found: %w", readErr)
	}

	var cliOutput runner.CLIOutput
	if err := json.Unmarshal(outputRaw, &cliOutput); err != nil {
		return runner.Result{}, fmt.Errorf("invalid adapter output: %w", err)
	}

	return runner.Result{
		Outcome:        cliOutput.Outcome,
		Summary:        cliOutput.Summary,
		EvidenceFiles:  cliOutput.EvidenceFiles,
		StabilityHints: cliOutput.StabilityHints,
	}, nil
}

// buildIOSApp builds ContactsApp for the iOS Simulator.
// Returns the path to the .app bundle.
func buildIOSApp(t *testing.T, mode string) string {
	t.Helper()

	derivedData := fmt.Sprintf("/tmp/contactsapp-build-%s", mode)
	pkgPath := absPath(t, filepath.Join("ios", "ContactsApp.swiftpm"))

	args := []string{
		"build",
		"-scheme", "ContactsApp",
		"-destination", "platform=iOS Simulator,name=iPhone 17",
		"-derivedDataPath", derivedData,
	}
	if mode == "buggy" {
		args = append(args, "SWIFT_ACTIVE_COMPILATION_CONDITIONS=BUGGY")
	}

	cmd := exec.Command("xcodebuild", args...)
	cmd.Dir = pkgPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xcodebuild (%s): %v\n%s", mode, err, string(out))
	}

	// The Swift package produces a raw executable, not a .app.
	// Wrap it in a .app bundle for simctl install.
	binPath := filepath.Join(derivedData, "Build", "Products", "Debug-iphonesimulator", "ContactsApp")
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("ContactsApp binary not found at %s", binPath)
	}

	appDir := absPath(t, filepath.Join("ios", fmt.Sprintf("ContactsApp-%s.app", mode)))
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir .app: %v", err)
	}

	binary, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "ContactsApp"), binary, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key><string>ContactsApp</string>
  <key>CFBundleIdentifier</key><string>com.benchmark.contactsapp</string>
  <key>CFBundleName</key><string>ContactsApp</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleVersion</key><string>1.0</string>
  <key>MinimumOSVersion</key><string>17.0</string>
  <key>CFBundleSupportedPlatforms</key>
  <array><string>iPhoneSimulator</string></array>
  <key>DTPlatformName</key><string>iphonesimulator</string>
</dict>
</plist>`
	if err := os.WriteFile(filepath.Join(appDir, "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatalf("write Info.plist: %v", err)
	}

	t.Logf("Built iOS %s app at: %s", mode, appDir)
	return appDir
}

func TestBenchmark_iOS(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("GEMINI_API_KEY") == "" {
		t.Skip("no API key set (need ANTHROPIC_API_KEY or GEMINI_API_KEY); skipping iOS benchmark")
	}
	if _, err := exec.LookPath("xcrun"); err != nil {
		t.Skip("xcrun not in PATH; skipping iOS benchmark")
	}
	if _, err := exec.LookPath("xcodebuild"); err != nil {
		t.Skip("xcodebuild not in PATH; skipping iOS benchmark")
	}
	if _, err := exec.LookPath("ai-computer-use"); err != nil {
		t.Skip("ai-computer-use CLI not in PATH")
	}

	// Check if a simulator is available
	simListOut, err := exec.Command("xcrun", "simctl", "list", "devices", "available").CombinedOutput()
	if err != nil || !strings.Contains(string(simListOut), "iPhone") {
		t.Skip("no available iOS simulators")
	}

	raw, err := os.ReadFile(filepath.Join("ios", "scenarios.json"))
	if err != nil {
		t.Fatalf("read scenarios: %v", err)
	}
	var sf iosScenarioFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		t.Fatalf("parse scenarios: %v", err)
	}

	// If status is still "stub", fall back to the stub runner
	if sf.Status == "stub" {
		iosExec := &iosExecutor{adapter: iosrunner.NewAdapter("")}
		executors := map[model.Surface]orchestrator.Executor{
			model.SurfaceIOS: iosExec,
		}
		for _, sc := range sf.Scenarios {
			sc := sc
			t.Run(sc.Name, func(t *testing.T) {
				scenario := benchScenario{
					name:          sc.Name,
					surface:       model.SurfaceIOS,
					description:   sc.Description,
					expectVerdict: model.VerdictCannotVerify,
					tasks: []model.Task{iosTask(
						"ios_"+sc.ID, "run", "ios_"+sc.ID,
						sc.AppBundleID, sc.DeviceProfile, "", sc.Steps, sc.Assertions,
						sf.UIContext,
					)},
				}
				verdict := runPipeline(t, scenario, executors)
				if verdict.Status != model.VerdictCannotVerify {
					t.Errorf("expected cannot_verify, got %s", verdict.Status)
				} else {
					t.Logf("[%s] correctly returned cannot_verify (iOS stub)", sc.Name)
				}
			})
		}
		return
	}

	// Simulator mode: build apps and run via ai-ios-simulator-adapter
	python := findPython(t)
	adapterPath := absPath(t, filepath.Join("adapters", "ai-ios-simulator-adapter"))
	if _, err := os.Stat(adapterPath); err != nil {
		t.Fatalf("adapter not found: %s", adapterPath)
	}

	cleanAppPath := buildIOSApp(t, "clean")
	buggyAppPath := buildIOSApp(t, "buggy")

	// Shut down simulators on cleanup
	t.Cleanup(func() {
		_ = exec.Command("xcrun", "simctl", "shutdown", "all").Run()
	})

	simExec := &iosSimulatorExecutor{adapterPath: adapterPath, pythonPath: python}
	executors := map[model.Surface]orchestrator.Executor{
		model.SurfaceIOS: simExec,
	}

	tp, fp, tn, fn := 0, 0, 0, 0

	for _, sc := range sf.Scenarios {
		sc := sc
		t.Run(sc.Name, func(t *testing.T) {
			appPath := cleanAppPath
			if sc.Build == "buggy" {
				appPath = buggyAppPath
			}

			scenario := benchScenario{
				name:        sc.Name,
				surface:     model.SurfaceIOS,
				description: sc.Description,
				tasks: []model.Task{iosTask(
					"ios_"+sc.ID, "run", "ios_"+sc.ID,
					sc.AppBundleID, sc.DeviceProfile, appPath, sc.Steps, sc.Assertions,
					sf.UIContext,
				)},
			}
			switch sc.ExpectOutcome {
			case "pass":
				scenario.expectVerdict = model.VerdictPass
			case "fail":
				scenario.expectVerdict = model.VerdictFail
			default:
				scenario.expectVerdict = model.VerdictCannotVerify
			}

			verdict := runPipeline(t, scenario, executors)
			t.Logf("[%s] verdict=%s (expected=%s)", sc.ID, verdict.Status, scenario.expectVerdict)
			if verdict.Status != scenario.expectVerdict {
				if scenario.expectVerdict == model.VerdictFail {
					fn++
				} else {
					fp++
				}
				t.Errorf("expected %s, got %s", scenario.expectVerdict, verdict.Status)
			} else {
				if scenario.expectVerdict == model.VerdictFail {
					tp++
				} else {
					tn++
				}
			}
		})
	}

	t.Logf("iOS: TP=%d TN=%d FP=%d FN=%d", tp, tn, fp, fn)
	if tp+fn > 0 {
		t.Logf("Recall: %.0f%%  Precision: %.0f%%", float64(tp)/float64(tp+fn)*100, float64(tp)/float64(tp+fp)*100)
	}
}

// =========================================================================
// FULL REPORT
// =========================================================================

func TestBenchmark_FullReport(t *testing.T) {
	clean := benchCleanServer()
	defer clean.Close()
	buggy := benchBuggyServer()
	defer buggy.Close()

	apiExec := &apiExecutor{adapter: apirunner.NewAdapter(10 * time.Second)}
	iosExec := &iosExecutor{adapter: iosrunner.NewAdapter("")}

	type result struct {
		surface  string
		name     string
		expected model.VerdictStatus
		got      model.VerdictStatus
		correct  bool
		bugs     []string
	}
	var results []result

	// API clean + buggy
	apiExecutors := map[model.Surface]orchestrator.Executor{model.SurfaceAPI: apiExec}
	for _, sc := range buildAPICleanScenarios(clean.URL) {
		v := runPipeline(t, sc, apiExecutors)
		results = append(results, result{surface: "api", name: sc.name, expected: sc.expectVerdict, got: v.Status, correct: v.Status == sc.expectVerdict})
	}
	for _, sc := range buildAPIBuggyScenarios(buggy.URL) {
		v := runPipeline(t, sc, apiExecutors)
		results = append(results, result{surface: "api", name: sc.name, expected: sc.expectVerdict, got: v.Status, correct: v.Status == sc.expectVerdict, bugs: sc.expectBugs})
	}

	// iOS
	iosExecutors := map[model.Surface]orchestrator.Executor{model.SurfaceIOS: iosExec}
	iosStub := benchScenario{
		name: "ios_stub", surface: model.SurfaceIOS, description: "iOS Settings app launches",
		tasks: []model.Task{iosTask("ios_s", "run", "ios_s", "com.apple.Preferences", "iPhone 15", "",
			[]string{"Launch Settings app"}, []string{"Settings main screen is visible"}, nil)},
		expectVerdict: model.VerdictCannotVerify,
	}
	v := runPipeline(t, iosStub, iosExecutors)
	results = append(results, result{surface: "ios", name: iosStub.name, expected: iosStub.expectVerdict, got: v.Status, correct: v.Status == iosStub.expectVerdict})

	// Print report
	tp, fp, tn, fn, correct := 0, 0, 0, 0, 0
	t.Log("\n=== BENCHMARK FULL REPORT ===")
	for _, r := range results {
		mark := "OK"
		if !r.correct {
			mark = "FAIL"
		} else {
			correct++
		}
		switch {
		case r.expected == model.VerdictFail && r.got == model.VerdictFail:
			tp++
		case r.expected == model.VerdictPass && r.got == model.VerdictPass:
			tn++
		case r.expected == model.VerdictPass && r.got != model.VerdictPass:
			fp++
		case r.expected == model.VerdictFail && r.got != model.VerdictFail:
			fn++
		}
		bugStr := ""
		if len(r.bugs) > 0 {
			bugStr = " <- " + strings.Join(r.bugs, ", ")
		}
		t.Logf("[%4s] %-35s surface=%-5s expected=%-14s got=%-14s%s", mark, r.name, r.surface, r.expected, r.got, bugStr)
	}
	t.Logf("Total: %d  Correct: %d (%.0f%%)  TP:%d TN:%d FP:%d FN:%d", len(results), correct, float64(correct)/float64(len(results))*100, tp, tn, fp, fn)
	if fn > 0 {
		t.Errorf("%d seeded bugs missed", fn)
	}
	if fp > 0 {
		t.Errorf("%d clean scenarios wrongly flagged", fp)
	}
}
