package blackboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"qa-agent/internal/model"
)

const (
	RetentionKeepAll          = "keep_all"
	RetentionKeepKeyArtifacts = "keep_key_artifacts"
	RetentionKeepSummaryOnly  = "keep_summary_only"
)

type Store struct {
	baseDir string
	mu      sync.Mutex
	dbs     map[string]*sql.DB
}

type ValidationRun struct {
	ID              string            `json:"id"`
	CreatedAt       time.Time         `json:"created_at"`
	Status          string            `json:"status"`
	RetentionPolicy string            `json:"retention_policy"`
	Budgets         map[string]int    `json:"budgets,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type TaskFilter struct {
	RunID   string
	Status  []model.TaskStatus
	Surface []model.Surface
	Kind    []model.TaskKind
	Limit   int
}

type RunFilter struct {
	RunID   string
	TaskID  string
	Outcome []model.RunOutcome
	Limit   int
}

type EvidenceFilter struct {
	RunID string
	Kind  []model.EvidenceKind
	Limit int
}

func NewStore(baseDir string) (*Store, error) {
	if strings.TrimSpace(baseDir) == "" {
		return nil, errors.New("baseDir is required")
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, err
	}
	return &Store{
		baseDir: baseDir,
		dbs:     make(map[string]*sql.DB),
	}, nil
}

func (s *Store) BaseDir() string {
	return s.baseDir
}

func (s *Store) RunDir(runID string) string {
	return filepath.Join(s.baseDir, "runs", runID)
}

func (s *Store) ArtifactDir(runID string) string {
	return filepath.Join(s.RunDir(runID), "artifacts")
}

func (s *Store) DBPath(runID string) string {
	return filepath.Join(s.RunDir(runID), "db.sqlite")
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for _, db := range s.dbs {
		if err := db.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	s.dbs = make(map[string]*sql.DB)
	return errors.Join(errs...)
}

func (s *Store) WithRunTx(ctx context.Context, runID string, fn func(tx *sql.Tx) error) error {
	db, err := s.dbForRun(runID)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) CreateValidationRun(ctx context.Context, run ValidationRun) error {
	if strings.TrimSpace(run.ID) == "" {
		return errors.New("run id is required")
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now().UTC()
	}
	if strings.TrimSpace(run.Status) == "" {
		run.Status = "created"
	}
	if !isValidRetention(run.RetentionPolicy) {
		run.RetentionPolicy = RetentionKeepAll
	}

	if err := os.MkdirAll(s.ArtifactDir(run.ID), 0o755); err != nil {
		return err
	}

	db, err := s.dbForRun(run.ID)
	if err != nil {
		return err
	}

	budgetsRaw, err := json.Marshal(run.Budgets)
	if err != nil {
		return err
	}
	metadataRaw, err := json.Marshal(run.Metadata)
	if err != nil {
		return err
	}

	_, err = db.ExecContext(
		ctx,
		`INSERT INTO validation_runs (id, created_at, status, retention_policy, budgets_json, metadata_json)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		run.ID,
		run.CreatedAt.UTC().Format(time.RFC3339Nano),
		run.Status,
		run.RetentionPolicy,
		string(budgetsRaw),
		string(metadataRaw),
	)
	return err
}

func (s *Store) GetValidationRun(ctx context.Context, runID string) (ValidationRun, error) {
	db, err := s.dbForRun(runID)
	if err != nil {
		return ValidationRun{}, err
	}

	var out ValidationRun
	var createdAt string
	var budgetsRaw string
	var metadataRaw string
	err = db.QueryRowContext(
		ctx,
		`SELECT id, created_at, status, retention_policy, budgets_json, metadata_json
		 FROM validation_runs WHERE id = ?`,
		runID,
	).Scan(&out.ID, &createdAt, &out.Status, &out.RetentionPolicy, &budgetsRaw, &metadataRaw)
	if err != nil {
		return ValidationRun{}, err
	}
	out.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return ValidationRun{}, err
	}
	if budgetsRaw != "" {
		if err := json.Unmarshal([]byte(budgetsRaw), &out.Budgets); err != nil {
			return ValidationRun{}, err
		}
	}
	if metadataRaw != "" {
		if err := json.Unmarshal([]byte(metadataRaw), &out.Metadata); err != nil {
			return ValidationRun{}, err
		}
	}
	return out, nil
}

