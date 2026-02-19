package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"qa-agent/internal/model"
	"qa-agent/internal/sandbox"
	"qa-agent/internal/trace"
)

type Runner interface {
	Run(ctx context.Context, task model.Task, env sandbox.Sandbox, artifactDir string) (Result, error)
}

type Result struct {
	Outcome        model.RunOutcome `json:"outcome"`
	Summary        string           `json:"summary"`
	ActionTraceRef string           `json:"action_trace_ref"`
	EvidenceFiles  []string         `json:"evidence_files,omitempty"`
	StabilityHints []string         `json:"stability_hints,omitempty"`
	ExitCode       int              `json:"exit_code"`
}

type CLIInput struct {
	Task        model.Task      `json:"task"`
	Sandbox     sandbox.Sandbox `json:"sandbox"`
	ArtifactDir string          `json:"artifact_dir"`
}

type CLIOutput struct {
	Outcome        model.RunOutcome `json:"outcome"`
	Summary        string           `json:"summary"`
	EvidenceFiles  []string         `json:"evidence_files,omitempty"`
	StabilityHints []string         `json:"stability_hints,omitempty"`
}

type SubprocessRunner struct {
	Name          string
	Binary        string
	BaseArgs      []string
	Timeout       time.Duration
	TraceRecorder *trace.Recorder
}

func (s *SubprocessRunner) Run(ctx context.Context, task model.Task, env sandbox.Sandbox, artifactDir string) (Result, error) {
	if task.RunID == "" {
		return Result{}, errors.New("task.run_id is required")
	}
	if task.TaskID == "" {
		return Result{}, errors.New("task.task_id is required")
	}
	if s.Binary == "" {
		return Result{}, errors.New("runner binary is required")
	}
	if s.TraceRecorder == nil {
		return Result{}, errors.New("trace recorder is required")
	}
	if s.Timeout <= 0 {
		s.Timeout = 5 * time.Minute
	}

	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return Result{}, err
	}

	input := CLIInput{
		Task:        task,
		Sandbox:     env,
		ArtifactDir: artifactDir,
	}
	inputRaw, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return Result{}, err
	}

	inputPath := filepath.Join(artifactDir, fmt.Sprintf("%s-input.json", task.TaskID))
	outputPath := filepath.Join(artifactDir, fmt.Sprintf("%s-output.json", task.TaskID))
	if err := os.WriteFile(inputPath, inputRaw, 0o644); err != nil {
		return Result{}, err
	}

	command := []string{s.Binary}
	command = append(command, s.BaseArgs...)
	command = append(command, "--input", inputPath, "--output", outputPath)

	actionTrace, runErr := s.TraceRecorder.CaptureSubprocess(ctx, trace.CaptureRequest{
		RunID:      task.RunID,
		TaskID:     task.TaskID,
		Runner:     s.Name,
		Command:    command,
		InputJSON:  inputRaw,
		WorkingDir: artifactDir,
		Timeout:    s.Timeout,
	})

	outputRaw, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		if runErr != nil {
			return Result{}, fmt.Errorf("runner process error: %w", runErr)
		}
		return Result{}, readErr
	}

	var output CLIOutput
	if err := json.Unmarshal(outputRaw, &output); err != nil {
		return Result{}, err
	}
	if !output.Outcome.IsValid() {
		return Result{}, fmt.Errorf("runner output contains invalid outcome: %q", output.Outcome)
	}

	result := Result{
		Outcome:        output.Outcome,
		Summary:        output.Summary,
		ActionTraceRef: actionTrace.TraceID,
		EvidenceFiles:  output.EvidenceFiles,
		StabilityHints: output.StabilityHints,
		ExitCode:       actionTrace.ExitCode,
	}

	if runErr != nil {
		return result, runErr
	}
	return result, nil
}
