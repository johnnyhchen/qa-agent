package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	judgeagent "qa-agent/internal/agents/judge"
	planneragent "qa-agent/internal/agents/planner"
	"qa-agent/internal/agents/runtime"
	"qa-agent/internal/blackboard"
	"qa-agent/internal/config"
	"qa-agent/internal/model"
	"qa-agent/internal/orchestrator"
	"qa-agent/internal/replay"
	"qa-agent/internal/report"
	apirunner "qa-agent/internal/runner/api"
	iosrunner "qa-agent/internal/runner/ios"
	macosrunner "qa-agent/internal/runner/macos"
	webrunner "qa-agent/internal/runner/web"
	"qa-agent/internal/trace"
)

const version = "0.1.0"

func main() {
	exitCode := execute(os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(exitCode)
}

func execute(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printHelp(stdout)
		return 0
	}

	switch args[0] {
	case "--help", "-h", "help":
		printHelp(stdout)
		return 0
	case "--version", "-v", "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "run":
		if err := runCommand(args[1:], stdout, stderr); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			fmt.Fprintf(stderr, "run: %v\n", err)
			return 1
		}
		return 0
	case "replay":
		if err := replayCommand(args[1:], stdout, stderr); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			fmt.Fprintf(stderr, "replay: %v\n", err)
			return 1
		}
		return 0
	case "trace":
		if err := traceCommand(args[1:], stdout, stderr); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			fmt.Fprintf(stderr, "trace: %v\n", err)
			return 1
		}
		return 0
	case "report":
		if err := reportCommand(args[1:], stdout, stderr); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			fmt.Fprintf(stderr, "report: %v\n", err)
			return 1
		}
		return 0
	case "bundle":
		if err := bundleCommand(args[1:], stdout, stderr); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			fmt.Fprintf(stderr, "bundle: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		printHelp(stderr)
		return 1
	}
}

func runCommand(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var configPath string
	var feature string
	var surfacesCSV string
	var budgetSteps int
	var budgetMinutes int
	var outputDir string

	fs.StringVar(&configPath, "config", "", "Path to config file")
	fs.StringVar(&feature, "feature", "", "Feature description text")
	fs.StringVar(&surfacesCSV, "surfaces", "web", "Comma-separated surfaces (web,api,macos,ios)")
	fs.IntVar(&budgetSteps, "budget-steps", 200, "Step budget for run")
	fs.IntVar(&budgetMinutes, "budget-minutes", 30, "Wall time budget in minutes")
	fs.StringVar(&outputDir, "output-dir", "", "Override output directory")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(feature) == "" {
		return errors.New("--feature is required")
	}

	overrides := config.CLIOverrides{}
	if strings.TrimSpace(outputDir) != "" {
		overrides.OutputDir = &outputDir
	}
	cfg, err := config.Load(configPath, overrides)
	if err != nil {
		return err
	}

	surfaces := parseCSV(surfacesCSV)
	if len(surfaces) == 0 {
		return errors.New("--surfaces must include at least one value")
	}

	runID := fmt.Sprintf("run_%d", time.Now().UnixNano())
	runDir := config.RunDir(cfg.OutputDir, runID)
	artifactDir := filepath.Join(runDir, "artifacts")

	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return err
	}

	metadata := map[string]any{
		"run_id":         runID,
		"feature":        feature,
		"surfaces":       surfaces,
		"budget_steps":   budgetSteps,
		"budget_minutes": budgetMinutes,
		"created_at":     time.Now().UTC().Format(time.RFC3339),
		"artifacts_dir":  artifactDir,
	}
	raw, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), raw, 0o644); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "run_id: %s\n", runID)
	fmt.Fprintf(stdout, "artifacts: %s\n", runDir)

	// ── Wire up and run the orchestrator ────────────────────────────
	store, err := blackboard.NewStore(cfg.OutputDir)
	if err != nil {
		return fmt.Errorf("creating store: %w", err)
	}

	registry := runtime.NewToolRegistry()
	rt := runtime.New(store, registry, nil, runtime.Config{})

	plan := planneragent.New(rt, store)
	judge := judgeagent.New()

	logger := trace.NewLogger(stderr, false)
	recorder := trace.NewRecorder(store, logger)

	executors := map[model.Surface]orchestrator.Executor{
		model.SurfaceAPI:   apirunner.NewAdapter(30 * time.Second),
		model.SurfaceWeb:   webrunner.NewAdapter(cfg.ToolBins.AIBrowserUseBin, 2*time.Minute, recorder),
		model.SurfaceMacOS: macosrunner.NewAdapter(cfg.ToolBins.AIComputerUseBin, 2*time.Minute, 50, recorder),
		model.SurfaceIOS:   iosrunner.NewAdapter("xcrun"),
	}

	budget := orchestrator.Budget{
		MaxQueuedTasks:          budgetSteps,
		MaxNewTasksPerJudgeTurn: 10,
		MaxJudgeTurns:           3,
		MaxWallTime:             time.Duration(budgetMinutes) * time.Minute,
		MaxRetriesPerTask:       3,
	}

	surfaceModels := make([]model.Surface, len(surfaces))
	for i, s := range surfaces {
		surfaceModels[i] = model.Surface(s)
	}

	orch := orchestrator.New(store, plan, judge, executors, budget)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(budgetMinutes)*time.Minute)
	defer cancel()

	verdict, err := orch.Run(ctx, orchestrator.Request{
		RunID:       runID,
		Description: feature,
		Surfaces:    surfaceModels,
	})
	if err != nil {
		fmt.Fprintf(stderr, "orchestrator error: %v\n", err)
	}

	// Write verdict to run directory
	verdictRaw, _ := json.MarshalIndent(verdict, "", "  ")
	_ = os.WriteFile(filepath.Join(runDir, "verdict.json"), verdictRaw, 0o644)

	fmt.Fprintf(stdout, "verdict: %s\n", verdict.Status)
	return nil
}

