package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const CurrentSchemaVersion = "v1"

type Surface string

const (
	SurfaceWeb   Surface = "web"
	SurfaceAPI   Surface = "api"
	SurfaceMacOS Surface = "macos"
	SurfaceIOS   Surface = "ios"
)

type TaskKind string

const (
	TaskKindProof          TaskKind = "proof"
	TaskKindCounterexample TaskKind = "counterexample"
	TaskKindSmoke          TaskKind = "smoke"
)

type Priority string

const (
	PriorityP0 Priority = "P0"
	PriorityP1 Priority = "P1"
	PriorityP2 Priority = "P2"
	PriorityP3 Priority = "P3"
)

type TaskStatus string

const (
	TaskStatusQueued    TaskStatus = "queued"
	TaskStatusClaimed   TaskStatus = "claimed"
	TaskStatusPassed    TaskStatus = "passed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusBlocked   TaskStatus = "blocked"
	TaskStatusErrored   TaskStatus = "error"
	TaskStatusCancelled TaskStatus = "cancelled"
)

type RunOutcome string

const (
	RunOutcomePass    RunOutcome = "pass"
	RunOutcomeFail    RunOutcome = "fail"
	RunOutcomeFlaky   RunOutcome = "flaky"
	RunOutcomeBlocked RunOutcome = "blocked"
	RunOutcomeError   RunOutcome = "error"
)

type EvidenceKind string

const (
	EvidenceKindScreenshot EvidenceKind = "screenshot"
	EvidenceKindLog        EvidenceKind = "log"
	EvidenceKindTrace      EvidenceKind = "trace"
	EvidenceKindTranscript EvidenceKind = "transcript"
)

type VerdictStatus string

const (
	VerdictPass         VerdictStatus = "pass"
	VerdictFail         VerdictStatus = "fail"
	VerdictCannotVerify VerdictStatus = "cannot_verify"
)

type FeatureSpec struct {
	SchemaVersion      string                `json:"schema_version"`
	RunID              string                `json:"run_id"`
	Description        string                `json:"description"`
	AcceptanceCriteria []AcceptanceCriterion `json:"acceptance_criteria"`
	Preconditions      []string              `json:"preconditions,omitempty"`
	Surfaces           []Surface             `json:"surfaces"`
	Risks              []string              `json:"risks,omitempty"`
	OpenQuestions      []string              `json:"open_questions,omitempty"`
}

type AcceptanceCriterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type TestPlan struct {
	SchemaVersion string   `json:"schema_version"`
	RunID         string   `json:"run_id"`
	Journeys      []string `json:"journeys"`
	Assertions    []string `json:"assertions"`
}

type Task struct {
	SchemaVersion         string         `json:"schema_version"`
	TaskID                string         `json:"task_id"`
	RunID                 string         `json:"run_id"`
	Surface               Surface        `json:"surface"`
	Kind                  TaskKind       `json:"kind"`
	Priority              Priority       `json:"priority"`
	Status                TaskStatus     `json:"status"`
	DedupeKey             string         `json:"dedupe_key"`
	MaxAttempts           int            `json:"max_attempts"`
	AttemptCount          int            `json:"attempt_count"`
	AcceptanceCriteriaIDs []string       `json:"acceptance_criteria_ids,omitempty"`
	Payload               map[string]any `json:"payload,omitempty"`
	CreatedBy             string         `json:"created_by,omitempty"`
	ClaimedBy             string         `json:"claimed_by,omitempty"`
}

type Run struct {
	SchemaVersion string     `json:"schema_version"`
	RunID         string     `json:"run_id"`
	TaskID        string     `json:"task_id"`
	SandboxID     string     `json:"sandbox_id,omitempty"`
	Outcome       RunOutcome `json:"outcome"`
	Summary       string     `json:"summary,omitempty"`
	TraceRef      string     `json:"action_trace_ref,omitempty"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    time.Time  `json:"finished_at"`
}

type Evidence struct {
	SchemaVersion string            `json:"schema_version"`
	EvidenceID    string            `json:"evidence_id"`
	RunID         string            `json:"run_id"`
	Kind          EvidenceKind      `json:"kind"`
	Path          string            `json:"path"`
	MIME          string            `json:"mime"`
	Bytes         int64             `json:"bytes"`
	SummaryFields map[string]string `json:"summary_fields,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
}

