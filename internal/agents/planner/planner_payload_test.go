package planner

import (
	"context"
	"testing"

	"qa-agent/internal/agents/runtime"
	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
)

func TestPlanner_APITasks_HavePayloads(t *testing.T) {
	store := tempStore(t)
	rt := runtime.New(store, nil, nil, runtime.Config{})
	agent := New(rt, store)

	out, err := agent.Plan(context.Background(), "test_run_1", "GET http://localhost:5099/api/pipelines returns a JSON array with status 200.", []model.Surface{model.SurfaceAPI})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(out.Tasks) == 0 {
		t.Fatal("expected at least one task")
	}
	for i, task := range out.Tasks {
		if task.Payload == nil {
			t.Errorf("task[%d] has nil Payload", i)
			continue
		}
		if _, ok := task.Payload["http_requests"]; !ok {
			t.Errorf("task[%d] Payload missing http_requests key", i)
		}
	}
}

func TestPlanner_APIPayload_ParsesURLAndMethod(t *testing.T) {
	store := tempStore(t)
	rt := runtime.New(store, nil, nil, runtime.Config{})
	agent := New(rt, store)

	out, err := agent.Plan(context.Background(), "test_run_2", "GET http://localhost:5099/api/pipelines returns JSON array with status 200.", []model.Surface{model.SurfaceAPI})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	task := out.Tasks[0]
	reqs, ok := task.Payload["http_requests"].([]any)
	if !ok || len(reqs) == 0 {
		t.Fatal("expected http_requests array")
	}
	req, ok := reqs[0].(map[string]any)
	if !ok {
		t.Fatal("expected http request map")
	}
	if req["method"] != "GET" {
		t.Errorf("expected method GET, got %v", req["method"])
	}
	if req["url"] != "http://localhost:5099/api/pipelines" {
		t.Errorf("expected url http://localhost:5099/api/pipelines, got %v", req["url"])
	}
	if req["expect_status"] != float64(200) {
		t.Errorf("expected status 200, got %v", req["expect_status"])
	}
}

func TestPlanner_MultipleCriteria_EachGetPayload(t *testing.T) {
	store := tempStore(t)
	rt := runtime.New(store, nil, nil, runtime.Config{})
	agent := New(rt, store)

	feature := "GET http://localhost:5099/api/pipelines returns status 200. GET http://localhost:5099/api/queue returns status 200. GET http://localhost:5099/ returns HTML with status 200."
	out, err := agent.Plan(context.Background(), "test_run_3", feature, []model.Surface{model.SurfaceAPI})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(out.Tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(out.Tasks))
	}
	for i, task := range out.Tasks {
		if task.Payload == nil {
			t.Errorf("task[%d] has nil Payload", i)
		}
	}
}

func TestPlanner_WebTasks_HavePayloads(t *testing.T) {
	store := tempStore(t)
	rt := runtime.New(store, nil, nil, runtime.Config{})
	agent := New(rt, store)

	out, err := agent.Plan(context.Background(), "test_run_4", "The dashboard at http://localhost:5099 shows pipeline status.", []model.Surface{model.SurfaceWeb})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	for i, task := range out.Tasks {
		if task.Payload == nil {
			t.Errorf("task[%d] has nil Payload", i)
			continue
		}
		if _, ok := task.Payload["url"]; !ok {
			t.Errorf("task[%d] Payload missing url key", i)
		}
	}
}

func TestExtractHTTPRequests(t *testing.T) {
	tests := []struct {
		text     string
		count    int
		method   string
		url      string
		status   float64
	}{
		{
			text:   "GET http://localhost:5099/api/pipelines returns JSON with status 200",
			count:  1,
			method: "GET",
			url:    "http://localhost:5099/api/pipelines",
			status: 200,
		},
		{
			text:   "POST http://example.com/data returns status 201",
			count:  1,
			method: "POST",
			url:    "http://example.com/data",
			status: 201,
		},
		{
			text:   "No HTTP request here",
			count:  0,
		},
	}

	for _, tc := range tests {
		reqs := extractHTTPRequests(tc.text)
		if len(reqs) != tc.count {
			t.Errorf("text=%q: expected %d requests, got %d", tc.text, tc.count, len(reqs))
			continue
		}
		if tc.count > 0 {
			if reqs[0]["method"] != tc.method {
				t.Errorf("text=%q: expected method %s, got %v", tc.text, tc.method, reqs[0]["method"])
			}
			if reqs[0]["url"] != tc.url {
				t.Errorf("text=%q: expected url %s, got %v", tc.text, tc.url, reqs[0]["url"])
			}
			if tc.status > 0 {
				if reqs[0]["expect_status"] != tc.status {
					t.Errorf("text=%q: expected status %v, got %v", tc.text, tc.status, reqs[0]["expect_status"])
				}
			}
		}
	}
}

