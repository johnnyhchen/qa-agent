package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	sections := []string{"## Verdict", "## Coverage", "## Evidence Bundle", "## Findings"}
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
