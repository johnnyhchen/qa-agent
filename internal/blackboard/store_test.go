package blackboard

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"qa-agent/internal/model"
)

func TestStoreMigrationsAndCRUD(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	runID := "run_1"
	err = store.CreateValidationRun(ctx, ValidationRun{
		ID:              runID,
		RetentionPolicy: RetentionKeepAll,
		Budgets:         map[string]int{"max_steps": 100},
	})
	if err != nil {
		t.Fatalf("CreateValidationRun() error = %v", err)
	}

	run, err := store.GetValidationRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetValidationRun() error = %v", err)
	}
	if run.ID != runID {
		t.Fatalf("run.ID = %q, want %q", run.ID, runID)
	}

	err = store.UpsertFeatureSpec(ctx, model.FeatureSpec{
		SchemaVersion: model.CurrentSchemaVersion,
		RunID:         runID,
		Description:   "Feature description",
		AcceptanceCriteria: []model.AcceptanceCriterion{
			{ID: "ac_1", Text: "Criterion"},
		},
		Surfaces: []model.Surface{model.SurfaceWeb},
	})
	if err != nil {
		t.Fatalf("UpsertFeatureSpec() error = %v", err)
	}

	err = store.CreateTask(ctx, model.Task{
		SchemaVersion: model.CurrentSchemaVersion,
		TaskID:        "task_1",
		RunID:         runID,
		Surface:       model.SurfaceWeb,
		Kind:          model.TaskKindProof,
		Priority:      model.PriorityP0,
		Status:        model.TaskStatusQueued,
		DedupeKey:     "web:proof:1",
		MaxAttempts:   2,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	tasks, err := store.TaskList(ctx, TaskFilter{
		RunID:  runID,
		Status: []model.TaskStatus{model.TaskStatusQueued},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("TaskList() error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(tasks))
	}

	now := time.Now().UTC()
	err = store.CreateRun(ctx, model.Run{
		SchemaVersion: model.CurrentSchemaVersion,
		RunID:         runID,
		TaskID:        "task_1",
		Outcome:       model.RunOutcomePass,
		Summary:       "ok",
		StartedAt:     now,
		FinishedAt:    now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}

	runRows, err := store.RunList(ctx, RunFilter{RunID: runID, Limit: 10})
	if err != nil {
		t.Fatalf("RunList() error = %v", err)
	}
	if len(runRows) != 1 {
		t.Fatalf("len(runRows) = %d, want 1", len(runRows))
	}

	err = store.CreateEvidence(ctx, model.Evidence{
		SchemaVersion: model.CurrentSchemaVersion,
		EvidenceID:    "ev_1",
		RunID:         runID,
		Kind:          model.EvidenceKindLog,
		Path:          filepath.Join(store.ArtifactDir(runID), "log.txt"),
		Bytes:         2,
		CreatedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateEvidence() error = %v", err)
	}

	evidenceRows, err := store.EvidenceList(ctx, EvidenceFilter{RunID: runID, Limit: 10})
	if err != nil {
		t.Fatalf("EvidenceList() error = %v", err)
	}
	if len(evidenceRows) != 1 {
		t.Fatalf("len(evidenceRows) = %d, want 1", len(evidenceRows))
	}

	err = store.UpsertVerdict(ctx, model.Verdict{
		SchemaVersion: model.CurrentSchemaVersion,
		VerdictID:     "verdict_1",
		RunID:         runID,
		Status:        model.VerdictPass,
		Reasons:       []string{"ok"},
		Coverage: map[string][]string{
			"ac_1": {"ev_1"},
		},
	})
	if err != nil {
		t.Fatalf("UpsertVerdict() error = %v", err)
	}
}

func TestStoreConcurrentEvidenceWritesDifferentRuns(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	runIDs := []string{"run_a", "run_b"}
	for _, runID := range runIDs {
		if err := store.CreateValidationRun(ctx, ValidationRun{
			ID:              runID,
			RetentionPolicy: RetentionKeepAll,
		}); err != nil {
			t.Fatalf("CreateValidationRun(%s) error = %v", runID, err)
		}
	}

	var wg sync.WaitGroup
	writeEvidence := func(runID string) {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			err := store.CreateEvidence(ctx, model.Evidence{
				SchemaVersion: model.CurrentSchemaVersion,
				EvidenceID:    fmt.Sprintf("%s_ev_%d", runID, i),
				RunID:         runID,
				Kind:          model.EvidenceKindLog,
				Path:          filepath.Join(store.ArtifactDir(runID), fmt.Sprintf("log-%d.txt", i)),
				Bytes:         int64(i),
				CreatedAt:     time.Now().UTC(),
			})
			if err != nil {
				t.Errorf("CreateEvidence(%s,%d) error = %v", runID, i, err)
				return
			}
		}
	}

	wg.Add(2)
	go writeEvidence("run_a")
	go writeEvidence("run_b")
	wg.Wait()

	for _, runID := range runIDs {
		items, err := store.EvidenceList(ctx, EvidenceFilter{RunID: runID, Limit: 100})
		if err != nil {
			t.Fatalf("EvidenceList(%s) error = %v", runID, err)
		}
		if len(items) != 20 {
			t.Fatalf("len(EvidenceList(%s)) = %d, want 20", runID, len(items))
		}
	}
}