func TestExtractHTTPRequests_WithBody(t *testing.T) {
	text := `POST http://localhost:8080/v3/conversations with body {"members":[{"id":"u-0001"}]} returns status 201 containing "id"`
	reqs := extractHTTPRequests(text)
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	req := reqs[0]
	if req["method"] != "POST" {
		t.Errorf("expected method POST, got %v", req["method"])
	}
	body, ok := req["body"].(string)
	if !ok || body == "" {
		t.Fatal("expected body to be a non-empty string")
	}
	if body != `{"members":[{"id":"u-0001"}]}` {
		t.Errorf("unexpected body: %s", body)
	}
	if req["expect_status"] != float64(201) {
		t.Errorf("expected status 201, got %v", req["expect_status"])
	}
}

func TestExtractHTTPRequests_WithBodyArray(t *testing.T) {
	text := `POST http://localhost:8080/api/batch with body [{"action":"create"}] returns status 200`
	reqs := extractHTTPRequests(text)
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	body, ok := reqs[0]["body"].(string)
	if !ok || body != `[{"action":"create"}]` {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestExtractHTTPRequests_NoBody(t *testing.T) {
	text := "GET http://localhost:8080/ping returns status 200 containing ok"
	reqs := extractHTTPRequests(text)
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if _, ok := reqs[0]["body"]; ok {
		t.Error("GET request should not have a body extracted")
	}
}

func TestSplitCriteria_PreservesDottedURLs(t *testing.T) {
	desc := "POST http://localhost:8080/api/users.create returns status 201. GET http://localhost:8080/api/users.list returns status 200."
	criteria := splitCriteria(desc)
	if len(criteria) != 2 {
		t.Fatalf("expected 2 criteria, got %d", len(criteria))
	}
	if !contains(criteria[0].Text, "users.create") {
		t.Errorf("first criterion should contain users.create, got: %s", criteria[0].Text)
	}
	if !contains(criteria[1].Text, "users.list") {
		t.Errorf("second criterion should contain users.list, got: %s", criteria[1].Text)
	}
}

func TestSplitCriteria_PreservesVersionedPaths(t *testing.T) {
	desc := "GET http://localhost:8080/v1.0/users returns status 200. GET http://localhost:8080/v1.0/teams returns status 200."
	criteria := splitCriteria(desc)
	if len(criteria) != 2 {
		t.Fatalf("expected 2 criteria, got %d", len(criteria))
	}
	if !contains(criteria[0].Text, "/v1.0/users") {
		t.Errorf("first criterion should contain /v1.0/users, got: %s", criteria[0].Text)
	}
	if !contains(criteria[1].Text, "/v1.0/teams") {
		t.Errorf("second criterion should contain /v1.0/teams, got: %s", criteria[1].Text)
	}
}

func TestSplitCriteria_MixedDottedAndCleanURLs(t *testing.T) {
	desc := "POST http://localhost:8080/api/users.create returns status 201 containing id. GET http://localhost:8080/v3/conversations returns status 200. GET http://localhost:8080/v1.0/teams returns status 200."
	criteria := splitCriteria(desc)
	if len(criteria) != 3 {
		t.Fatalf("expected 3 criteria, got %d: %v", len(criteria), criteriaTexts(criteria))
	}
}

func TestSplitCriteria_SingleCriterionNoPeriod(t *testing.T) {
	desc := "GET http://localhost:8080/ping returns status 200"
	criteria := splitCriteria(desc)
	if len(criteria) != 1 {
		t.Fatalf("expected 1 criterion, got %d", len(criteria))
	}
}

func TestPlanner_POSTWithBody_HasPayloadBody(t *testing.T) {
	store := tempStore(t)
	rt := runtime.New(store, nil, nil, runtime.Config{})
	agent := New(rt, store)

	feature := `POST http://localhost:8080/v3/conversations with body {"members":[{"id":"u-0001"}]} returns status 201 containing "id".`
	out, err := agent.Plan(context.Background(), "test_run_body", feature, []model.Surface{model.SurfaceAPI})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(out.Tasks) == 0 {
		t.Fatal("expected at least one task")
	}
	task := out.Tasks[0]
	if task.Payload == nil {
		t.Fatal("task payload is nil")
	}
	reqs, ok := task.Payload["http_requests"].([]any)
	if !ok || len(reqs) == 0 {
		t.Fatal("expected http_requests array in payload")
	}
	req, ok := reqs[0].(map[string]any)
	if !ok {
		t.Fatal("expected request map")
	}
	body, ok := req["body"].(string)
	if !ok || body == "" {
		t.Fatalf("expected body string in request, got %v", req["body"])
	}
	if body != `{"members":[{"id":"u-0001"}]}` {
		t.Errorf("unexpected body: %s", body)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func criteriaTexts(criteria []model.AcceptanceCriterion) []string {
	texts := make([]string, len(criteria))
	for i, c := range criteria {
		texts[i] = c.Text
	}
	return texts
}

func tempStore(t *testing.T) *blackboard.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := blackboard.NewStore(dir)
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	return store
}
