package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
)

type Manager interface {
	Create(ctx context.Context, request CreateRequest) (Sandbox, error)
	Destroy(ctx context.Context, id string) error
	Exec(ctx context.Context, id string, command []string) (ExecResult, error)
}

type CreateRequest struct {
	RunID       string
	ComposeFile string
	Env         map[string]string
}

type Sandbox struct {
	ID           string            `json:"id"`
	RunID        string            `json:"run_id"`
	WorkspaceDir string            `json:"workspace_dir"`
	ContainerIDs []string          `json:"container_ids"`
	Ports        map[string]string `json:"ports,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
}

type ExecResult struct {
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	ExitCode int           `json:"exit_code"`
	Duration time.Duration `json:"duration"`
}

type DockerManager struct {
	store   *blackboard.Store
	docker  string
	timeout time.Duration

	mu        sync.Mutex
	sandboxes map[string]Sandbox
}

func NewDockerManager(store *blackboard.Store, dockerBin string, timeout time.Duration) *DockerManager {
	if strings.TrimSpace(dockerBin) == "" {
		dockerBin = "docker"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &DockerManager{
		store:     store,
		docker:    dockerBin,
		timeout:   timeout,
		sandboxes: map[string]Sandbox{},
	}
}

func (m *DockerManager) Create(ctx context.Context, request CreateRequest) (Sandbox, error) {
	if strings.TrimSpace(request.RunID) == "" {
		return Sandbox{}, errors.New("run_id is required")
	}
	if strings.TrimSpace(request.ComposeFile) == "" {
		return Sandbox{}, errors.New("compose_file is required")
	}

	sandboxID := fmt.Sprintf("sandbox_%d", time.Now().UTC().UnixNano())
	workspaceDir := filepath.Join(m.store.RunDir(request.RunID), "sandbox", sandboxID)
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return Sandbox{}, err
	}

	if _, _, _, err := m.runDocker(
		ctx,
		[]string{"compose", "-f", request.ComposeFile, "up", "-d"},
		request.Env,
		workspaceDir,
	); err != nil {
		return Sandbox{}, err
	}

	containerStdout, _, _, err := m.runDocker(
		ctx,
		[]string{"compose", "-f", request.ComposeFile, "ps", "-q"},
		request.Env,
		workspaceDir,
	)
	if err != nil {
		return Sandbox{}, err
	}
	containerIDs := splitLines(containerStdout)

	inspectStdout := ""
	if len(containerIDs) > 0 {
		args := append([]string{"inspect"}, containerIDs...)
		inspectStdout, _, _, err = m.runDocker(ctx, args, request.Env, workspaceDir)
		if err != nil {
			return Sandbox{}, err
		}
	}

	sandbox := Sandbox{
		ID:           sandboxID,
		RunID:        request.RunID,
		WorkspaceDir: workspaceDir,
		ContainerIDs: containerIDs,
		Env:          copyEnv(request.Env),
		CreatedAt:    time.Now().UTC(),
	}
	sandbox.Env["COMPOSE_FILE"] = request.ComposeFile
	if err := m.writeEnvironmentSnapshot(ctx, sandbox, request.ComposeFile, inspectStdout); err != nil {
		return Sandbox{}, err
	}

	m.mu.Lock()
	m.sandboxes[sandboxID] = sandbox
	m.mu.Unlock()

	return sandbox, nil
}

func (m *DockerManager) Destroy(ctx context.Context, id string) error {
	sandbox, err := m.getSandbox(id)
	if err != nil {
		return err
	}

	composeFile, ok := sandbox.Env["COMPOSE_FILE"]
	if !ok || strings.TrimSpace(composeFile) == "" {
		composeFile = filepath.Join(sandbox.WorkspaceDir, "docker-compose.yml")
	}

	logStdout, logStderr, _, _ := m.runDocker(
		ctx,
		[]string{"compose", "-f", composeFile, "logs"},
		sandbox.Env,
		sandbox.WorkspaceDir,
	)
	logPath := filepath.Join(m.store.ArtifactDir(sandbox.RunID), "sandbox", sandbox.ID, "docker-logs.txt")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(logPath, []byte(logStdout+"\n"+logStderr), 0o644); err != nil {
		return err
	}

	_, _, _, err = m.runDocker(
		ctx,
		[]string{"compose", "-f", composeFile, "down", "--volumes"},
		sandbox.Env,
		sandbox.WorkspaceDir,
	)
	if err != nil {
		return err
	}

	if err := m.store.CreateEvidence(ctx, model.Evidence{
		SchemaVersion: model.CurrentSchemaVersion,
		EvidenceID:    sandbox.ID + "_docker_logs",
		RunID:         sandbox.RunID,
		Kind:          model.EvidenceKindLog,
		Path:          logPath,
		MIME:          "text/plain",
		Bytes:         int64(len(logStdout) + len(logStderr)),
		SummaryFields: map[string]string{
			"sandbox_id": sandbox.ID,
		},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}

	m.mu.Lock()
	delete(m.sandboxes, id)
	m.mu.Unlock()
	return nil
}

func (m *DockerManager) Exec(ctx context.Context, id string, command []string) (ExecResult, error) {
	sandbox, err := m.getSandbox(id)
	if err != nil {
		return ExecResult{}, err
	}
	if len(command) == 0 {
		return ExecResult{}, errors.New("command is required")
	}
	if len(sandbox.ContainerIDs) == 0 {
		return ExecResult{}, errors.New("sandbox has no containers")
	}
	args := append([]string{"exec", sandbox.ContainerIDs[0]}, command...)
	stdout, stderr, exitCode, err := m.runDocker(ctx, args, sandbox.Env, sandbox.WorkspaceDir)
	return ExecResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
	}, err
}

func (m *DockerManager) getSandbox(id string) (Sandbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sandbox, ok := m.sandboxes[id]
	if !ok {
		return Sandbox{}, fmt.Errorf("sandbox not found: %s", id)
	}
	return sandbox, nil
}

func (m *DockerManager) writeEnvironmentSnapshot(ctx context.Context, sandbox Sandbox, composeFile, inspectOutput string) error {
	snapshotPath := filepath.Join(m.store.ArtifactDir(sandbox.RunID), "sandbox", sandbox.ID, "environment-snapshot.json")
	if err := os.MkdirAll(filepath.Dir(snapshotPath), 0o755); err != nil {
		return err
	}

	payload := map[string]any{
		"sandbox":      sandbox,
		"compose_file": composeFile,
		"inspected_at": time.Now().UTC().Format(time.RFC3339Nano),
		"inspect_raw":  inspectOutput,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(snapshotPath, raw, 0o644); err != nil {
		return err
	}
	return m.store.CreateEvidence(ctx, model.Evidence{
		SchemaVersion: model.CurrentSchemaVersion,
		EvidenceID:    sandbox.ID + "_env_snapshot",
		RunID:         sandbox.RunID,
		Kind:          model.EvidenceKindTranscript,
		Path:          snapshotPath,
		MIME:          "application/json",
		Bytes:         int64(len(raw)),
		SummaryFields: map[string]string{
			"sandbox_id": sandbox.ID,
		},
		CreatedAt: time.Now().UTC(),
	})
}

func (m *DockerManager) runDocker(ctx context.Context, args []string, env map[string]string, workingDir string) (stdout string, stderr string, exitCode int, err error) {
	runCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, m.docker, args...)
	cmd.Dir = workingDir
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}

	var stdoutBuffer strings.Builder
	var stderrBuffer strings.Builder
	cmd.Stdout = &stdoutBuffer
	cmd.Stderr = &stderrBuffer

	err = cmd.Run()
	stdout = stdoutBuffer.String()
	stderr = stderrBuffer.String()
	exitCode = 0
	if err != nil {
		exitCode = -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	return stdout, stderr, exitCode, err
}

func splitLines(input string) []string {
	rawLines := strings.Split(input, "\n")
	result := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func copyEnv(env map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range env {
		out[key] = value
	}
	return out
}
