package macos

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"qa-agent/internal/model"
	"qa-agent/internal/runner"
	"qa-agent/internal/sandbox"
	"qa-agent/internal/trace"
)

type TaskSpec struct {
	AppBundleID        string   `json:"app_bundle_id"`
	LaunchArgs         []string `json:"launch_args,omitempty"`
	Steps              []string `json:"steps"`
	Assertions         []string `json:"assertions"`
	MaxSteps           int      `json:"max_steps"`
	MaxWallTimeSeconds int      `json:"max_wall_time_seconds"`
}

type Adapter struct {
	binary     string
	maxSteps   int
	maxRuntime time.Duration
	subprocess *runner.SubprocessRunner
}

func NewAdapter(binary string, timeout time.Duration, maxSteps int, recorder *trace.Recorder) *Adapter {
	if strings.TrimSpace(binary) == "" {
		binary = "ai-computer-use"
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if maxSteps <= 0 {
		maxSteps = 100
	}
	return &Adapter{
		binary:     binary,
		maxSteps:   maxSteps,
		maxRuntime: timeout,
		subprocess: &runner.SubprocessRunner{
			Name:          "macos-ai-computer-use",
			Binary:        binary,
			BaseArgs:      []string{"run"},
			Timeout:       timeout,
			TraceRecorder: recorder,
		},
	}
}

func (a *Adapter) Doctor(ctx context.Context) error {
	path, err := exec.LookPath(a.binary)
	if err != nil {
		return fmt.Errorf("macOS runner binary not found (%s): %w", a.binary, err)
	}
	versionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(versionCtx, path, "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("macOS runner binary is not callable (%s --version): %w", path, err)
	}
	return nil
}

func (a *Adapter) Run(ctx context.Context, task model.Task, env sandbox.Sandbox, artifactDir string) (runner.Result, error) {
	spec, err := ParseTaskSpec(task)
	if err != nil {
		return runner.Result{}, err
	}
	if spec.MaxSteps > a.maxSteps {
		return runner.Result{}, fmt.Errorf("macos task max_steps %d exceeds adapter limit %d", spec.MaxSteps, a.maxSteps)
	}
	if spec.MaxWallTimeSeconds > int(a.maxRuntime.Seconds()) {
		return runner.Result{}, fmt.Errorf("macos task max_wall_time_seconds %d exceeds adapter limit %d", spec.MaxWallTimeSeconds, int(a.maxRuntime.Seconds()))
	}

	result, runErr := a.subprocess.Run(ctx, task, env, artifactDir)
	result.StabilityHints = append(result.StabilityHints, "experimental_surface_macos")
	if runErr != nil {
		result.StabilityHints = append(result.StabilityHints, "macos_runner_nondeterminism_possible")
	}
	return result, runErr
}

func ParseTaskSpec(task model.Task) (TaskSpec, error) {
	if task.Payload == nil {
		return TaskSpec{}, errors.New("macos task payload is required")
	}
	spec := TaskSpec{
		MaxSteps:           50,
		MaxWallTimeSeconds: 300,
	}

	if bundleID, ok := task.Payload["app_bundle_id"].(string); ok && strings.TrimSpace(bundleID) != "" {
		spec.AppBundleID = strings.TrimSpace(bundleID)
	} else {
		return TaskSpec{}, errors.New("macos task payload.app_bundle_id is required")
	}

	rawSteps, ok := task.Payload["steps"].([]any)
	if !ok || len(rawSteps) == 0 {
		return TaskSpec{}, errors.New("macos task payload.steps is required")
	}
	for _, item := range rawSteps {
		text, ok := item.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return TaskSpec{}, errors.New("macos task payload.steps must be strings")
		}
		spec.Steps = append(spec.Steps, strings.TrimSpace(text))
	}

	rawAssertions, ok := task.Payload["assertions"].([]any)
	if !ok || len(rawAssertions) == 0 {
		return TaskSpec{}, errors.New("macos task payload.assertions is required")
	}
	for _, item := range rawAssertions {
		text, ok := item.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return TaskSpec{}, errors.New("macos task payload.assertions must be strings")
		}
		spec.Assertions = append(spec.Assertions, strings.TrimSpace(text))
	}

	if rawArgs, ok := task.Payload["launch_args"].([]any); ok {
		for _, item := range rawArgs {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				spec.LaunchArgs = append(spec.LaunchArgs, strings.TrimSpace(text))
			}
		}
	}
	if value, ok := task.Payload["max_steps"].(float64); ok && int(value) > 0 {
		spec.MaxSteps = int(value)
	}
	if value, ok := task.Payload["max_wall_time_seconds"].(float64); ok && int(value) > 0 {
		spec.MaxWallTimeSeconds = int(value)
	}
	return spec, nil
}
