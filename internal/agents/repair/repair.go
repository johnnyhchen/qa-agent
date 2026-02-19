package repair

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
	Enabled     bool
	ApplyRepair bool
}

type Proposal struct {
	Diff      string `json:"diff"`
	Rationale string `json:"rationale"`
}

type Client interface {
	GeneratePatch(ctx context.Context, diagnostics string) (Proposal, error)
}

type Agent struct {
	store  *blackboard.Store
	client Client
	config Config
}

func New(store *blackboard.Store, client Client, config Config) *Agent {
	return &Agent{
		store:  store,
		client: client,
		config: config,
	}
}

func (a *Agent) MaybeRun(ctx context.Context, runID string, blocked bool, diagnostics string) (*Proposal, error) {
	if !a.config.Enabled || !blocked {
		return nil, nil
	}
	if diagnostics == "" {
		return nil, nil
	}
	if a.client == nil {
		return nil, errors.New("repair client is required when enabled")
	}

	proposal, err := a.client.GeneratePatch(ctx, diagnostics)
	if err != nil {
		return nil, err
	}
	artifactDir := filepath.Join(a.store.ArtifactDir(runID), "repair")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return nil, err
	}
	proposalPath := filepath.Join(artifactDir, fmt.Sprintf("proposal-%d.json", time.Now().UTC().UnixNano()))
	raw, err := json.MarshalIndent(map[string]any{
		"dry_run":   !a.config.ApplyRepair,
		"proposal":  proposal,
		"generated": time.Now().UTC().Format(time.RFC3339Nano),
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(proposalPath, raw, 0o644); err != nil {
		return nil, err
	}
	if err := a.store.CreateEvidence(ctx, model.Evidence{
		SchemaVersion: model.CurrentSchemaVersion,
		EvidenceID:    fmt.Sprintf("repair_%d", time.Now().UTC().UnixNano()),
		RunID:         runID,
		Kind:          model.EvidenceKindTrace,
		Path:          proposalPath,
		MIME:          "application/json",
		Bytes:         int64(len(raw)),
		SummaryFields: map[string]string{
			"component": "repair_agent",
			"dry_run":   fmt.Sprintf("%t", !a.config.ApplyRepair),
		},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		return nil, err
	}

	if a.config.ApplyRepair {
		return nil, errors.New("automatic patch apply is not implemented; use proposal artifact manually")
	}
	return &proposal, nil
}
