package config

import (
	"os"
	"path/filepath"
	"testing"
)

type mapEnv map[string]string

func (m mapEnv) LookupEnv(key string) (string, bool) {
	v, ok := m[key]
	return v, ok
}

func TestLoadWithEnv_Defaults(t *testing.T) {
	cfg, err := LoadWithEnv("", mapEnv{}, CLIOverrides{})
	if err != nil {
		t.Fatalf("LoadWithEnv() error = %v", err)
	}

	if cfg.OutputDir != defaultOutputDir {
		t.Fatalf("OutputDir = %q, want %q", cfg.OutputDir, defaultOutputDir)
	}
	if cfg.ToolBins.AIBrowserUseBin != defaultAIBrowserUse {
		t.Fatalf("AIBrowserUseBin = %q, want %q", cfg.ToolBins.AIBrowserUseBin, defaultAIBrowserUse)
	}
	if cfg.ToolBins.AIComputerUseBin != defaultAIComputerUse {
		t.Fatalf("AIComputerUseBin = %q, want %q", cfg.ToolBins.AIComputerUseBin, defaultAIComputerUse)
	}
	if cfg.ToolBins.DockerBin != defaultDockerBin {
		t.Fatalf("DockerBin = %q, want %q", cfg.ToolBins.DockerBin, defaultDockerBin)
	}
}

func TestLoadWithEnv_Precedence(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "qa-agent.json")
	err := os.WriteFile(path, []byte(`{
  "output_dir": "from-file",
  "tool_bins": {
    "ai_browser_use_bin": "browser-file",
    "ai_computer_use_bin": "computer-file",
    "docker_bin": "docker-file"
  }
}`), 0o644)
	if err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	env := mapEnv{
		"QA_AGENT_OUTPUT_DIR": "from-env",
		"AI_BROWSER_USE_BIN":  "browser-env",
		"AI_COMPUTER_USE_BIN": "computer-env",
		"DOCKER_BIN":          "docker-env",
	}

	overrideOutput := "from-flag"
	overrideBrowser := "browser-flag"

	cfg, err := LoadWithEnv(path, env, CLIOverrides{
		OutputDir:       &overrideOutput,
		AIBrowserUseBin: &overrideBrowser,
	})
	if err != nil {
		t.Fatalf("LoadWithEnv() error = %v", err)
	}

	if cfg.OutputDir != "from-flag" {
		t.Fatalf("OutputDir = %q, want %q", cfg.OutputDir, "from-flag")
	}
	if cfg.ToolBins.AIBrowserUseBin != "browser-flag" {
		t.Fatalf("AIBrowserUseBin = %q, want %q", cfg.ToolBins.AIBrowserUseBin, "browser-flag")
	}
	if cfg.ToolBins.AIComputerUseBin != "computer-env" {
		t.Fatalf("AIComputerUseBin = %q, want %q", cfg.ToolBins.AIComputerUseBin, "computer-env")
	}
	if cfg.ToolBins.DockerBin != "docker-env" {
		t.Fatalf("DockerBin = %q, want %q", cfg.ToolBins.DockerBin, "docker-env")
	}
}
