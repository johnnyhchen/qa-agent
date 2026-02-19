package web

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
	StartURLs  []string          `json:"start_urls"`
	Steps      []string          `json:"steps"`
	Assertions []string          `json:"assertions"`
	TestData   map[string]string `json:"test_data,omitempty"`
}

type Adapter struct {
	binary     string
	subprocess *runner.SubprocessRunner
}

func NewAdapter(binary string, timeout time.Duration, recorder *trace.Recorder) *Adapter {
	if strings.TrimSpace(binary) == "" {
		binary = "ai-browser-use"
	}
	return &Adapter{
		binary: binary,
		subprocess: &runner.SubprocessRunner{
			Name:          "web-ai-browser-use",
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
		return fmt.Errorf("web runner binary not found (%s): %w", a.binary, err)
	}

	versionCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(versionCtx, path, "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("web runner binary is not callable (%s --version): %w", path, err)
	}
	return nil
}

func (a *Adapter) Run(ctx context.Context, task model.Task, env sandbox.Sandbox, artifactDir string) (runner.Result, error) {
	if _, err := ParseTaskSpec(task); err != nil {
		return runner.Result{}, err
	}
	return a.subprocess.Run(ctx, task, env, artifactDir)
}

func ParseTaskSpec(task model.Task) (TaskSpec, error) {
	var spec TaskSpec
	if task.Payload == nil {
		return TaskSpec{}, errors.New("web task payload is required")
	}
	spec = TaskSpec{
		TestData: map[string]string{},
	}

	rawStartURLs, ok := task.Payload["start_urls"].([]any)
	if !ok || len(rawStartURLs) == 0 {
		return TaskSpec{}, errors.New("web task payload.start_urls is required")
	}
	for _, value := range rawStartURLs {
		url, ok := value.(string)
		if !ok || strings.TrimSpace(url) == "" {
			return TaskSpec{}, errors.New("web task payload.start_urls must be strings")
		}
		spec.StartURLs = append(spec.StartURLs, strings.TrimSpace(url))
	}

	rawSteps, ok := task.Payload["steps"].([]any)
	if !ok || len(rawSteps) == 0 {
		return TaskSpec{}, errors.New("web task payload.steps is required")
	}
	for _, value := range rawSteps {
		step, ok := value.(string)
		if !ok || strings.TrimSpace(step) == "" {
			return TaskSpec{}, errors.New("web task payload.steps must be strings")
		}
		spec.Steps = append(spec.Steps, strings.TrimSpace(step))
	}

	rawAssertions, ok := task.Payload["assertions"].([]any)
	if !ok || len(rawAssertions) == 0 {
		return TaskSpec{}, errors.New("web task payload.assertions is required")
	}
	for _, value := range rawAssertions {
		assertion, ok := value.(string)
		if !ok || strings.TrimSpace(assertion) == "" {
			return TaskSpec{}, errors.New("web task payload.assertions must be strings")
		}
		spec.Assertions = append(spec.Assertions, strings.TrimSpace(assertion))
	}

	if rawData, ok := task.Payload["test_data"].(map[string]any); ok {
		for key, value := range rawData {
			if text, ok := value.(string); ok {
				spec.TestData[key] = text
			}
		}
	}

	return spec, nil
}
