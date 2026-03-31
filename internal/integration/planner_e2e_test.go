package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	judgeagent "qa-agent/internal/agents/judge"
	"qa-agent/internal/agents/planner"
	"qa-agent/internal/agents/runtime"
	"qa-agent/internal/model"
	"qa-agent/internal/orchestrator"
	"qa-agent/internal/report"
	apirunner "qa-agent/internal/runner/api"
)

// TestOrchestrator_WithRealPlanner_ExecutesAPITasks exercises the full path:
// real planner (deterministic payload generation) → queue → real API runner → judge → verdict.
func TestOrchestrator_WithRealPlanner_ExecutesAPITasks(t *testing.T) {
	server := cleanServer()
	defer server.Close()

	store := newTestStore(t)
	rt := runtime.New(store, nil, &runtime.EchoClient{}, runtime.Config{})
	realPlanner := planner.New(rt, store)
	realRunner := &apiExecutorAdapter{adapter: apirunner.NewAdapter(30 * time.Second)}
	judge := judgeagent.New()

	runID := fmt.Sprintf("e2e_real_planner_pass_%d", time.Now().UnixNano())
	base := localhostURL(server.URL)
	description := fmt.Sprintf(
		"GET %s/health returns status 200. GET %s/users/1 returns JSON with status 200.",
		base, base,
	)

	orch := orchestrator.New(
		store,
		realPlanner,
		judge,
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
		Description: description,
		Surfaces:    []model.Surface{model.SurfaceAPI},
	})
	if err != nil {
		t.Fatalf("orchestrator.Run() error: %v", err)
	}

	// Assert: verdict is pass
	if verdict.Status != model.VerdictPass {
		t.Errorf("expected verdict pass, got %s. Reasons: %v", verdict.Status, verdict.Reasons)
	}

	// Assert: at least 2 criteria were generated (one per sentence)
	if len(verdict.Coverage) < 2 {
		t.Errorf("expected at least 2 criteria in coverage, got %d", len(verdict.Coverage))
	}

	// Assert: artifact directory contains api-transcript files
	artifactDir := store.ArtifactDir(runID)
	transcripts := findFiles(t, artifactDir, "api-transcript.json")
	if len(transcripts) == 0 {
		t.Error("expected at least one api-transcript.json in artifact directory")
	}
	t.Logf("Found %d api-transcript files", len(transcripts))
}

// TestOrchestrator_WithRealPlanner_DetectsFailure verifies the full pipeline
// detects failures when targeting a buggy endpoint.
func TestOrchestrator_WithRealPlanner_DetectsFailure(t *testing.T) {
	server := buggyServer()
	defer server.Close()

	store := newTestStore(t)
	rt := runtime.New(store, nil, &runtime.EchoClient{}, runtime.Config{})
	realPlanner := planner.New(rt, store)
	realRunner := &apiExecutorAdapter{adapter: apirunner.NewAdapter(30 * time.Second)}
	judge := judgeagent.New()

	runID := fmt.Sprintf("e2e_real_planner_fail_%d", time.Now().UnixNano())
	base := localhostURL(server.URL)
	// BUG4: /users/999 returns 200 instead of 404
	description := fmt.Sprintf(
		"GET %s/users/999 returns status 404.",
		base,
	)

	orch := orchestrator.New(
		store,
		realPlanner,
		judge,
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
		Description: description,
		Surfaces:    []model.Surface{model.SurfaceAPI},
	})
	if err != nil {
		t.Fatalf("orchestrator.Run() error: %v", err)
	}

	// Assert: verdict is fail
	if verdict.Status != model.VerdictFail {
		t.Errorf("expected verdict fail, got %s. Reasons: %v", verdict.Status, verdict.Reasons)
	}

	// Assert: findings reference the failed criterion
	if len(verdict.Findings) == 0 {
		t.Fatal("expected at least one finding for the failed criterion")
	}
	found := false
	for _, f := range verdict.Findings {
		if strings.HasPrefix(f.CriterionID, "ac_") {
			found = true
			t.Logf("Finding: criterion=%s severity=%s summary=%s", f.CriterionID, f.Severity, f.Summary)
		}
	}
	if !found {
		t.Error("expected finding to reference a criterion ID starting with 'ac_'")
	}
}

