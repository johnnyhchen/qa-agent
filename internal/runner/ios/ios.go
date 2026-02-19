package ios

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"qa-agent/internal/model"
	"qa-agent/internal/runner"
	"qa-agent/internal/sandbox"
)

type TaskSpec struct {
	AppBundleID   string   `json:"app_bundle_id"`
	DeviceProfile string   `json:"device_profile"`
	Steps         []string `json:"steps"`
	Assertions    []string `json:"assertions"`
}

type Adapter struct {
	xcrunBin string
}

func NewAdapter(xcrunBin string) *Adapter {
	if strings.TrimSpace(xcrunBin) == "" {
		xcrunBin = "xcrun"
	}
	return &Adapter{
		xcrunBin: xcrunBin,
	}
}

func (a *Adapter) Run(_ context.Context, task model.Task, _ sandbox.Sandbox, artifactDir string) (runner.Result, error) {
	_, err := ParseTaskSpec(task)
	if err != nil {
		return runner.Result{}, err
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return runner.Result{}, err
	}

	diagnostics := map[string]any{
		"runner":    "ios-stub",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"status":    "blocked",
	}
	if _, err := exec.LookPath(a.xcrunBin); err != nil {
		diagnostics["reason"] = "iOS tooling not configured"
		diagnostics["detail"] = fmt.Sprintf("required binary %q not found in PATH", a.xcrunBin)
		diagPath := filepath.Join(artifactDir, "ios-diagnostics.json")
		raw, marshalErr := json.MarshalIndent(diagnostics, "", "  ")
		if marshalErr == nil {
			_ = os.WriteFile(diagPath, raw, 0o644)
		}
		return runner.Result{
			Outcome:       model.RunOutcomeBlocked,
			Summary:       diagnostics["reason"].(string),
			EvidenceFiles: []string{diagPath},
			StabilityHints: []string{
				"experimental_surface_ios",
				"requires_xctest_driver",
			},
		}, nil
	}

	return runner.Result{
		Outcome: model.RunOutcomeBlocked,
		Summary: "iOS runner is a stub; XCUITest driver integration not implemented",
		StabilityHints: []string{
			"experimental_surface_ios",
			"stub_runner",
		},
	}, nil
}

func ParseTaskSpec(task model.Task) (TaskSpec, error) {
	if task.Payload == nil {
		return TaskSpec{}, errors.New("ios task payload is required")
	}
	spec := TaskSpec{}

	if bundleID, ok := task.Payload["app_bundle_id"].(string); ok && strings.TrimSpace(bundleID) != "" {
		spec.AppBundleID = strings.TrimSpace(bundleID)
	} else {
		return TaskSpec{}, errors.New("ios payload.app_bundle_id is required")
	}
	if profile, ok := task.Payload["device_profile"].(string); ok && strings.TrimSpace(profile) != "" {
		spec.DeviceProfile = strings.TrimSpace(profile)
	} else {
		return TaskSpec{}, errors.New("ios payload.device_profile is required")
	}

	rawSteps, ok := task.Payload["steps"].([]any)
	if !ok || len(rawSteps) == 0 {
		return TaskSpec{}, errors.New("ios payload.steps is required")
	}
	for _, value := range rawSteps {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return TaskSpec{}, errors.New("ios payload.steps must be strings")
		}
		spec.Steps = append(spec.Steps, strings.TrimSpace(text))
	}

	rawAssertions, ok := task.Payload["assertions"].([]any)
	if !ok || len(rawAssertions) == 0 {
		return TaskSpec{}, errors.New("ios payload.assertions is required")
	}
	for _, value := range rawAssertions {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return TaskSpec{}, errors.New("ios payload.assertions must be strings")
		}
		spec.Assertions = append(spec.Assertions, strings.TrimSpace(text))
	}

	return spec, nil
}
