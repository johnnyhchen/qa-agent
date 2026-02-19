package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	judgeagent "qa-agent/internal/agents/judge"
	planneragent "qa-agent/internal/agents/planner"
	"qa-agent/internal/model"
	"qa-agent/internal/orchestrator"
	"qa-agent/internal/runner"
	apirunner "qa-agent/internal/runner/api"
	"qa-agent/internal/sandbox"
)

// =========================================================================
// TEST HTTP SERVERS — "clean" (correct) and "buggy" (seeded defects)
// =========================================================================

// cleanServer returns a fully correct REST API.
// Every endpoint behaves as documented. This is the ground truth.
func cleanServer() *httptest.Server {
	mux := http.NewServeMux()

	// Auth: POST /login with valid creds returns 200 + token
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
			w.WriteHeader(200)
			fmt.Fprint(w, `{"token":"valid-jwt-token","expires_in":3600}`)
			return
		}
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"invalid credentials"}`)
	})

	// GET /users requires Authorization header
	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":"unauthorized"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"users":[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]}`)
	})

	// GET /users/1 returns a single user
	mux.HandleFunc("/users/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"id":1,"name":"Alice","email":"alice@example.com"}`)
	})

	// GET /users/999 returns 404 for missing user
	mux.HandleFunc("/users/999", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":"user not found"}`)
	})

	// POST /users creates a user and returns 201
	mux.HandleFunc("/users/create", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":3,"name":"Charlie","created":true}`)
	})

	// GET /health returns 200
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprint(w, `{"status":"healthy","version":"1.0.0"}`)
	})

	// DELETE /users/1 returns 204 (no content)
	mux.HandleFunc("/users/1/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(405)
			return
		}
		w.WriteHeader(204)
	})

	// GET /search?q=... returns filtered results
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"query":"%s","results":[{"id":1,"match":true}],"total":1}`, q)
	})

	return httptest.NewServer(mux)
}

// buggyServer returns a REST API with seeded defects.
// Each bug is documented so we know ground truth.
func buggyServer() *httptest.Server {
	mux := http.NewServeMux()

	// BUG 1: Login returns 200 even for WRONG credentials (broken auth)
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		// Always returns 200 with a token regardless of credentials
		w.WriteHeader(200)
		fmt.Fprint(w, `{"token":"fake-token","expires_in":3600}`)
	})

	// BUG 2: GET /users returns 200 even WITHOUT auth header (missing auth check)
	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		// No auth check at all!
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"users":[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]}`)
	})

	// BUG 3: GET /users/1 returns wrong content-type (text/plain instead of JSON)
	mux.HandleFunc("/users/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"id":1,"name":"Alice","email":"alice@example.com"}`)
	})

	// BUG 4: GET /users/999 returns 200 instead of 404 (missing user not handled)
	mux.HandleFunc("/users/999", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprint(w, `{"id":999,"name":"","email":""}`)
	})

	// BUG 5: POST /users/create returns 200 instead of 201 (wrong status code)
	mux.HandleFunc("/users/create", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		w.WriteHeader(200) // should be 201
		fmt.Fprint(w, `{"id":3,"name":"Charlie","created":true}`)
	})

	// OK: health is correct (not everything is broken)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprint(w, `{"status":"healthy","version":"1.0.0"}`)
	})

	// BUG 6: DELETE returns 200 instead of 204
	mux.HandleFunc("/users/1/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(405)
			return
		}
		w.WriteHeader(200) // should be 204
		fmt.Fprint(w, `{"deleted":true}`)
	})

	// BUG 7: GET /search returns broken JSON
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"query": "test", "results": [BROKEN JSON`)
	})

	return httptest.NewServer(mux)
}

// =========================================================================
// EVAL SCENARIO BUILDER — constructs orchestrator tasks targeting real URLs
// =========================================================================

// evalScenario describes one test case: a feature description, the API tasks
// to run, and the expected verdict.
type evalScenario struct {
	name          string
	description   string
	tasks         []model.Task
	expectVerdict model.VerdictStatus
	expectBugs    []string // which seeded bugs this should catch
}

func buildCleanScenarios(baseURL string) []evalScenario {
	return []evalScenario{
		{
			name:        "health_check_clean",
			description: "Health endpoint returns 200 with status healthy",
			tasks: []model.Task{apiTask("health_clean", "run_clean", "health_1", baseURL+"/health", "GET", 200, "healthy")},
			expectVerdict: model.VerdictPass,
		},
		{
			name:        "login_valid_creds_clean",
			description: "Login with valid credentials returns 200 with token",
			tasks: []model.Task{apiTaskWithBody("login_valid_clean", "run_clean", "login_valid_1",
				baseURL+"/login", "POST", `{"username":"admin","password":"correct-password"}`, 200, "token")},
			expectVerdict: model.VerdictPass,
		},
		{
			name:        "login_bad_creds_returns_401_clean",
			description: "Login with bad credentials returns 401",
			tasks: []model.Task{apiTaskWithBody("login_bad_clean", "run_clean", "login_bad_1",
				baseURL+"/login", "POST", `{"username":"admin","password":"wrong"}`, 401, "invalid credentials")},
			expectVerdict: model.VerdictPass,
		},
		{
			name:        "get_users_with_auth_clean",
			description: "GET /users with valid auth returns 200 with user list",
			tasks: []model.Task{apiTaskWithAuth("users_auth_clean", "run_clean", "users_auth_1",
				baseURL+"/users", "GET", "Bearer valid-token", 200, "Alice")},
			expectVerdict: model.VerdictPass,
		},
		{
			name:        "get_users_no_auth_returns_401_clean",
			description: "GET /users without auth returns 401",
			tasks: []model.Task{apiTask("users_noauth_clean", "run_clean", "users_noauth_1",
				baseURL+"/users", "GET", 401, "unauthorized")},
			expectVerdict: model.VerdictPass,
		},
		{
			name:        "get_user_exists_clean",
			description: "GET /users/1 returns 200 with user data",
			tasks: []model.Task{apiTask("user1_clean", "run_clean", "user1_1",
				baseURL+"/users/1", "GET", 200, "Alice")},
			expectVerdict: model.VerdictPass,
		},
		{
			name:        "get_user_missing_returns_404_clean",
			description: "GET /users/999 returns 404 for missing user",
			tasks: []model.Task{apiTask("user999_clean", "run_clean", "user999_1",
				baseURL+"/users/999", "GET", 404, "not found")},
			expectVerdict: model.VerdictPass,
		},
		{
			name:        "create_user_returns_201_clean",
			description: "POST /users/create returns 201",
			tasks: []model.Task{apiTaskWithBody("create_clean", "run_clean", "create_1",
				baseURL+"/users/create", "POST", `{"name":"Charlie"}`, 201, "created")},
			expectVerdict: model.VerdictPass,
		},
		{
			name:        "search_returns_results_clean",
			description: "GET /search returns results with total count",
			tasks: []model.Task{apiTask("search_clean", "run_clean", "search_1",
				baseURL+"/search?q=test", "GET", 200, "total")},
			expectVerdict: model.VerdictPass,
		},
	}
}

func buildBuggyScenarios(baseURL string) []evalScenario {
	return []evalScenario{
		{
			name:        "health_check_buggy",
			description: "Health endpoint returns 200 with status healthy",
			tasks: []model.Task{apiTask("health_buggy", "run_buggy", "health_1",
				baseURL+"/health", "GET", 200, "healthy")},
			expectVerdict: model.VerdictPass, // health IS correct in buggy server
		},
		{
			name:        "BUG1_login_bad_creds_should_401",
			description: "Login with bad credentials must return 401",
			tasks: []model.Task{apiTaskWithBody("login_bad_buggy", "run_buggy", "login_bad_1",
				baseURL+"/login", "POST", `{"username":"admin","password":"wrong"}`, 401, "invalid")},
			expectVerdict: model.VerdictFail,
			expectBugs:    []string{"BUG1: login accepts any credentials"},
		},
		{
			name:        "BUG2_users_no_auth_should_401",
			description: "GET /users without auth header must return 401",
			tasks: []model.Task{apiTask("users_noauth_buggy", "run_buggy", "users_noauth_1",
				baseURL+"/users", "GET", 401, "unauthorized")},
			expectVerdict: model.VerdictFail,
			expectBugs:    []string{"BUG2: /users has no auth check"},
		},
		{
			name:        "BUG4_missing_user_should_404",
			description: "GET /users/999 must return 404 for missing user",
			tasks: []model.Task{apiTask("user999_buggy", "run_buggy", "user999_1",
				baseURL+"/users/999", "GET", 404, "not found")},
			expectVerdict: model.VerdictFail,
			expectBugs:    []string{"BUG4: missing user returns 200 instead of 404"},
		},
		{
			name:        "BUG5_create_should_return_201",
			description: "POST /users/create must return 201 Created",
			tasks: []model.Task{apiTaskWithBody("create_buggy", "run_buggy", "create_1",
				baseURL+"/users/create", "POST", `{"name":"Charlie"}`, 201, "created")},
			expectVerdict: model.VerdictFail,
			expectBugs:    []string{"BUG5: create returns 200 instead of 201"},
		},
		{
			name:        "BUG7_search_returns_valid_json",
			description: "GET /search returns valid JSON with results array",
			tasks: []model.Task{apiTask("search_buggy", "run_buggy", "search_1",
				baseURL+"/search?q=test", "GET", 200, `"total"`)},
			expectVerdict: model.VerdictFail,
			expectBugs:    []string{"BUG7: search returns broken JSON"},
		},
	}
}

// =========================================================================
// TASK CONSTRUCTORS
// =========================================================================

func apiTask(taskID, runID, dedupe, url, method string, expectStatus int, expectBody string) model.Task {
	return model.Task{
		SchemaVersion: model.CurrentSchemaVersion,
		TaskID:        taskID,
		RunID:         runID,
		Surface:       model.SurfaceAPI,
		Kind:          model.TaskKindProof,
		Priority:      model.PriorityP1,
		Status:        model.TaskStatusQueued,
		DedupeKey:     dedupe,
		MaxAttempts:   1,
		CreatedBy:     "eval",
		AcceptanceCriteriaIDs: []string{"ac_1"},
		Payload: map[string]any{
			"http_requests": []any{
				map[string]any{
					"method":              method,
					"url":                 url,
					"expect_status":       float64(expectStatus),
					"expect_body_contains": expectBody,
				},
			},
		},
	}
}

func apiTaskWithBody(taskID, runID, dedupe, url, method, body string, expectStatus int, expectBody string) model.Task {
	return model.Task{
		SchemaVersion: model.CurrentSchemaVersion,
		TaskID:        taskID,
		RunID:         runID,
		Surface:       model.SurfaceAPI,
		Kind:          model.TaskKindProof,
		Priority:      model.PriorityP1,
		Status:        model.TaskStatusQueued,
		DedupeKey:     dedupe,
		MaxAttempts:   1,
		CreatedBy:     "eval",
		AcceptanceCriteriaIDs: []string{"ac_1"},
		Payload: map[string]any{
			"http_requests": []any{
				map[string]any{
					"method":              method,
					"url":                 url,
					"body":                body,
					"expect_status":       float64(expectStatus),
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
		SchemaVersion: model.CurrentSchemaVersion,
		TaskID:        taskID,
		RunID:         runID,
		Surface:       model.SurfaceAPI,
		Kind:          model.TaskKindProof,
		Priority:      model.PriorityP1,
		Status:        model.TaskStatusQueued,
		DedupeKey:     dedupe,
		MaxAttempts:   1,
		CreatedBy:     "eval",
		AcceptanceCriteriaIDs: []string{"ac_1"},
		Payload: map[string]any{
			"http_requests": []any{
				map[string]any{
					"method":              method,
					"url":                 url,
					"expect_status":       float64(expectStatus),
					"expect_body_contains": expectBody,
					"headers": map[string]any{
						"Authorization": auth,
					},
				},
			},
		},
	}
}

// =========================================================================
// FULL PIPELINE EVAL — orchestrator → real API runner → judge → verdict
// =========================================================================

// apiExecutorAdapter wraps the real API runner adapter so it satisfies
// the orchestrator.Executor interface (which it already does).
type apiExecutorAdapter struct {
	adapter *apirunner.Adapter
}

func (a *apiExecutorAdapter) Run(ctx context.Context, task model.Task, env sandbox.Sandbox, artifactDir string) (runner.Result, error) {
	return a.adapter.Run(ctx, task, env, artifactDir)
}

// runFullPipeline executes one scenario through the real orchestrator loop:
// planner output → queue → API runner (real HTTP) → judge → verdict.
func runFullPipeline(t *testing.T, scenario evalScenario) model.Verdict {
	t.Helper()

	store := newTestStore(t)
	runID := fmt.Sprintf("eval_%s_%d", scenario.name, time.Now().UnixNano())

	// Build a planner output that maps to this scenario
	criteria := []model.AcceptanceCriterion{{ID: "ac_1", Text: scenario.description}}
	tasks := make([]model.Task, len(scenario.tasks))
	for i, task := range scenario.tasks {
		task.RunID = runID
		task.AcceptanceCriteriaIDs = []string{"ac_1"}
		tasks[i] = task
	}

	plan := planneragent.Output{
		FeatureSpec: model.FeatureSpec{
			SchemaVersion:      model.CurrentSchemaVersion,
			RunID:              runID,
			Description:        scenario.description,
			AcceptanceCriteria: criteria,
			Surfaces:           []model.Surface{model.SurfaceAPI},
		},
		TestPlan: model.TestPlan{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Journeys:      []string{"journey_1"},
			Assertions:    []string{scenario.description},
		},
		Tasks: tasks,
	}

	// Real API runner — makes actual HTTP requests
	realRunner := &apiExecutorAdapter{adapter: apirunner.NewAdapter(10 * time.Second)}

	orch := orchestrator.New(
		store,
		&countingPlanner{output: plan},
		judgeagent.New(),
		map[model.Surface]orchestrator.Executor{
			model.SurfaceAPI: realRunner,
		},
		orchestrator.Budget{
			MaxJudgeTurns:  3,
			MaxQueuedTasks: 50,
		},
	)

	verdict, err := orch.Run(context.Background(), orchestrator.Request{
		RunID:       runID,
		Description: scenario.description,
		Surfaces:    []model.Surface{model.SurfaceAPI},
	})
	if err != nil {
		t.Fatalf("[%s] orchestrator error: %v", scenario.name, err)
	}

	// Dump transcript for debugging
	artifactDir := store.ArtifactDir(runID)
	transcripts, _ := filepath.Glob(filepath.Join(artifactDir, "**", "api-transcript.json"))
	for _, tp := range transcripts {
		raw, _ := os.ReadFile(tp)
		t.Logf("[%s] transcript: %s", scenario.name, string(raw))
	}

	return verdict
}

// =========================================================================
// THE ACTUAL EVAL TESTS
// =========================================================================

func TestEval_CleanServer_AllScenariosPass(t *testing.T) {
	server := cleanServer()
	defer server.Close()

	scenarios := buildCleanScenarios(server.URL)
	passed := 0
	failed := 0

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			verdict := runFullPipeline(t, sc)

			if verdict.Status == sc.expectVerdict {
				passed++
				t.Logf("CORRECT: %s → %s (expected %s)", sc.name, verdict.Status, sc.expectVerdict)
			} else {
				failed++
				t.Errorf("WRONG: %s → %s (expected %s). Reasons: %v",
					sc.name, verdict.Status, sc.expectVerdict, verdict.Reasons)
			}
		})
	}

	t.Logf("\n=== CLEAN SERVER RESULTS ===")
	t.Logf("Correct: %d/%d", passed, len(scenarios))
	t.Logf("False positives (flagged clean code as broken): %d", failed)
}

func TestEval_BuggyServer_DetectsDefects(t *testing.T) {
	server := buggyServer()
	defer server.Close()

	scenarios := buildBuggyScenarios(server.URL)

	truePositives := 0  // correctly detected bugs
	falseNegatives := 0 // missed bugs
	trueNegatives := 0  // correctly passed clean endpoints
	falsePositives := 0 // wrongly failed clean endpoints

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			verdict := runFullPipeline(t, sc)

			if verdict.Status == sc.expectVerdict {
				if sc.expectVerdict == model.VerdictFail {
					truePositives++
					t.Logf("TRUE POSITIVE: detected %v", sc.expectBugs)
				} else {
					trueNegatives++
					t.Logf("TRUE NEGATIVE: correctly passed %s", sc.name)
				}
			} else {
				if sc.expectVerdict == model.VerdictFail {
					falseNegatives++
					t.Errorf("FALSE NEGATIVE (missed bug): %s → %s but expected fail. Bugs: %v",
						sc.name, verdict.Status, sc.expectBugs)
				} else {
					falsePositives++
					t.Errorf("FALSE POSITIVE: %s → %s but expected pass",
						sc.name, verdict.Status)
				}
			}
		})
	}

	total := len(scenarios)
	seededBugs := 0
	for _, sc := range scenarios {
		if sc.expectVerdict == model.VerdictFail {
			seededBugs++
		}
	}

	t.Logf("\n=== BUGGY SERVER RESULTS ===")
	t.Logf("Scenarios:       %d", total)
	t.Logf("Seeded bugs:     %d", seededBugs)
	t.Logf("True positives:  %d (detected real bugs)", truePositives)
	t.Logf("False negatives: %d (missed real bugs)", falseNegatives)
	t.Logf("True negatives:  %d (correctly passed clean)", trueNegatives)
	t.Logf("False positives: %d (wrongly flagged clean)", falsePositives)

	if seededBugs > 0 {
		recall := float64(truePositives) / float64(seededBugs) * 100
		t.Logf("Recall:    %.1f%% (%d/%d bugs detected)", recall, truePositives, seededBugs)
	}
	if truePositives+falsePositives > 0 {
		precision := float64(truePositives) / float64(truePositives+falsePositives) * 100
		t.Logf("Precision: %.1f%% (%d/%d findings are real)", precision, truePositives, truePositives+falsePositives)
	}
}

// =========================================================================
// COMBINED REPORT — runs both servers and produces a single summary
// =========================================================================

func TestEval_FullReport(t *testing.T) {
	clean := cleanServer()
	defer clean.Close()
	buggy := buggyServer()
	defer buggy.Close()

	type result struct {
		name     string
		server   string
		expected model.VerdictStatus
		got      model.VerdictStatus
		correct  bool
		bugs     []string
	}

	var results []result

	// Run all clean scenarios
	for _, sc := range buildCleanScenarios(clean.URL) {
		verdict := runFullPipeline(t, sc)
		results = append(results, result{
			name:     sc.name,
			server:   "clean",
			expected: sc.expectVerdict,
			got:      verdict.Status,
			correct:  verdict.Status == sc.expectVerdict,
		})
	}

	// Run all buggy scenarios
	for _, sc := range buildBuggyScenarios(buggy.URL) {
		verdict := runFullPipeline(t, sc)
		results = append(results, result{
			name:     sc.name,
			server:   "buggy",
			expected: sc.expectVerdict,
			got:      verdict.Status,
			correct:  verdict.Status == sc.expectVerdict,
			bugs:     sc.expectBugs,
		})
	}

	// Print report
	t.Log("")
	t.Log("╔══════════════════════════════════════════════════════════════════╗")
	t.Log("║              QA-AGENT EVAL PIPELINE RESULTS                    ║")
	t.Log("╠══════════════════════════════════════════════════════════════════╣")

	correct := 0
	tp, fp, tn, fn := 0, 0, 0, 0

	for _, r := range results {
		mark := "PASS"
		if !r.correct {
			mark = "FAIL"
		}
		if r.correct {
			correct++
		}

		if r.expected == model.VerdictFail && r.got == model.VerdictFail {
			tp++
		} else if r.expected == model.VerdictPass && r.got == model.VerdictPass {
			tn++
		} else if r.expected == model.VerdictPass && r.got != model.VerdictPass {
			fp++
		} else if r.expected == model.VerdictFail && r.got != model.VerdictFail {
			fn++
		}

		bugStr := ""
		if len(r.bugs) > 0 {
			bugStr = " ← " + strings.Join(r.bugs, ", ")
		}
		t.Logf("║ [%s] %-40s server=%-5s expected=%-14s got=%-14s%s",
			mark, r.name, r.server, r.expected, r.got, bugStr)
	}

	t.Log("╠══════════════════════════════════════════════════════════════════╣")
	t.Logf("║ Total scenarios: %d", len(results))
	t.Logf("║ Correct:         %d/%d (%.1f%%)", correct, len(results), float64(correct)/float64(len(results))*100)
	t.Logf("║ True positives:  %d (bugs caught)", tp)
	t.Logf("║ True negatives:  %d (clean code passed)", tn)
	t.Logf("║ False positives: %d (clean code wrongly flagged)", fp)
	t.Logf("║ False negatives: %d (bugs missed)", fn)

	if tp+fn > 0 {
		t.Logf("║ Recall:          %.1f%%", float64(tp)/float64(tp+fn)*100)
	}
	if tp+fp > 0 {
		t.Logf("║ Precision:       %.1f%%", float64(tp)/float64(tp+fp)*100)
	}
	t.Log("╚══════════════════════════════════════════════════════════════════╝")

	if fn > 0 {
		t.Errorf("%d seeded bugs were missed by the pipeline", fn)
	}
	if fp > 0 {
		t.Errorf("%d clean scenarios were wrongly flagged", fp)
	}
}
