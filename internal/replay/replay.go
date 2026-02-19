package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"qa-agent/internal/model"
)

type TraceEntry struct {
	Path      string
	TraceID   string
	TaskID    string
	Runner    string
	ExitCode  int
	StartedAt time.Time
}

type ReplayResult struct {
	ReplayID   string
	TaskID     string
	TracePath  string
	StdoutPath string
	StderrPath string
	ExitCode   int
}

func ListTraces(runDir string) ([]TraceEntry, error) {
	root := filepath.Join(runDir, "artifacts")
	var entries []TraceEntry
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "action-trace.json" {
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var trace model.ActionTrace
		if err := json.Unmarshal(raw, &trace); err != nil {
			return nil
		}
		entries = append(entries, TraceEntry{
			Path:      path,
			TraceID:   trace.TraceID,
			TaskID:    trace.TaskID,
			Runner:    trace.Runner,
			ExitCode:  trace.ExitCode,
			StartedAt: trace.StartedAt,
		})
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].StartedAt.Before(entries[j].StartedAt)
	})
	return entries, nil
}

func ReplayTask(ctx context.Context, runDir, taskID string, timeout time.Duration) (ReplayResult, error) {
	if strings.TrimSpace(taskID) == "" {
		return ReplayResult{}, errors.New("task_id is required")
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	traces, err := ListTraces(runDir)
	if err != nil {
		return ReplayResult{}, err
	}
	var selected TraceEntry
	found := false
	for i := len(traces) - 1; i >= 0; i-- {
		if traces[i].TaskID == taskID {
			selected = traces[i]
			found = true
			break
		}
	}
	if !found {
		return ReplayResult{}, fmt.Errorf("no trace found for task %s", taskID)
	}

	raw, err := os.ReadFile(selected.Path)
	if err != nil {
		return ReplayResult{}, err
	}
	var original model.ActionTrace
	if err := json.Unmarshal(raw, &original); err != nil {
		return ReplayResult{}, err
	}
	if len(original.Command) == 0 {
		return ReplayResult{}, errors.New("trace command is empty")
	}

	replayID := fmt.Sprintf("replay_%d", time.Now().UTC().UnixNano())
	replayDir := filepath.Join(runDir, "artifacts", "replays", replayID)
	if err := os.MkdirAll(replayDir, 0o755); err != nil {
		return ReplayResult{}, err
	}
	command := rewriteOutputArg(original.Command, filepath.Join(replayDir, taskID+"-output.json"))
	stdoutPath := filepath.Join(replayDir, "stdout.log")
	stderrPath := filepath.Join(replayDir, "stderr.log")

	replayCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(replayCtx, command[0], command[1:]...)
	if original.StdinPath != "" {
		inputRaw, _ := os.ReadFile(original.StdinPath)
		cmd.Stdin = bytes.NewReader(inputRaw)
	}
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
		return ReplayResult{}, err
	}
	if err := os.WriteFile(stderrPath, stderrBuffer.Bytes(), 0o644); err != nil {
		return ReplayResult{}, err
	}

	trace := model.ActionTrace{
		SchemaVersion: model.CurrentSchemaVersion,
		TraceID:       replayID,
		RunID:         original.RunID,
		TaskID:        original.TaskID,
		Runner:        original.Runner,
		Command:       command,
		StdinPath:     original.StdinPath,
		StdoutPath:    stdoutPath,
		StderrPath:    stderrPath,
		ExitCode:      exitCode,
		StartedAt:     time.Now().UTC().Add(-time.Millisecond),
		FinishedAt:    time.Now().UTC(),
	}
	traceRaw, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return ReplayResult{}, err
	}
	tracePath := filepath.Join(replayDir, "action-trace.json")
	if err := os.WriteFile(tracePath, traceRaw, 0o644); err != nil {
		return ReplayResult{}, err
	}
	if runErr != nil {
		return ReplayResult{
			ReplayID:   replayID,
			TaskID:     taskID,
			TracePath:  tracePath,
			StdoutPath: stdoutPath,
			StderrPath: stderrPath,
			ExitCode:   exitCode,
		}, runErr
	}
	return ReplayResult{
		ReplayID:   replayID,
		TaskID:     taskID,
		TracePath:  tracePath,
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
		ExitCode:   exitCode,
	}, nil
}

func rewriteOutputArg(command []string, outputPath string) []string {
	rewritten := make([]string, 0, len(command)+2)
	rewritten = append(rewritten, command...)
	for idx := 0; idx < len(rewritten); idx++ {
		if rewritten[idx] == "--output" && idx+1 < len(rewritten) {
			rewritten[idx+1] = outputPath
			return rewritten
		}
	}
	rewritten = append(rewritten, "--output", outputPath)
	return rewritten
}
