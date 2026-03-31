package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qa-agent/internal/model"
)

func TestManifestReferencesExistingFiles(t *testing.T) {
	runDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), []byte(`{"run_id":"run_1"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(run.json) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(runDir, "artifacts"), 0o755); err != nil {
		t.Fatalf("MkdirAll(artifacts) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "artifacts", "trace.log"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFile(trace.log) error = %v", err)
	}

	generator := NewGenerator()
	_, manifestPath, err := generator.Write("run_1", runDir)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("ReadFile(manifest) error = %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("json.Unmarshal(manifest) error = %v", err)
	}
	for _, file := range manifest.Files {
		if _, err := os.Stat(filepath.Join(runDir, file)); err != nil {
			t.Fatalf("manifest file %s does not exist: %v", file, err)
		}
	}
}

func TestReportOutputContainsRequiredSectionsStableOrder(t *testing.T) {
	runDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), []byte(`{"run_id":"run_2"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(run.json) error = %v", err)
	}
	generator := NewGenerator()
	_, _, markdown, err := generator.Generate("run_2", runDir)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	sections := []string{"## Verdict", "## Coverage", "## Findings", "## Task Summary", "## Evidence Bundle"}
	lastIndex := -1
	for _, section := range sections {
		idx := strings.Index(markdown, section)
		if idx == -1 {
			t.Fatalf("missing section %q in report output", section)
		}
		if idx < lastIndex {
			t.Fatalf("section %q appears out of order", section)
		}
		lastIndex = idx
	}
}

