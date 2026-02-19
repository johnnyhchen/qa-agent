package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
)

var (
	ErrTaskExists     = errors.New("task with dedupe_key already exists")
	ErrQueueSaturated = errors.New("queue saturated")
	ErrNoTaskReady    = errors.New("no task ready")
)

type Manager struct {
	store     *blackboard.Store
	maxQueued int
}

func NewManager(store *blackboard.Store, maxQueued int) *Manager {
	if maxQueued < 1 {
		maxQueued = 1000
	}
	return &Manager{
		store:     store,
		maxQueued: maxQueued,
	}
}

func (m *Manager) EnqueueTask(ctx context.Context, task model.Task) error {
	if err := task.Validate(); err != nil {
		return err
	}

	return m.store.WithRunTx(ctx, task.RunID, func(tx *sql.Tx) error {
		var queuedCount int
		if err := tx.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM tasks WHERE status = ?`,
			string(model.TaskStatusQueued),
		).Scan(&queuedCount); err != nil {
			return err
		}

		if queuedCount >= m.maxQueued {
			if !canDropFor(task.Priority) {
				if err := evictLowestPriorityTask(ctx, tx); err != nil {
					return err
				}
			} else {
				return ErrQueueSaturated
			}
		}

		criteriaRaw, err := json.Marshal(task.AcceptanceCriteriaIDs)
		if err != nil {
			return err
		}
		payloadRaw, err := json.Marshal(task.Payload)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(
			ctx,
			`INSERT INTO tasks (
				id, run_id, surface, kind, priority, status, dedupe_key, max_attempts, attempt_count, criteria_ids_json, payload_json, created_by, claimed_by, claimed_at, lease_until, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			task.TaskID,
			task.RunID,
			string(task.Surface),
			string(task.Kind),
			string(task.Priority),
			string(task.Status),
			task.DedupeKey,
			task.MaxAttempts,
			task.AttemptCount,
			string(criteriaRaw),
			string(payloadRaw),
			task.CreatedBy,
			"",
			"",
			"",
			time.Now().UTC().Format(time.RFC3339Nano),
		)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return ErrTaskExists
			}
			return err
		}
		return nil
	})
}

func (m *Manager) ClaimTask(ctx context.Context, runID, claimedBy string, leaseDuration time.Duration) (model.Task, error) {
	if strings.TrimSpace(runID) == "" {
		return model.Task{}, errors.New("runID is required")
	}
	if strings.TrimSpace(claimedBy) == "" {
		return model.Task{}, errors.New("claimedBy is required")
	}
	if leaseDuration <= 0 {
		leaseDuration = 5 * time.Minute
	}

	var claimed model.Task
	err := m.store.WithRunTx(ctx, runID, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(
			ctx,
			`SELECT id, run_id, surface, kind, priority, status, dedupe_key, max_attempts, attempt_count, criteria_ids_json, payload_json, created_by
			 FROM tasks
			 WHERE status = ?
			 ORDER BY CASE priority WHEN 'P0' THEN 0 WHEN 'P1' THEN 1 WHEN 'P2' THEN 2 ELSE 3 END, created_at ASC
			 LIMIT 1`,
			string(model.TaskStatusQueued),
		)

		var surface string
		var kind string
		var priority string
		var status string
		var criteriaRaw string
		var payloadRaw string
		err := row.Scan(
			&claimed.TaskID,
			&claimed.RunID,
			&surface,
			&kind,
			&priority,
			&status,
			&claimed.DedupeKey,
			&claimed.MaxAttempts,
			&claimed.AttemptCount,
			&criteriaRaw,
			&payloadRaw,
			&claimed.CreatedBy,
		)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNoTaskReady
			}
			return err
		}

		claimed.Surface = model.Surface(surface)
		claimed.Kind = model.TaskKind(kind)
		claimed.Priority = model.Priority(priority)
		claimed.Status = model.TaskStatus(status)
		claimed.SchemaVersion = model.CurrentSchemaVersion
		if criteriaRaw != "" {
			if err := json.Unmarshal([]byte(criteriaRaw), &claimed.AcceptanceCriteriaIDs); err != nil {
				return err
			}
		}
		if payloadRaw != "" && payloadRaw != "{}" && payloadRaw != "null" {
			if err := json.Unmarshal([]byte(payloadRaw), &claimed.Payload); err != nil {
				return err
			}
		}

		now := time.Now().UTC()
		result, err := tx.ExecContext(
			ctx,
			`UPDATE tasks
			 SET status = ?, claimed_by = ?, claimed_at = ?, lease_until = ?, attempt_count = attempt_count + 1
			 WHERE id = ? AND status = ?`,
			string(model.TaskStatusClaimed),
			claimedBy,
			now.Format(time.RFC3339Nano),
			now.Add(leaseDuration).Format(time.RFC3339Nano),
			claimed.TaskID,
			string(model.TaskStatusQueued),
		)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrNoTaskReady
		}
		claimed.Status = model.TaskStatusClaimed
		claimed.ClaimedBy = claimedBy
		claimed.AttemptCount++
		return nil
	})
	if err != nil {
		return model.Task{}, err
	}
	return claimed, nil
}