func (s *Store) UpsertFeatureSpec(ctx context.Context, spec model.FeatureSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	db, err := s.dbForRun(spec.RunID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(
		ctx,
		`INSERT INTO feature_specs (id, run_id, payload_json, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET payload_json=excluded.payload_json`,
		featureSpecID(spec.RunID),
		spec.RunID,
		string(payload),
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) CreateTask(ctx context.Context, task model.Task) error {
	if err := task.Validate(); err != nil {
		return err
	}
	db, err := s.dbForRun(task.RunID)
	if err != nil {
		return err
	}
	criteriaRaw, err := json.Marshal(task.AcceptanceCriteriaIDs)
	if err != nil {
		return err
	}
	payloadRaw, err := json.Marshal(task.Payload)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(
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
		task.ClaimedBy,
		"",
		"",
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) TaskList(ctx context.Context, filter TaskFilter) ([]model.Task, error) {
	if strings.TrimSpace(filter.RunID) == "" {
		return nil, errors.New("TaskList filter requires run_id")
	}
	db, err := s.dbForRun(filter.RunID)
	if err != nil {
		return nil, err
	}
	query := `SELECT id, run_id, surface, kind, priority, status, dedupe_key, max_attempts, attempt_count, criteria_ids_json, payload_json, created_by, claimed_by
	          FROM tasks`
	var where []string
	var args []any
	if len(filter.Status) > 0 {
		where = append(where, "status IN ("+placeholders(len(filter.Status))+")")
		for _, v := range filter.Status {
			args = append(args, string(v))
		}
	}
	if len(filter.Surface) > 0 {
		where = append(where, "surface IN ("+placeholders(len(filter.Surface))+")")
		for _, v := range filter.Surface {
			args = append(args, string(v))
		}
	}
	if len(filter.Kind) > 0 {
		where = append(where, "kind IN ("+placeholders(len(filter.Kind))+")")
		for _, v := range filter.Kind {
			args = append(args, string(v))
		}
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY CASE priority WHEN 'P0' THEN 0 WHEN 'P1' THEN 1 WHEN 'P2' THEN 2 ELSE 3 END, created_at ASC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []model.Task
	for rows.Next() {
		var task model.Task
		var criteriaRaw string
		var payloadRaw string
		var surface string
		var kind string
		var priority string
		var status string
		if err := rows.Scan(
			&task.TaskID,
			&task.RunID,
			&surface,
			&kind,
			&priority,
			&status,
			&task.DedupeKey,
			&task.MaxAttempts,
			&task.AttemptCount,
			&criteriaRaw,
			&payloadRaw,
			&task.CreatedBy,
			&task.ClaimedBy,
		); err != nil {
			return nil, err
		}
		task.SchemaVersion = model.CurrentSchemaVersion
		task.Surface = model.Surface(surface)
		task.Kind = model.TaskKind(kind)
		task.Priority = model.Priority(priority)
		task.Status = model.TaskStatus(status)
		if criteriaRaw != "" {
			if err := json.Unmarshal([]byte(criteriaRaw), &task.AcceptanceCriteriaIDs); err != nil {
				return nil, err
			}
		}
		if payloadRaw != "" && payloadRaw != "{}" {
			if err := json.Unmarshal([]byte(payloadRaw), &task.Payload); err != nil {
				return nil, err
			}
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *Store) CreateRun(ctx context.Context, run model.Run) error {
	if err := run.Validate(); err != nil {
		return err
	}
	db, err := s.dbForRun(run.RunID)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(
		ctx,
		`INSERT INTO runs (id, run_id, task_id, sandbox_id, outcome, summary, trace_ref, started_at, finished_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runRecordID(run.TaskID, run.StartedAt),
		run.RunID,
		run.TaskID,
		run.SandboxID,
		string(run.Outcome),
		run.Summary,
		run.TraceRef,
		run.StartedAt.UTC().Format(time.RFC3339Nano),
		run.FinishedAt.UTC().Format(time.RFC3339Nano),
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) RunList(ctx context.Context, filter RunFilter) ([]model.Run, error) {
	if strings.TrimSpace(filter.RunID) == "" {
		return nil, errors.New("RunList filter requires run_id")
	}
	db, err := s.dbForRun(filter.RunID)
	if err != nil {
		return nil, err
	}
	query := `SELECT run_id, task_id, sandbox_id, outcome, summary, trace_ref, started_at, finished_at FROM runs`
	var where []string
	var args []any
	if filter.TaskID != "" {
		where = append(where, "task_id = ?")
		args = append(args, filter.TaskID)
	}
	if len(filter.Outcome) > 0 {
		where = append(where, "outcome IN ("+placeholders(len(filter.Outcome))+")")
		for _, outcome := range filter.Outcome {
			args = append(args, string(outcome))
		}
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY started_at ASC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.Run
	for rows.Next() {
		var startedAt string
		var finishedAt string
		var run model.Run
		var outcome string
		if err := rows.Scan(
			&run.RunID,
			&run.TaskID,
			&run.SandboxID,
			&outcome,
			&run.Summary,
			&run.TraceRef,
			&startedAt,
			&finishedAt,
		); err != nil {
			return nil, err
		}
		var parseErr error
		run.StartedAt, parseErr = time.Parse(time.RFC3339Nano, startedAt)
		if parseErr != nil {
			return nil, parseErr
		}
		run.FinishedAt, parseErr = time.Parse(time.RFC3339Nano, finishedAt)
		if parseErr != nil {
			return nil, parseErr
		}
		run.Outcome = model.RunOutcome(outcome)
		run.SchemaVersion = model.CurrentSchemaVersion
		result = append(result, run)
	}
	return result, rows.Err()
}

func (s *Store) CreateEvidence(ctx context.Context, evidence model.Evidence) error {
	if err := evidence.Validate(); err != nil {
		return err
	}
	db, err := s.dbForRun(evidence.RunID)
	if err != nil {
		return err
	}
	summaryRaw, err := json.Marshal(evidence.SummaryFields)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(
		ctx,
		`INSERT INTO evidence (id, run_id, kind, path, mime, bytes, summary_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		evidence.EvidenceID,
		evidence.RunID,
		string(evidence.Kind),
		evidence.Path,
		evidence.MIME,
		evidence.Bytes,
		string(summaryRaw),
		evidence.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) EvidenceList(ctx context.Context, filter EvidenceFilter) ([]model.Evidence, error) {
	if strings.TrimSpace(filter.RunID) == "" {
		return nil, errors.New("EvidenceList filter requires run_id")
	}
	db, err := s.dbForRun(filter.RunID)
	if err != nil {
		return nil, err
	}
	query := `SELECT id, run_id, kind, path, mime, bytes, summary_json, created_at FROM evidence`
	var where []string
	var args []any
	if len(filter.Kind) > 0 {
		where = append(where, "kind IN ("+placeholders(len(filter.Kind))+")")
		for _, kind := range filter.Kind {
			args = append(args, string(kind))
		}
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at ASC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.Evidence
	for rows.Next() {
		var item model.Evidence
		var kind string
		var createdAt string
		var summaryRaw string
		if err := rows.Scan(
			&item.EvidenceID,
			&item.RunID,
			&kind,
			&item.Path,
			&item.MIME,
			&item.Bytes,
			&summaryRaw,
			&createdAt,
		); err != nil {
			return nil, err
		}
		item.SchemaVersion = model.CurrentSchemaVersion
		item.Kind = model.EvidenceKind(kind)
		var parseErr error
		item.CreatedAt, parseErr = time.Parse(time.RFC3339Nano, createdAt)
		if parseErr != nil {
			return nil, parseErr
		}
		if summaryRaw != "" {
			if err := json.Unmarshal([]byte(summaryRaw), &item.SummaryFields); err != nil {
				return nil, err
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpsertVerdict(ctx context.Context, verdict model.Verdict) error {
	if err := verdict.Validate(); err != nil {
		return err
	}
	db, err := s.dbForRun(verdict.RunID)
	if err != nil {
		return err
	}
	reasonsRaw, err := json.Marshal(verdict.Reasons)
	if err != nil {
		return err
	}
	coverageRaw, err := json.Marshal(verdict.Coverage)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(
		ctx,
		`INSERT INTO verdicts (id, run_id, status, reasons_json, coverage_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET status=excluded.status, reasons_json=excluded.reasons_json, coverage_json=excluded.coverage_json`,
		verdict.VerdictID,
		verdict.RunID,
		string(verdict.Status),
		string(reasonsRaw),
		string(coverageRaw),
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) dbForRun(runID string) (*sql.DB, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, errors.New("run id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if db, ok := s.dbs[runID]; ok {
		return db, nil
	}

	if err := os.MkdirAll(s.RunDir(runID), 0o755); err != nil {
		return nil, err
	}

	dsn := s.DBPath(runID)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Each run has its own SQLite file; a single connection avoids intra-process writer lock storms.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	s.dbs[runID] = db
	return db, nil
}

func runMigrations(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS validation_runs (
			id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL,
			status TEXT NOT NULL,
			retention_policy TEXT NOT NULL,
			budgets_json TEXT NOT NULL,
			metadata_json TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS feature_specs (
			id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			surface TEXT NOT NULL,
			kind TEXT NOT NULL,
			priority TEXT NOT NULL,
			status TEXT NOT NULL,
			dedupe_key TEXT NOT NULL,
			max_attempts INTEGER NOT NULL,
			attempt_count INTEGER NOT NULL,
			criteria_ids_json TEXT NOT NULL,
			payload_json TEXT NOT NULL DEFAULT '{}',
			created_by TEXT NOT NULL,
			claimed_by TEXT NOT NULL,
			claimed_at TEXT NOT NULL,
			lease_until TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_run_dedupe ON tasks(run_id, dedupe_key);`,
		`CREATE TABLE IF NOT EXISTS runs (
			id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			task_id TEXT NOT NULL,
			sandbox_id TEXT NOT NULL,
			outcome TEXT NOT NULL,
			summary TEXT NOT NULL,
			trace_ref TEXT NOT NULL,
			started_at TEXT NOT NULL,
			finished_at TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS evidence (
			id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			path TEXT NOT NULL,
			mime TEXT NOT NULL,
			bytes INTEGER NOT NULL,
			summary_json TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS verdicts (
			id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			status TEXT NOT NULL,
			reasons_json TEXT NOT NULL,
			coverage_json TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func featureSpecID(runID string) string {
	return "feature_spec_" + runID
}

func runRecordID(taskID string, startedAt time.Time) string {
	return fmt.Sprintf("run_%s_%d", taskID, startedAt.UTC().UnixNano())
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	items := make([]string, n)
	for i := range items {
		items[i] = "?"
	}
	return strings.Join(items, ",")
}

func isValidRetention(policy string) bool {
	switch policy {
	case RetentionKeepAll, RetentionKeepKeyArtifacts, RetentionKeepSummaryOnly:
		return true
	default:
		return false
	}
}