func TestBundleCreatesArchive(t *testing.T) {
	runDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), []byte(`{"run_id":"run_3"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(run.json) error = %v", err)
	}
	generator := NewGenerator()
	if _, _, err := generator.Write("run_3", runDir); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	zipPath := filepath.Join(t.TempDir(), "bundle.zip")
	if err := generator.Bundle("run_3", runDir, zipPath); err != nil {
		t.Fatalf("Bundle() error = %v", err)
	}
	info, err := os.Stat(zipPath)
	if err != nil {
		t.Fatalf("Stat(bundle) error = %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("bundle archive is empty")
	}
}

// writeVerdictJSON marshals a verdict to verdict.json in the given directory.
func writeVerdictJSON(t *testing.T, dir string, v model.Verdict) {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal verdict: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "verdict.json"), raw, 0o644); err != nil {
		t.Fatalf("write verdict.json: %v", err)
	}
}

func TestReportSurfacesVerdictReasons(t *testing.T) {
	runDir := t.TempDir()

	writeVerdictJSON(t, runDir, model.Verdict{
		SchemaVersion: model.CurrentSchemaVersion,
		VerdictID:     "verdict_1",
		RunID:         "run_reasons",
		Status:        model.VerdictFail,
		Reasons: []string{
			"At least one acceptance criterion has stable failure evidence",
			"Login endpoint accepts any credentials",
		},
		Coverage: map[string][]string{
			"ac_1": {"trace_ref_1"},
		},
	})

	generator := NewGenerator()
	_, _, markdown, err := generator.Generate("run_reasons", runDir)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if !strings.Contains(markdown, "### Reasons") {
		t.Fatal("report missing '### Reasons' section")
	}
	for _, reason := range []string{
		"At least one acceptance criterion has stable failure evidence",
		"Login endpoint accepts any credentials",
	} {
		if !strings.Contains(markdown, reason) {
			t.Fatalf("report missing reason %q", reason)
		}
	}
	if !strings.Contains(markdown, "`fail`") {
		t.Fatal("report missing verdict status 'fail'")
	}
}

func TestReportSurfacesFindings(t *testing.T) {
	runDir := t.TempDir()

	writeVerdictJSON(t, runDir, model.Verdict{
		SchemaVersion: model.CurrentSchemaVersion,
		VerdictID:     "verdict_2",
		RunID:         "run_findings",
		Status:        model.VerdictFail,
		Reasons:       []string{"Failure detected"},
		Coverage: map[string][]string{
			"ac_1": {"trace_ref_1"},
		},
		Findings: []model.Finding{
			{
				SchemaVersion: model.CurrentSchemaVersion,
				FindingID:     "finding_ac_1_0",
				RunID:         "run_findings",
				CriterionID:   "ac_1",
				Severity:      "high",
				Summary:       "Stable counterexample found",
				ReproSteps:    []string{"Replay failed task and inspect evidence bundle"},
				EvidenceRefs:  []string{"trace_ref_1"},
			},
		},
	})

	generator := NewGenerator()
	_, _, markdown, err := generator.Generate("run_findings", runDir)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	for _, want := range []string{
		"### Finding: finding_ac_1_0",
		"**Severity**: high",
		"**Criterion**: ac_1",
		"**Summary**: Stable counterexample found",
		"Replay failed task and inspect evidence bundle",
		"**Evidence**: trace_ref_1",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("report missing finding detail %q", want)
		}
	}
}

func TestReportSurfacesCoverage(t *testing.T) {
	runDir := t.TempDir()

	writeVerdictJSON(t, runDir, model.Verdict{
		SchemaVersion: model.CurrentSchemaVersion,
		VerdictID:     "verdict_3",
		RunID:         "run_coverage",
		Status:        model.VerdictFail,
		Reasons:       []string{"Failure detected"},
		Coverage: map[string][]string{
			"ac_1": {"trace_ref_1", "trace_ref_2"},
			"ac_2": {"missing"},
			"ac_3": {"trace_ref_3"},
		},
	})

	generator := NewGenerator()
	_, _, markdown, err := generator.Generate("run_coverage", runDir)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// Should have a coverage table
	if !strings.Contains(markdown, "| Criterion | Status | Evidence |") {
		t.Fatal("report missing coverage table header")
	}
	// ac_1 should be covered
	if !strings.Contains(markdown, "| ac_1 | covered |") {
		t.Fatal("report missing ac_1 as covered")
	}
	// ac_2 should be missing
	if !strings.Contains(markdown, "| ac_2 | missing |") {
		t.Fatal("report missing ac_2 as missing")
	}
	// ac_3 should be covered
	if !strings.Contains(markdown, "| ac_3 | covered |") {
		t.Fatal("report missing ac_3 as covered")
	}
}

func TestReportTaskSummary(t *testing.T) {
	runDir := t.TempDir()

	writeVerdictJSON(t, runDir, model.Verdict{
		SchemaVersion: model.CurrentSchemaVersion,
		VerdictID:     "verdict_4",
		RunID:         "run_tasksummary",
		Status:        model.VerdictFail,
		Reasons:       []string{"Failure detected"},
		Coverage: map[string][]string{
			"ac_1": {"trace_ref_1"},
			"ac_2": {"missing"},
			"ac_3": {"trace_ref_3"},
		},
		Findings: []model.Finding{
			{
				SchemaVersion: model.CurrentSchemaVersion,
				FindingID:     "finding_ac_1",
				RunID:         "run_tasksummary",
				CriterionID:   "ac_1",
				Severity:      "high",
				Summary:       "Counterexample found",
				ReproSteps:    []string{"step1"},
				EvidenceRefs:  []string{"trace_ref_1"},
			},
		},
	})

	generator := NewGenerator()
	_, _, markdown, err := generator.Generate("run_tasksummary", runDir)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if !strings.Contains(markdown, "## Task Summary") {
		t.Fatal("report missing '## Task Summary' section")
	}
	// Total 3 criteria: 1 failed (ac_1), 1 blocked (ac_2 missing), 1 passed (ac_3)
	if !strings.Contains(markdown, "Total: 3") {
		t.Fatal("report should show Total: 3")
	}
	if !strings.Contains(markdown, "Passed: 1") {
		t.Fatal("report should show Passed: 1")
	}
	if !strings.Contains(markdown, "Failed: 1") {
		t.Fatal("report should show Failed: 1")
	}
	if !strings.Contains(markdown, "Blocked: 1") {
		t.Fatal("report should show Blocked: 1")
	}
}