func (m *Manager) CompleteTask(ctx context.Context, runID, taskID string, status model.TaskStatus) error {
	if strings.TrimSpace(runID) == "" {
		return errors.New("runID is required")
	}
	if strings.TrimSpace(taskID) == "" {
		return errors.New("taskID is required")
	}
	if status != model.TaskStatusPassed && status != model.TaskStatusFailed && status != model.TaskStatusBlocked && status != model.TaskStatusErrored && status != model.TaskStatusCancelled {
		return fmt.Errorf("invalid completion status: %s", status)
	}
	return m.store.WithRunTx(ctx, runID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(
			ctx,
			`UPDATE tasks
			 SET status = ?, lease_until = ''
			 WHERE id = ? AND status = ?`,
			string(status),
			taskID,
			string(model.TaskStatusClaimed),
		)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return errors.New("task was not claimed or does not exist")
		}
		return nil
	})
}

func (m *Manager) RequeueTask(ctx context.Context, runID, taskID string) error {
	if strings.TrimSpace(runID) == "" {
		return errors.New("runID is required")
	}
	if strings.TrimSpace(taskID) == "" {
		return errors.New("taskID is required")
	}
	return m.store.WithRunTx(ctx, runID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(
			ctx,
			`UPDATE tasks
			 SET status = ?, claimed_by = '', claimed_at = '', lease_until = ''
			 WHERE id = ? AND status = ?`,
			string(model.TaskStatusQueued),
			taskID,
			string(model.TaskStatusClaimed),
		)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return errors.New("task was not claimed or does not exist")
		}
		return nil
	})
}

func (m *Manager) CountTasksCreatedBy(ctx context.Context, runID, createdBy string, since time.Time) (int, error) {
	var count int
	err := m.store.WithRunTx(ctx, runID, func(tx *sql.Tx) error {
		return tx.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM tasks WHERE created_by = ? AND created_at >= ?`,
			createdBy,
			since.UTC().Format(time.RFC3339Nano),
		).Scan(&count)
	})
	return count, err
}

func evictLowestPriorityTask(ctx context.Context, tx *sql.Tx) error {
	row := tx.QueryRowContext(
		ctx,
		`SELECT id FROM tasks
		 WHERE status = ?
		 ORDER BY CASE priority WHEN 'P3' THEN 0 WHEN 'P2' THEN 1 ELSE 2 END, created_at ASC
		 LIMIT 1`,
		string(model.TaskStatusQueued),
	)
	var taskID string
	if err := row.Scan(&taskID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrQueueSaturated
		}
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, taskID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrQueueSaturated
	}
	return nil
}

func canDropFor(priority model.Priority) bool {
	return priority == model.PriorityP2 || priority == model.PriorityP3
}
