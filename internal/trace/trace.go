package trace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
)

type Recorder struct {
	store  *blackboard.Store
	logger *slog.Logger
}

type CaptureRequest struct {
	RunID      string
	TaskID     string
	Runner     string
	Command    []string
	InputJSON  []byte
	WorkingDir string
	Timeout    time.Duration
}

func NewLogger(out io.Writer, jsonOutput bool) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}
	if jsonOutput {
		return slog.New(slog.NewJSONHandler(out, opts))
	}
	return slog.New(slog.NewTextHandler(out, opts))
}

func NewRecorder(store *blackboard.Store, logger *slog.Logger) *Recorder {
	if logger == nil {
		logger = NewLogger(os.Stdout, true)
	}
	return &Recorder{
		store:  store,
		logger: logger,
	}
}

func (r *Recorder) CaptureSubprocess(ctx context.Context, request CaptureRequest) (model.ActionTrace, error) {
	startedAt := time.Now().UTC()
	traceID := fmt.Sprintf("trace_%d", startedAt.UnixNano())
	traceDir := filepath.Join(r.store.ArtifactDir(request.RunID), "traces", traceID)
	if err := os.MkdirAll(traceDir, 0o755); err != nil {
		return model.ActionTrace{}, err
	}

	stdinPath := filepath.Join(traceDir, "stdin.json")
	stdoutPath := filepath.Join(traceDir, "stdout.log")
	stderrPath := filepath.Join(traceDir, "stderr.log")
	tracePath := filepath.Join(traceDir, "action-trace.json")

	if err := os.WriteFile(stdinPath, request.InputJSON, 0o644); err != nil {
		return model.ActionTrace{}, err
	}

	if len(request.Command) == 0 {
		return model.ActionTrace{}, fmt.Errorf("command is required")
	}

	timeout := request.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, request.Command[0], request.Command[1:]...)
	if request.WorkingDir != "" {
		cmd.Dir = request.WorkingDir
	}
	cmd.Stdin = bytes.NewReader(request.InputJSON)

	stdoutBuffer := new(bytes.Buffer)
	stderrBuffer := new(bytes.Buffer)
	cmd.Stdout = stdoutBuffer
	cmd.Stderr = stderrBuffer

	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	if err := os.WriteFile(stdoutPath, stdoutBuffer.Bytes(), 0o644); err != nil {
		return model.ActionTrace{}, err
	}
	if err := os.WriteFile(stderrPath, stderrBuffer.Bytes(), 0o644); err != nil {
		return model.ActionTrace{}, err
	}

	finishedAt := time.Now().UTC()
	actionTrace := model.ActionTrace{
		SchemaVersion: model.CurrentSchemaVersion,
		TraceID:       traceID,
		RunID:         request.RunID,
		TaskID:        request.TaskID,
		Runner:        request.Runner,
		Command:       request.Command,
		StdinPath:     stdinPath,
		StdoutPath:    stdoutPath,
		StderrPath:    stderrPath,
		ExitCode:      exitCode,
		StartedAt:     startedAt,
		FinishedAt:    finishedAt,
	}
	if err := actionTrace.Validate(); err != nil {
		return model.ActionTrace{}, err
	}

	rawTrace, err := json.MarshalIndent(actionTrace, "", "  ")
	if err != nil {
		return model.ActionTrace{}, err
	}
	if err := os.WriteFile(tracePath, rawTrace, 0o644); err != nil {
		return model.ActionTrace{}, err
	}

	if err := r.store.CreateEvidence(ctx, model.Evidence{
		SchemaVersion: model.CurrentSchemaVersion,
		EvidenceID:    traceID + "_trace",
		RunID:         request.RunID,
		Kind:          model.EvidenceKindTrace,
		Path:          tracePath,
		MIME:          "application/json",
		Bytes:         int64(len(rawTrace)),
		SummaryFields: map[string]string{
			"trace_id": traceID,
			"runner":   request.Runner,
		},
		CreatedAt: finishedAt,
	}); err != nil {
		return model.ActionTrace{}, err
	}

	if err := r.store.CreateEvidence(ctx, model.Evidence{
		SchemaVersion: model.CurrentSchemaVersion,
		EvidenceID:    traceID + "_stdout",
		RunID:         request.RunID,
		Kind:          model.EvidenceKindLog,
		Path:          stdoutPath,
		MIME:          "text/plain",
		Bytes:         int64(stdoutBuffer.Len()),
		SummaryFields: map[string]string{
			"trace_id": traceID,
			"stream":   "stdout",
		},
		CreatedAt: finishedAt,
	}); err != nil {
		return model.ActionTrace{}, err
	}

	if err := r.store.CreateEvidence(ctx, model.Evidence{
		SchemaVersion: model.CurrentSchemaVersion,
		EvidenceID:    traceID + "_stderr",
		RunID:         request.RunID,
		Kind:          model.EvidenceKindLog,
		Path:          stderrPath,
		MIME:          "text/plain",
		Bytes:         int64(stderrBuffer.Len()),
		SummaryFields: map[string]string{
			"trace_id": traceID,
			"stream":   "stderr",
		},
		CreatedAt: finishedAt,
	}); err != nil {
		return model.ActionTrace{}, err
	}

	r.logger.Info("captured subprocess trace",
		"run_id", request.RunID,
		"task_id", request.TaskID,
		"trace_id", traceID,
		"exit_code", exitCode,
	)

	return actionTrace, runErr
}
