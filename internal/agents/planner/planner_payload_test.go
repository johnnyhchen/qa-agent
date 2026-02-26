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

func tempStore(t *testing.T) *blackboard.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := blackboard.NewStore(dir)
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	return store
}