// TestOrchestrator_WithRealPlanner_ReportContainsFindings runs a failing
// pipeline and then generates a report, verifying the report surfaces the
// verdict status, reasons, and findings.
func TestOrchestrator_WithRealPlanner_ReportContainsFindings(t *testing.T) {
	server := buggyServer()
	defer server.Close()

	store := newTestStore(t)
	rt := runtime.New(store, nil, &runtime.EchoClient{}, runtime.Config{})
	realPlanner := planner.New(rt, store)
	realRunner := &apiExecutorAdapter{adapter: apirunner.NewAdapter(30 * time.Second)}
	judge := judgeagent.New()

	runID := fmt.Sprintf("e2e_real_planner_report_%d", time.Now().UnixNano())
	base := localhostURL(server.URL)
	// BUG4: /users/999 returns 200 instead of 404
	description := fmt.Sprintf(
		"GET %s/users/999 returns status 404.",
		base,
	)

	orch := orchestrator.New(
		store,
		realPlanner,
		judge,
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
		Description: description,
		Surfaces:    []model.Surface{model.SurfaceAPI},
	})
	if err != nil {
		t.Fatalf("orchestrator.Run() error: %v", err)
	}
	if verdict.Status != model.VerdictFail {
		t.Fatalf("expected verdict fail, got %s", verdict.Status)
	}

	// Write verdict.json to the run's artifact directory so the report can read it
	artifactDir := store.ArtifactDir(runID)
	verdictRaw, err := json.MarshalIndent(verdict, "", "  ")
	if err != nil {
		t.Fatalf("marshal verdict: %v", err)
	}
	// The report reads verdict.json from runDir, which is the parent of artifacts
	runDir := store.RunDir(runID)
	if err := os.WriteFile(filepath.Join(runDir, "verdict.json"), verdictRaw, 0o644); err != nil {
		t.Fatalf("write verdict.json: %v", err)
	}

	// Generate report
	gen := report.NewGenerator()
	_, _, markdown, err := gen.Generate(runID, runDir)
	if err != nil {
		t.Fatalf("report.Generate() error: %v", err)
	}

	// Assert: report.md contains "fail" verdict status
	if !strings.Contains(markdown, "`fail`") {
		t.Error("report does not contain verdict status 'fail'")
	}

	// Assert: report.md contains reasons
	if !strings.Contains(markdown, "### Reasons") {
		t.Error("report does not contain '### Reasons' section")
	}
	for _, reason := range verdict.Reasons {
		if !strings.Contains(markdown, reason) {
			t.Errorf("report missing reason: %s", reason)
		}
	}

	// Assert: report.md contains the finding summary text
	if len(verdict.Findings) == 0 {
		t.Fatal("expected findings in verdict")
	}
	for _, f := range verdict.Findings {
		if !strings.Contains(markdown, f.Summary) {
			t.Errorf("report missing finding summary: %s", f.Summary)
		}
	}

	t.Logf("Report artifact dir: %s", artifactDir)
	t.Logf("Report excerpt (first 500 chars):\n%s", truncate(markdown, 500))
}

// localhostURL rewrites the test server URL to use "localhost" instead of
// "127.0.0.1" so that the planner's period-based sentence splitter doesn't
// fragment the IP address into separate criteria.
func localhostURL(serverURL string) string {
	u, err := url.Parse(serverURL)
	if err != nil {
		return serverURL
	}
	u.Host = "localhost:" + u.Port()
	return u.String()
}

// findFiles recursively finds files with the given name under dir.
func findFiles(t *testing.T, dir, name string) []string {
	t.Helper()
	var matches []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Name() == name {
			matches = append(matches, path)
		}
		return nil
	})
	return matches
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