type ActionTrace struct {
	SchemaVersion string    `json:"schema_version"`
	TraceID       string    `json:"trace_id"`
	RunID         string    `json:"run_id"`
	TaskID        string    `json:"task_id"`
	Runner        string    `json:"runner"`
	Command       []string  `json:"command"`
	StdinPath     string    `json:"stdin_path"`
	StdoutPath    string    `json:"stdout_path"`
	StderrPath    string    `json:"stderr_path"`
	ExitCode      int       `json:"exit_code"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
}

type Finding struct {
	SchemaVersion string   `json:"schema_version"`
	FindingID     string   `json:"finding_id"`
	RunID         string   `json:"run_id"`
	CriterionID   string   `json:"criterion_id"`
	Severity      string   `json:"severity"`
	Summary       string   `json:"summary"`
	ReproSteps    []string `json:"repro_steps"`
	EvidenceRefs  []string `json:"evidence_refs"`
}

type Verdict struct {
	SchemaVersion string              `json:"schema_version"`
	VerdictID     string              `json:"verdict_id"`
	RunID         string              `json:"run_id"`
	Status        VerdictStatus       `json:"status"`
	Reasons       []string            `json:"reasons"`
	Coverage      map[string][]string `json:"coverage"`
	Findings      []Finding           `json:"findings,omitempty"`
}

func (f FeatureSpec) Validate() error {
	if err := validateSchemaVersion(f.SchemaVersion); err != nil {
		return err
	}
	if strings.TrimSpace(f.RunID) == "" {
		return errors.New("feature_spec.run_id is required")
	}
	if strings.TrimSpace(f.Description) == "" {
		return errors.New("feature_spec.description is required")
	}
	if len(f.AcceptanceCriteria) == 0 {
		return errors.New("feature_spec.acceptance_criteria is required")
	}
	for i, criterion := range f.AcceptanceCriteria {
		if err := criterion.Validate(); err != nil {
			return fmt.Errorf("feature_spec.acceptance_criteria[%d]: %w", i, err)
		}
	}
	for i, surface := range f.Surfaces {
		if !surface.IsValid() {
			return fmt.Errorf("feature_spec.surfaces[%d] is invalid: %q", i, surface)
		}
	}
	return nil
}

func (a AcceptanceCriterion) Validate() error {
	if strings.TrimSpace(a.ID) == "" {
		return errors.New("id is required")
	}
	if strings.TrimSpace(a.Text) == "" {
		return errors.New("text is required")
	}
	return nil
}

func (t TestPlan) Validate() error {
	if err := validateSchemaVersion(t.SchemaVersion); err != nil {
		return err
	}
	if strings.TrimSpace(t.RunID) == "" {
		return errors.New("test_plan.run_id is required")
	}
	if len(t.Journeys) == 0 {
		return errors.New("test_plan.journeys is required")
	}
	if len(t.Assertions) == 0 {
		return errors.New("test_plan.assertions is required")
	}
	return nil
}

func (t Task) Validate() error {
	if err := validateSchemaVersion(t.SchemaVersion); err != nil {
		return err
	}
	if strings.TrimSpace(t.TaskID) == "" {
		return errors.New("task.task_id is required")
	}
	if strings.TrimSpace(t.RunID) == "" {
		return errors.New("task.run_id is required")
	}
	if !t.Surface.IsValid() {
		return fmt.Errorf("task.surface is invalid: %q", t.Surface)
	}
	if !t.Kind.IsValid() {
		return fmt.Errorf("task.kind is invalid: %q", t.Kind)
	}
	if !t.Priority.IsValid() {
		return fmt.Errorf("task.priority is invalid: %q", t.Priority)
	}
	if !t.Status.IsValid() {
		return fmt.Errorf("task.status is invalid: %q", t.Status)
	}
	if strings.TrimSpace(t.DedupeKey) == "" {
		return errors.New("task.dedupe_key is required")
	}
	if t.MaxAttempts < 1 {
		return errors.New("task.max_attempts must be >= 1")
	}
	if t.AttemptCount < 0 {
		return errors.New("task.attempt_count must be >= 0")
	}
	return nil
}

func (r Run) Validate() error {
	if err := validateSchemaVersion(r.SchemaVersion); err != nil {
		return err
	}
	if strings.TrimSpace(r.RunID) == "" {
		return errors.New("run.run_id is required")
	}
	if strings.TrimSpace(r.TaskID) == "" {
		return errors.New("run.task_id is required")
	}
	if !r.Outcome.IsValid() {
		return fmt.Errorf("run.outcome is invalid: %q", r.Outcome)
	}
	if r.StartedAt.IsZero() {
		return errors.New("run.started_at is required")
	}
	if r.FinishedAt.IsZero() {
		return errors.New("run.finished_at is required")
	}
	if r.FinishedAt.Before(r.StartedAt) {
		return errors.New("run.finished_at must be >= run.started_at")
	}
	return nil
}

func (e Evidence) Validate() error {
	if err := validateSchemaVersion(e.SchemaVersion); err != nil {
		return err
	}
	if strings.TrimSpace(e.EvidenceID) == "" {
		return errors.New("evidence.evidence_id is required")
	}
	if strings.TrimSpace(e.RunID) == "" {
		return errors.New("evidence.run_id is required")
	}
	if !e.Kind.IsValid() {
		return fmt.Errorf("evidence.kind is invalid: %q", e.Kind)
	}
	if strings.TrimSpace(e.Path) == "" {
		return errors.New("evidence.path is required")
	}
	if e.Bytes < 0 {
		return errors.New("evidence.bytes must be >= 0")
	}
	if e.CreatedAt.IsZero() {
		return errors.New("evidence.created_at is required")
	}
	return nil
}

func (a ActionTrace) Validate() error {
	if err := validateSchemaVersion(a.SchemaVersion); err != nil {
		return err
	}
	if strings.TrimSpace(a.TraceID) == "" {
		return errors.New("action_trace.trace_id is required")
	}
	if strings.TrimSpace(a.RunID) == "" {
		return errors.New("action_trace.run_id is required")
	}
	if strings.TrimSpace(a.TaskID) == "" {
		return errors.New("action_trace.task_id is required")
	}
	if strings.TrimSpace(a.Runner) == "" {
		return errors.New("action_trace.runner is required")
	}
	if len(a.Command) == 0 {
		return errors.New("action_trace.command is required")
	}
	if a.StartedAt.IsZero() || a.FinishedAt.IsZero() {
		return errors.New("action_trace started_at and finished_at are required")
	}
	return nil
}

func (f Finding) Validate() error {
	if err := validateSchemaVersion(f.SchemaVersion); err != nil {
		return err
	}
	if strings.TrimSpace(f.FindingID) == "" {
		return errors.New("finding.finding_id is required")
	}
	if strings.TrimSpace(f.RunID) == "" {
		return errors.New("finding.run_id is required")
	}
	if strings.TrimSpace(f.CriterionID) == "" {
		return errors.New("finding.criterion_id is required")
	}
	if strings.TrimSpace(f.Severity) == "" {
		return errors.New("finding.severity is required")
	}
	if strings.TrimSpace(f.Summary) == "" {
		return errors.New("finding.summary is required")
	}
	if len(f.EvidenceRefs) == 0 {
		return errors.New("finding.evidence_refs is required")
	}
	return nil
}

func (v Verdict) Validate() error {
	if err := validateSchemaVersion(v.SchemaVersion); err != nil {
		return err
	}
	if strings.TrimSpace(v.VerdictID) == "" {
		return errors.New("verdict.verdict_id is required")
	}
	if strings.TrimSpace(v.RunID) == "" {
		return errors.New("verdict.run_id is required")
	}
	if !v.Status.IsValid() {
		return fmt.Errorf("verdict.status is invalid: %q", v.Status)
	}
	if len(v.Reasons) == 0 {
		return errors.New("verdict.reasons is required")
	}
	if len(v.Coverage) == 0 {
		return errors.New("verdict.coverage is required")
	}
	for i, finding := range v.Findings {
		if err := finding.Validate(); err != nil {
			return fmt.Errorf("verdict.findings[%d]: %w", i, err)
		}
	}
	return nil
}

func validateSchemaVersion(version string) error {
	if strings.TrimSpace(version) == "" {
		return errors.New("schema_version is required")
	}
	if version != CurrentSchemaVersion {
		return fmt.Errorf("unsupported schema_version: %q", version)
	}
	return nil
}

func (s Surface) IsValid() bool {
	switch s {
	case SurfaceWeb, SurfaceAPI, SurfaceMacOS, SurfaceIOS:
		return true
	default:
		return false
	}
}

func (k TaskKind) IsValid() bool {
	switch k {
	case TaskKindProof, TaskKindCounterexample, TaskKindSmoke:
		return true
	default:
		return false
	}
}

func (p Priority) IsValid() bool {
	switch p {
	case PriorityP0, PriorityP1, PriorityP2, PriorityP3:
		return true
	default:
		return false
	}
}

func (s TaskStatus) IsValid() bool {
	switch s {
	case TaskStatusQueued, TaskStatusClaimed, TaskStatusPassed, TaskStatusFailed, TaskStatusBlocked, TaskStatusErrored, TaskStatusCancelled:
		return true
	default:
		return false
	}
}

func (o RunOutcome) IsValid() bool {
	switch o {
	case RunOutcomePass, RunOutcomeFail, RunOutcomeFlaky, RunOutcomeBlocked, RunOutcomeError:
		return true
	default:
		return false
	}
}

func (k EvidenceKind) IsValid() bool {
	switch k {
	case EvidenceKindScreenshot, EvidenceKindLog, EvidenceKindTrace, EvidenceKindTranscript:
		return true
	default:
		return false
	}
}

func (s VerdictStatus) IsValid() bool {
	switch s {
	case VerdictPass, VerdictFail, VerdictCannotVerify:
		return true
	default:
		return false
	}
}
