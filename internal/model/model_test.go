package model

import (
	"testing"
	"time"
)

func TestFeatureSpecValidate(t *testing.T) {
	valid := FeatureSpec{
		SchemaVersion: CurrentSchemaVersion,
		RunID:         "run_1",
		Description:   "Validate login flow",
		AcceptanceCriteria: []AcceptanceCriterion{
			{ID: "ac_1", Text: "User can login with valid creds"},
		},
		Surfaces: []Surface{SurfaceWeb},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	invalid := valid
	invalid.AcceptanceCriteria = nil
	if err := invalid.Validate(); err == nil {
		t.Fatal("Validate() expected error for missing criteria")
	}
}

func TestTaskValidate(t *testing.T) {
	task := Task{
		SchemaVersion: CurrentSchemaVersion,
		TaskID:        "task_1",
		RunID:         "run_1",
		Surface:       SurfaceWeb,
		Kind:          TaskKindProof,
		Priority:      PriorityP1,
		Status:        TaskStatusQueued,
		DedupeKey:     "web:login:proof",
		MaxAttempts:   2,
		AttemptCount:  0,
	}
	if err := task.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	invalid := task
	invalid.Surface = "other"
	if err := invalid.Validate(); err == nil {
		t.Fatal("Validate() expected error for invalid surface")
	}
}

func TestRunEvidenceAndVerdictValidate(t *testing.T) {
	now := time.Now().UTC()
	run := Run{
		SchemaVersion: CurrentSchemaVersion,
		RunID:         "run_1",
		TaskID:        "task_1",
		Outcome:       RunOutcomePass,
		StartedAt:     now,
		FinishedAt:    now.Add(time.Second),
	}
	if err := run.Validate(); err != nil {
		t.Fatalf("run.Validate() error = %v", err)
	}

	evidence := Evidence{
		SchemaVersion: CurrentSchemaVersion,
		EvidenceID:    "ev_1",
		RunID:         "run_1",
		Kind:          EvidenceKindLog,
		Path:          "runs/run_1/artifacts/log.txt",
		Bytes:         10,
		CreatedAt:     now,
	}
	if err := evidence.Validate(); err != nil {
		t.Fatalf("evidence.Validate() error = %v", err)
	}

	verdict := Verdict{
		SchemaVersion: CurrentSchemaVersion,
		VerdictID:     "verdict_1",
		RunID:         "run_1",
		Status:        VerdictPass,
		Reasons:       []string{"All criteria proven"},
		Coverage: map[string][]string{
			"ac_1": {"ev_1"},
		},
	}
	if err := verdict.Validate(); err != nil {
		t.Fatalf("verdict.Validate() error = %v", err)
	}

	invalidVerdict := verdict
	invalidVerdict.Status = "maybe"
	if err := invalidVerdict.Validate(); err == nil {
		t.Fatal("verdict.Validate() expected error for invalid status")
	}
}

func TestActionTraceAndFindingValidate(t *testing.T) {
	now := time.Now().UTC()
	trace := ActionTrace{
		SchemaVersion: CurrentSchemaVersion,
		TraceID:       "trace_1",
		RunID:         "run_1",
		TaskID:        "task_1",
		Runner:        "web",
		Command:       []string{"runner", "--input", "in.json"},
		StartedAt:     now,
		FinishedAt:    now.Add(time.Second),
	}
	if err := trace.Validate(); err != nil {
		t.Fatalf("trace.Validate() error = %v", err)
	}

	finding := Finding{
		SchemaVersion: CurrentSchemaVersion,
		FindingID:     "finding_1",
		RunID:         "run_1",
		CriterionID:   "ac_1",
		Severity:      "high",
		Summary:       "Login fails for valid user",
		ReproSteps:    []string{"Open login", "Enter valid creds", "Submit"},
		EvidenceRefs:  []string{"ev_1"},
	}
	if err := finding.Validate(); err != nil {
		t.Fatalf("finding.Validate() error = %v", err)
	}

	invalid := finding
	invalid.EvidenceRefs = nil
	if err := invalid.Validate(); err == nil {
		t.Fatal("finding.Validate() expected error for missing evidence refs")
	}
}
