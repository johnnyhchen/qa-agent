package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultOutputDir      = ".qa-agent/runs"
	defaultAIBrowserUse   = "ai-browser-use"
	defaultAIComputerUse  = "ai-computer-use"
	defaultDockerBin      = "docker"
	defaultConfigFilename = "qa-agent.json"
)

type Config struct {
	OutputDir string  `json:"output_dir"`
	ToolBins  ToolBin `json:"tool_bins"`
}

type ToolBin struct {
	AIBrowserUseBin  string `json:"ai_browser_use_bin"`
	AIComputerUseBin string `json:"ai_computer_use_bin"`
	DockerBin        string `json:"docker_bin"`
}

type CLIOverrides struct {
	OutputDir        *string
	AIBrowserUseBin  *string
	AIComputerUseBin *string
	DockerBin        *string
}

type envProvider interface {
	LookupEnv(string) (string, bool)
}

type osEnvProvider struct{}

func (osEnvProvider) LookupEnv(key string) (string, bool) {
	return os.LookupEnv(key)
}

func Load(configPath string, overrides CLIOverrides) (Config, error) {
	return LoadWithEnv(configPath, osEnvProvider{}, overrides)
}

func LoadWithEnv(configPath string, env envProvider, overrides CLIOverrides) (Config, error) {
	cfg := defaultConfig()

	path := strings.TrimSpace(configPath)
	if path == "" {
		if _, err := os.Stat(defaultConfigFilename); err == nil {
			path = defaultConfigFilename
		}
	}

	if path != "" {
		if err := applyFileConfig(path, &cfg); err != nil {
			return Config{}, err
		}
	}

	applyEnv(env, &cfg)
	applyOverrides(overrides, &cfg)

	if strings.TrimSpace(cfg.OutputDir) == "" {
		return Config{}, errors.New("output directory cannot be empty")
	}
	return cfg, nil
}

func defaultConfig() Config {
	return Config{
		OutputDir: defaultOutputDir,
		ToolBins: ToolBin{
			AIBrowserUseBin:  defaultAIBrowserUse,
			AIComputerUseBin: defaultAIComputerUse,
			DockerBin:        defaultDockerBin,
		},
	}
}

func applyFileConfig(path string, cfg *Config) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var fileCfg Config
	if err := json.Unmarshal(raw, &fileCfg); err != nil {
		return err
	}
	if v := strings.TrimSpace(fileCfg.OutputDir); v != "" {
		cfg.OutputDir = v
	}
	if v := strings.TrimSpace(fileCfg.ToolBins.AIBrowserUseBin); v != "" {
		cfg.ToolBins.AIBrowserUseBin = v
	}
	if v := strings.TrimSpace(fileCfg.ToolBins.AIComputerUseBin); v != "" {
		cfg.ToolBins.AIComputerUseBin = v
	}
	if v := strings.TrimSpace(fileCfg.ToolBins.DockerBin); v != "" {
		cfg.ToolBins.DockerBin = v
	}
	return nil
}

func applyEnv(env envProvider, cfg *Config) {
	if v, ok := env.LookupEnv("QA_AGENT_OUTPUT_DIR"); ok && strings.TrimSpace(v) != "" {
		cfg.OutputDir = strings.TrimSpace(v)
	}
	if v, ok := env.LookupEnv("AI_BROWSER_USE_BIN"); ok && strings.TrimSpace(v) != "" {
		cfg.ToolBins.AIBrowserUseBin = strings.TrimSpace(v)
	}
	if v, ok := env.LookupEnv("AI_COMPUTER_USE_BIN"); ok && strings.TrimSpace(v) != "" {
		cfg.ToolBins.AIComputerUseBin = strings.TrimSpace(v)
	}
	if v, ok := env.LookupEnv("DOCKER_BIN"); ok && strings.TrimSpace(v) != "" {
		cfg.ToolBins.DockerBin = strings.TrimSpace(v)
	}
}

func applyOverrides(overrides CLIOverrides, cfg *Config) {
	if overrides.OutputDir != nil && strings.TrimSpace(*overrides.OutputDir) != "" {
		cfg.OutputDir = strings.TrimSpace(*overrides.OutputDir)
	}
	if overrides.AIBrowserUseBin != nil && strings.TrimSpace(*overrides.AIBrowserUseBin) != "" {
		cfg.ToolBins.AIBrowserUseBin = strings.TrimSpace(*overrides.AIBrowserUseBin)
	}
	if overrides.AIComputerUseBin != nil && strings.TrimSpace(*overrides.AIComputerUseBin) != "" {
		cfg.ToolBins.AIComputerUseBin = strings.TrimSpace(*overrides.AIComputerUseBin)
	}
	if overrides.DockerBin != nil && strings.TrimSpace(*overrides.DockerBin) != "" {
		cfg.ToolBins.DockerBin = strings.TrimSpace(*overrides.DockerBin)
	}
}

func RunDir(outputDir, runID string) string {
	return filepath.Join(outputDir, runID)
}
