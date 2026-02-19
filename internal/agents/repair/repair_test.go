package repair

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"qa-agent/internal/blackboard"
)

type fakeClient struct {
	callCount int
}

func (f *fakeClient) GeneratePatch(_ context.Context, diagnostics string) (Proposal, error) {
	f.callCount++
	return Proposal{
		Diff:      "--- a/file\n+++ b/file\n@@\n-old\n+new\n",
		Rationale: "Fix blocked setup: " + diagnostics,
	}, nil
}

func TestRepairAgentGateDisabled(t *testing.T) {
	store, err := blackboard.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	runID := "run_gate"
	if err := store.CreateValidationRun(context.Background(), blackboard.ValidationRun{
		ID:              runID,
		RetentionPolicy: blackboard.RetentionKeepAll,
	}); err != nil {
		t.Fatalf("CreateValidationRun() error = %v", err)
	}

	client := &fakeClient{}
	agent := New(store, client, Config{
		Enabled: false,
	})
	proposal, err := agent.MaybeRun(context.Background(), runID, true, "missing fixture")
	if err != nil {
		t.Fatalf("MaybeRun() error = %v", err)
	}
	if proposal != nil {
		t.Fatal("expected nil proposal when gate disabled")
	}
	if client.callCount != 0 {
		t.Fatalf("client.callCount = %d, want 0", client.callCount)
	}
}

func TestRepairAgentDryRunProposalArtifact(t *testing.T) {
	store, err := blackboard.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	runID := "run_dry"
	if err := store.CreateValidationRun(context.Background(), blackboard.ValidationRun{
		ID:              runID,
		RetentionPolicy: blackboard.RetentionKeepAll,
	}); err != nil {
		t.Fatalf("CreateValidationRun() error = %v", err)
	}

	client := &fakeClient{}
	agent := New(store, client, Config{
		Enabled:     true,
		ApplyRepair: false,
	})
	proposal, err := agent.MaybeRun(context.Background(), runID, true, "build fails")
	if err != nil {
		t.Fatalf("MaybeRun() error = %v", err)
	}
	if proposal == nil {
		t.Fatal("expected proposal for dry run")
	}
	repairDir := filepath.Join(store.ArtifactDir(runID), "repair")
	entries, err := os.ReadDir(repairDir)
	if err != nil {
		t.Fatalf("ReadDir(repairDir) error = %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected repair proposal artifact")
	}
	if client.callCount != 1 {
		t.Fatalf("client.callCount = %d, want 1", client.callCount)
	}
}
