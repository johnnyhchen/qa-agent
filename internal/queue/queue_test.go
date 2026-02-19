package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
)

func newQueueForTest(t *testing.T, maxQueued int) (*Manager, string) {
	t.Helper()
	store, err := blackboard.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	runID := "run_1"
	if err := store.CreateValidationRun(context.Background(), blackboard.ValidationRun{
		ID:              runID,
		RetentionPolicy: blackboard.RetentionKeepAll,
	}); err != nil {
		t.Fatalf("CreateValidationRun() error = %v", err)
	}

	return NewManager(store, maxQueued), runID
}

func makeTask(runID, taskID string, priority model.Priority, dedupeKey string) model.Task {
	return model.Task{
		SchemaVersion: model.CurrentSchemaVersion,
		TaskID:        taskID,
		RunID:         runID,
		Surface:       model.SurfaceWeb,
		Kind:          model.TaskKindProof,
		Priority:      priority,
		Status:        model.TaskStatusQueued,
		DedupeKey:     dedupeKey,
		MaxAttempts:   2,
		CreatedBy:     "planner",
	}
}

func TestEnqueueTask_Dedupe(t *testing.T) {
	ctx := context.Background()
	manager, runID := newQueueForTest(t, 100)

	task := makeTask(runID, "task_1", model.PriorityP1, "dedupe:web:1")
	if err := manager.EnqueueTask(ctx, task); err != nil {
		t.Fatalf("EnqueueTask() first error = %v", err)
	}
	if err := manager.EnqueueTask(ctx, task); !errors.Is(err, ErrTaskExists) {
		t.Fatalf("EnqueueTask() second error = %v, want ErrTaskExists", err)
	}
}

func TestClaimTask_AtomicUnderContention(t *testing.T) {
	ctx := context.Background()
	manager, runID := newQueueForTest(t, 100)

	if err := manager.EnqueueTask(ctx, makeTask(runID, "task_1", model.PriorityP0, "dedupe:web:atomic")); err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			_, err := manager.ClaimTask(ctx, runID, fmt.Sprintf("worker-%d", workerID), time.Minute)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successes++
				return
			}
			if !errors.Is(err, ErrNoTaskReady) {
				t.Errorf("ClaimTask() error = %v", err)
			}
		}(i)
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("successful claims = %d, want 1", successes)
	}
}

func TestClaimTask_PriorityOrderingStable(t *testing.T) {
	ctx := context.Background()
	manager, runID := newQueueForTest(t, 100)

	enqueue := []model.Task{
		makeTask(runID, "task_p2", model.PriorityP2, "d2"),
		makeTask(runID, "task_p0", model.PriorityP0, "d0"),
		makeTask(runID, "task_p1", model.PriorityP1, "d1"),
	}
	for _, task := range enqueue {
		if err := manager.EnqueueTask(ctx, task); err != nil {
			t.Fatalf("EnqueueTask(%s) error = %v", task.TaskID, err)
		}
	}

	first, err := manager.ClaimTask(ctx, runID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("ClaimTask() first error = %v", err)
	}
	if first.TaskID != "task_p0" {
		t.Fatalf("first claim = %s, want task_p0", first.TaskID)
	}
	if err := manager.CompleteTask(ctx, runID, first.TaskID, model.TaskStatusPassed); err != nil {
		t.Fatalf("CompleteTask() first error = %v", err)
	}

	second, err := manager.ClaimTask(ctx, runID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("ClaimTask() second error = %v", err)
	}
	if second.TaskID != "task_p1" {
		t.Fatalf("second claim = %s, want task_p1", second.TaskID)
	}
	if err := manager.CompleteTask(ctx, runID, second.TaskID, model.TaskStatusPassed); err != nil {
		t.Fatalf("CompleteTask() second error = %v", err)
	}

	third, err := manager.ClaimTask(ctx, runID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("ClaimTask() third error = %v", err)
	}
	if third.TaskID != "task_p2" {
		t.Fatalf("third claim = %s, want task_p2", third.TaskID)
	}
}

func TestEnqueueTask_SaturationDropsLowestPriorityFirst(t *testing.T) {
	ctx := context.Background()
	manager, runID := newQueueForTest(t, 2)

	if err := manager.EnqueueTask(ctx, makeTask(runID, "task_p3", model.PriorityP3, "d3")); err != nil {
		t.Fatalf("EnqueueTask() p3 error = %v", err)
	}
	if err := manager.EnqueueTask(ctx, makeTask(runID, "task_p2", model.PriorityP2, "d2")); err != nil {
		t.Fatalf("EnqueueTask() p2 error = %v", err)
	}
	if err := manager.EnqueueTask(ctx, makeTask(runID, "task_p1", model.PriorityP1, "d1")); err != nil {
		t.Fatalf("EnqueueTask() p1 error = %v", err)
	}

	first, err := manager.ClaimTask(ctx, runID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("ClaimTask() first error = %v", err)
	}
	if first.Priority != model.PriorityP1 {
		t.Fatalf("first priority = %s, want P1", first.Priority)
	}
}

func TestRequeueTask_RequeuesClaimedTask(t *testing.T) {
	ctx := context.Background()
	manager, runID := newQueueForTest(t, 100)

	if err := manager.EnqueueTask(ctx, makeTask(runID, "task_1", model.PriorityP1, "dedupe:requeue")); err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}

	claimed, err := manager.ClaimTask(ctx, runID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("ClaimTask() error = %v", err)
	}
	if claimed.AttemptCount != 1 {
		t.Fatalf("claimed.AttemptCount = %d, want 1", claimed.AttemptCount)
	}

	if err := manager.RequeueTask(ctx, runID, claimed.TaskID); err != nil {
		t.Fatalf("RequeueTask() error = %v", err)
	}

	reclaimed, err := manager.ClaimTask(ctx, runID, "worker-2", time.Minute)
	if err != nil {
		t.Fatalf("ClaimTask() after requeue error = %v", err)
	}
	if reclaimed.TaskID != claimed.TaskID {
		t.Fatalf("reclaimed.TaskID = %s, want %s", reclaimed.TaskID, claimed.TaskID)
	}
	if reclaimed.AttemptCount != 2 {
		t.Fatalf("reclaimed.AttemptCount = %d, want 2", reclaimed.AttemptCount)
	}
}

func TestEnqueueTask_ConcurrentNoBusyErrors(t *testing.T) {
	ctx := context.Background()
	manager, runID := newQueueForTest(t, 200)

	var wg sync.WaitGroup
	var mu sync.Mutex
	busyErrors := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			task := makeTask(runID, fmt.Sprintf("task_%d", i), model.PriorityP1, fmt.Sprintf("dedupe_%d", i))
			if err := manager.EnqueueTask(ctx, task); err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "busy") || strings.Contains(strings.ToLower(err.Error()), "locked") {
					mu.Lock()
					busyErrors++
					mu.Unlock()
					return
				}
				t.Errorf("EnqueueTask(%d) unexpected error = %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if busyErrors != 0 {
		t.Fatalf("busyErrors = %d, want 0", busyErrors)
	}
}
