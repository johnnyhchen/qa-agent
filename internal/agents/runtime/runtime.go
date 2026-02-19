package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
)

type Config struct {
	PlannerModel string
	JudgeModel   string
	Provider     string
	TokenCap     int
	TimeCap      time.Duration
	CostCapUSD   float64
}

type ToolCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type ToolResult struct {
	Output map[string]any `json:"output"`
}

type ToolHandler func(ctx context.Context, runID string, args map[string]any) (ToolResult, error)

type ToolRegistry struct {
	handlers map[string]ToolHandler
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{handlers: map[string]ToolHandler{}}
}

func (r *ToolRegistry) Register(name string, handler ToolHandler) {
	r.handlers[name] = handler
}

func (r *ToolRegistry) Invoke(ctx context.Context, runID string, call ToolCall) (ToolResult, error) {
	handler, ok := r.handlers[call.Name]
	if !ok {
		return ToolResult{}, fmt.Errorf("tool %q is not allowlisted", call.Name)
	}
	return handler(ctx, runID, call.Args)
}

type Client interface {
	Run(ctx context.Context, prompt string, calls []ToolResult) (string, Usage, error)
}

type Usage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

type Runtime struct {
	store    *blackboard.Store
	registry *ToolRegistry
	client   Client
	config   Config
}

type TurnRequest struct {
	RunID     string     `json:"run_id"`
	AgentName string     `json:"agent_name"`
	Prompt    string     `json:"prompt"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type TurnResult struct {
	Output  string       `json:"output"`
	Usage   Usage        `json:"usage"`
	ToolOut []ToolResult `json:"tool_out,omitempty"`
}

func New(store *blackboard.Store, registry *ToolRegistry, client Client, config Config) *Runtime {
	if registry == nil {
		registry = NewToolRegistry()
	}
	if client == nil {
		client = &EchoClient{}
	}
	if config.TokenCap <= 0 {
		config.TokenCap = 15000
	}
	if config.TimeCap <= 0 {
		config.TimeCap = 2 * time.Minute
	}
	return &Runtime{
		store:    store,
		registry: registry,
		client:   client,
		config:   config,
	}
}

func (r *Runtime) RunTurn(ctx context.Context, request TurnRequest) (TurnResult, error) {
	if request.RunID == "" {
		return TurnResult{}, errors.New("run_id is required")
	}
	if request.AgentName == "" {
		return TurnResult{}, errors.New("agent_name is required")
	}
	if request.Prompt == "" {
		return TurnResult{}, errors.New("prompt is required")
	}

	started := time.Now().UTC()
	toolResults := make([]ToolResult, 0, len(request.ToolCalls))
	for _, call := range request.ToolCalls {
		result, err := r.registry.Invoke(ctx, request.RunID, call)
		if err != nil {
			return TurnResult{}, err
		}
		toolResults = append(toolResults, result)
	}

	output, usage, err := r.client.Run(ctx, request.Prompt, toolResults)
	if err != nil {
		return TurnResult{}, err
	}
	if usage.InputTokens+usage.OutputTokens > r.config.TokenCap {
		return TurnResult{}, fmt.Errorf("token cap exceeded (%d > %d)", usage.InputTokens+usage.OutputTokens, r.config.TokenCap)
	}
	if usage.CostUSD > r.config.CostCapUSD && r.config.CostCapUSD > 0 {
		return TurnResult{}, fmt.Errorf("cost cap exceeded (%.4f > %.4f)", usage.CostUSD, r.config.CostCapUSD)
	}

	artifactDir := filepath.Join(r.store.ArtifactDir(request.RunID), "agents", request.AgentName)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return TurnResult{}, err
	}

	inputPath := filepath.Join(artifactDir, fmt.Sprintf("%d-input.json", started.UnixNano()))
	outputPath := filepath.Join(artifactDir, fmt.Sprintf("%d-output.json", started.UnixNano()))
	usagePath := filepath.Join(artifactDir, fmt.Sprintf("%d-usage.json", started.UnixNano()))

	inputRaw, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return TurnResult{}, err
	}
	outputRaw, err := json.MarshalIndent(map[string]any{
		"output":   output,
		"tool_out": toolResults,
	}, "", "  ")
	if err != nil {
		return TurnResult{}, err
	}
	usageRaw, err := json.MarshalIndent(usage, "", "  ")
	if err != nil {
		return TurnResult{}, err
	}

	if err := os.WriteFile(inputPath, inputRaw, 0o644); err != nil {
		return TurnResult{}, err
	}
	if err := os.WriteFile(outputPath, outputRaw, 0o644); err != nil {
		return TurnResult{}, err
	}
	if err := os.WriteFile(usagePath, usageRaw, 0o644); err != nil {
		return TurnResult{}, err
	}

	if err := r.registerEvidence(ctx, request.RunID, request.AgentName, inputPath, int64(len(inputRaw))); err != nil {
		return TurnResult{}, err
	}
	if err := r.registerEvidence(ctx, request.RunID, request.AgentName, outputPath, int64(len(outputRaw))); err != nil {
		return TurnResult{}, err
	}
	if err := r.registerEvidence(ctx, request.RunID, request.AgentName, usagePath, int64(len(usageRaw))); err != nil {
		return TurnResult{}, err
	}

	return TurnResult{
		Output:  output,
		Usage:   usage,
		ToolOut: toolResults,
	}, nil
}

func (r *Runtime) registerEvidence(ctx context.Context, runID, agentName, path string, size int64) error {
	return r.store.CreateEvidence(ctx, model.Evidence{
		SchemaVersion: model.CurrentSchemaVersion,
		EvidenceID:    fmt.Sprintf("agent_%s_%d", agentName, time.Now().UTC().UnixNano()),
		RunID:         runID,
		Kind:          model.EvidenceKindTrace,
		Path:          path,
		MIME:          "application/json",
		Bytes:         size,
		SummaryFields: map[string]string{
			"agent": agentName,
		},
		CreatedAt: time.Now().UTC(),
	})
}

type EchoClient struct{}

func (e *EchoClient) Run(_ context.Context, prompt string, calls []ToolResult) (string, Usage, error) {
	output := "echo: " + prompt
	return output, Usage{
		InputTokens:  len(prompt) / 4,
		OutputTokens: len(output) / 4,
		CostUSD:      float64(len(prompt)+len(output)) * 0.000001,
	}, nil
}