func replayCommand(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var configPath string
	var runID string
	var outputDir string
	var taskID string

	fs.StringVar(&configPath, "config", "", "Path to config file")
	fs.StringVar(&runID, "run-id", "", "Run ID")
	fs.StringVar(&outputDir, "output-dir", "", "Override output directory")
	fs.StringVar(&taskID, "task-id", "", "Task ID to replay")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(runID) == "" {
		return errors.New("--run-id is required")
	}

	overrides := config.CLIOverrides{}
	if strings.TrimSpace(outputDir) != "" {
		overrides.OutputDir = &outputDir
	}
	cfg, err := config.Load(configPath, overrides)
	if err != nil {
		return err
	}
	if err := ensureRunExists(cfg, runID); err != nil {
		return err
	}

	if strings.TrimSpace(taskID) == "" {
		return errors.New("--task-id is required")
	}
	runDir := config.RunDir(cfg.OutputDir, runID)
	result, err := replay.ReplayTask(context.Background(), runDir, taskID, 2*time.Minute)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "replay_id: %s\n", result.ReplayID)
	fmt.Fprintf(stdout, "trace: %s\n", result.TracePath)
	fmt.Fprintf(stdout, "stdout: %s\n", result.StdoutPath)
	fmt.Fprintf(stdout, "stderr: %s\n", result.StderrPath)
	fmt.Fprintf(stdout, "exit_code: %d\n", result.ExitCode)
	return nil
}

func reportCommand(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var configPath string
	var runID string
	var outputDir string

	fs.StringVar(&configPath, "config", "", "Path to config file")
	fs.StringVar(&runID, "run-id", "", "Run ID")
	fs.StringVar(&outputDir, "output-dir", "", "Override output directory")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(runID) == "" {
		return errors.New("--run-id is required")
	}

	overrides := config.CLIOverrides{}
	if strings.TrimSpace(outputDir) != "" {
		overrides.OutputDir = &outputDir
	}
	cfg, err := config.Load(configPath, overrides)
	if err != nil {
		return err
	}
	if err := ensureRunExists(cfg, runID); err != nil {
		return err
	}

	runDir := config.RunDir(cfg.OutputDir, runID)
	reportPath, manifestPath, err := report.NewGenerator().Write(runID, runDir)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "report: %s\n", reportPath)
	fmt.Fprintf(stdout, "manifest: %s\n", manifestPath)
	return nil
}

func bundleCommand(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("bundle", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var configPath string
	var runID string
	var outputDir string
	var outZip string

	fs.StringVar(&configPath, "config", "", "Path to config file")
	fs.StringVar(&runID, "run-id", "", "Run ID")
	fs.StringVar(&outputDir, "output-dir", "", "Override output directory")
	fs.StringVar(&outZip, "out", "", "Output bundle zip path")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(runID) == "" {
		return errors.New("--run-id is required")
	}

	overrides := config.CLIOverrides{}
	if strings.TrimSpace(outputDir) != "" {
		overrides.OutputDir = &outputDir
	}
	cfg, err := config.Load(configPath, overrides)
	if err != nil {
		return err
	}
	if err := ensureRunExists(cfg, runID); err != nil {
		return err
	}

	runDir := config.RunDir(cfg.OutputDir, runID)
	if strings.TrimSpace(outZip) == "" {
		outZip = filepath.Join(runDir, runID+"-bundle.zip")
	}
	if err := report.NewGenerator().Bundle(runID, runDir, outZip); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "bundle: %s\n", outZip)
	return nil
}

func traceCommand(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("trace", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var configPath string
	var runID string
	var outputDir string
	var taskID string

	fs.StringVar(&configPath, "config", "", "Path to config file")
	fs.StringVar(&runID, "run-id", "", "Run ID")
	fs.StringVar(&outputDir, "output-dir", "", "Override output directory")
	fs.StringVar(&taskID, "task-id", "", "Optional task ID filter")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(runID) == "" {
		return errors.New("--run-id is required")
	}
	overrides := config.CLIOverrides{}
	if strings.TrimSpace(outputDir) != "" {
		overrides.OutputDir = &outputDir
	}
	cfg, err := config.Load(configPath, overrides)
	if err != nil {
		return err
	}
	if err := ensureRunExists(cfg, runID); err != nil {
		return err
	}
	runDir := config.RunDir(cfg.OutputDir, runID)
	entries, err := replay.ListTraces(runDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if taskID != "" && entry.TaskID != taskID {
			continue
		}
		fmt.Fprintf(stdout, "trace_id=%s task_id=%s runner=%s exit_code=%d path=%s\n", entry.TraceID, entry.TaskID, entry.Runner, entry.ExitCode, entry.Path)
	}
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "no traces found")
	}
	return nil
}

func ensureRunExists(cfg config.Config, runID string) error {
	runDir := config.RunDir(cfg.OutputDir, runID)
	info, err := os.Stat(runDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("run %q not found in %s", runID, cfg.OutputDir)
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("run path is not a directory: %s", runDir)
	}
	return nil
}

func parseCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, "qa-agent validates feature behavior against local surfaces.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  qa-agent [command] [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  run       Start a validation run")
	fmt.Fprintln(out, "  replay    Replay a task from a run")
	fmt.Fprintln(out, "  report    Build a run report")
	fmt.Fprintln(out, "  bundle    Package a run artifact bundle")
	fmt.Fprintln(out, "  trace     Inspect run traces")
	fmt.Fprintln(out, "  help      Show this help")
}
