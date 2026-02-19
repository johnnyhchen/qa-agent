package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
)

func TestDockerManagerCreateDestroyAndSnapshot(t *testing.T) {
	store, err := blackboard.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	runID := "run_1"
	if err := store.CreateValidationRun(context.Background(), blackboard.ValidationRun{
		ID:              runID,
		RetentionPolicy: blackboard.RetentionKeepAll,
	}); err != nil {
		t.Fatalf("CreateValidationRun() error = %v", err)
	}

	logPath := filepath.Join(t.TempDir(), "docker-calls.log")
	dockerBin := writeFakeDocker(t, false)
	composeFile := filepath.Join(t.TempDir(), "docker-compose.yml")
	if err := os.WriteFile(composeFile, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(compose) error = %v", err)
	}

	manager := NewDockerManager(store, dockerBin, 2*time.Second)
	sandbox, err := manager.Create(context.Background(), CreateRequest{
		RunID:       runID,
		ComposeFile: composeFile,
		Env: map[string]string{
			"FAKE_DOCKER_LOG": logPath,
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if len(sandbox.ContainerIDs) != 1 || sandbox.ContainerIDs[0] != "container-1" {
		t.Fatalf("sandbox.ContainerIDs = %#v, want [container-1]", sandbox.ContainerIDs)
	}

	snapshotPath := filepath.Join(store.ArtifactDir(runID), "sandbox", sandbox.ID, "environment-snapshot.json")
	if _, err := os.Stat(snapshotPath); err != nil {
		t.Fatalf("environment snapshot missing: %v", err)
	}

	execResult, err := manager.Exec(context.Background(), sandbox.ID, []string{"echo", "hello"})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if !strings.Contains(execResult.Stdout, "exec ok") {
		t.Fatalf("Exec() stdout = %q, expected exec output", execResult.Stdout)
	}

	if err := manager.Destroy(context.Background(), sandbox.ID); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}

	callsRaw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(log) error = %v", err)
	}
	calls := string(callsRaw)
	if !strings.Contains(calls, "compose -f") || !strings.Contains(calls, "down --volumes") {
		t.Fatalf("docker call log missing expected commands: %s", calls)
	}

	evidenceRows, err := store.EvidenceList(context.Background(), blackboard.EvidenceFilter{
		RunID: runID,
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("EvidenceList() error = %v", err)
	}
	if !containsEvidenceKind(evidenceRows, model.EvidenceKindTranscript) {
		t.Fatal("expected transcript evidence from environment snapshot")
	}
	if !containsEvidenceKind(evidenceRows, model.EvidenceKindLog) {
		t.Fatal("expected log evidence from docker logs")
	}
}

func TestDockerManagerTimeout(t *testing.T) {
	store, err := blackboard.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	runID := "run_timeout"
	if err := store.CreateValidationRun(context.Background(), blackboard.ValidationRun{
		ID:              runID,
		RetentionPolicy: blackboard.RetentionKeepAll,
	}); err != nil {
		t.Fatalf("CreateValidationRun() error = %v", err)
	}

	dockerBin := writeFakeDocker(t, true)
	composeFile := filepath.Join(t.TempDir(), "docker-compose.yml")
	if err := os.WriteFile(composeFile, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(compose) error = %v", err)
	}

	manager := NewDockerManager(store, dockerBin, 50*time.Millisecond)
	_, err = manager.Create(context.Background(), CreateRequest{
		RunID:       runID,
		ComposeFile: composeFile,
		Env:         map[string]string{},
	})
	if err == nil {
		t.Fatal("Create() expected timeout error")
	}
}

func writeFakeDocker(t *testing.T, sleep bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-docker.sh")
	script := `#!/bin/sh
if [ ! -z "$FAKE_DOCKER_LOG" ]; then
  echo "$@" >> "$FAKE_DOCKER_LOG"
fi
if [ "$FAKE_DOCKER_SLEEP" = "1" ]; then
  sleep 1
fi
if [ "$1" = "compose" ] && [ "$4" = "ps" ]; then
  echo "container-1"
  exit 0
fi
if [ "$1" = "inspect" ]; then
  echo '[{"Id":"container-1"}]'
  exit 0
fi
if [ "$1" = "compose" ] && [ "$4" = "logs" ]; then
  echo "service logs"
  exit 0
fi
if [ "$1" = "exec" ]; then
  echo "exec ok"
  exit 0
fi
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake docker) error = %v", err)
	}
	if sleep {
		t.Setenv("FAKE_DOCKER_SLEEP", "1")
	}
	return path
}

func containsEvidenceKind(items []model.Evidence, kind model.EvidenceKind) bool {
	for _, item := range items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}
