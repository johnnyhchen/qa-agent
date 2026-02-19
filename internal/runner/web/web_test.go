package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
	"qa-agent/internal/sandbox"
	"qa-agent/internal/trace"
)

func TestAdapterRunAgainstLocalFixture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte(`<html><body><h1>ok</h1></body></html>`))
	}))
	defer server.Close()

	store, err := blackboard.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	runID := "run_web"
	if err := store.CreateValidationRun(context.Background(), blackboard.ValidationRun{
		ID:              runID,
		RetentionPolicy: blackboard.RetentionKeepAll,
	}); err != nil {
		t.Fatalf("CreateValidationRun() error = %v", err)
	}

	binary := writeFakeBrowserUse(t)
	recorder := trace.NewRecorder(store, trace.NewLogger(new(bytes.Buffer), true))
	adapter := NewAdapter(binary, time.Second, recorder)

	if err := adapter.Doctor(context.Background()); err != nil {
		t.Fatalf("Doctor() error = %v", err)
	}

	task := model.Task{
		SchemaVersion: model.CurrentSchemaVersion,
		TaskID:        "task_web_1",
		RunID:         runID,
		Surface:       model.SurfaceWeb,
		Kind:          model.TaskKindProof,
		Priority:      model.PriorityP1,
		Status:        model.TaskStatusQueued,
		DedupeKey:     "web-login-proof",
		MaxAttempts:   1,
		Payload: map[string]any{
			"start_urls": []any{server.URL},
			"steps":      []any{"open page"},
			"assertions": []any{"heading is visible"},
		},
	}

	artifactDir := filepath.Join(store.ArtifactDir(runID), "web-task")
	result, err := adapter.Run(context.Background(), task, sandbox.Sandbox{
		ID:    "sandbox_web",
		RunID: runID,
	}, artifactDir)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Outcome != model.RunOutcomePass {
		t.Fatalf("result.Outcome = %s, want %s", result.Outcome, model.RunOutcomePass)
	}

	evidenceRows, err := store.EvidenceList(context.Background(), blackboard.EvidenceFilter{
		RunID: runID,
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("EvidenceList() error = %v", err)
	}
	if len(evidenceRows) == 0 {
		t.Fatal("expected evidence rows for web run")
	}
}

func TestParseTaskSpecValidation(t *testing.T) {
	_, err := ParseTaskSpec(model.Task{})
	if err == nil {
		t.Fatal("ParseTaskSpec() expected error for missing payload")
	}
}

func writeFakeBrowserUse(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-ai-browser-use.sh")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "fake-ai-browser-use 0.0.1"
  exit 0
fi
input=""
output=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --input) input="$2"; shift 2;;
    --output) output="$2"; shift 2;;
    *) shift;;
  esac
done
if [ -z "$output" ]; then
  echo "missing output path" >&2
  exit 2
fi
echo '{"outcome":"pass","summary":"web ok","evidence_files":["screenshot.png","dom.json","console.log","network.har"]}' > "$output"
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake browser use) error = %v", err)
	}
	return path
}
