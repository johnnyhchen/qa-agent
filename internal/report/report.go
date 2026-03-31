package report

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"qa-agent/internal/model"
)

type Manifest struct {
	RunID       string   `json:"run_id"`
	GeneratedAt string   `json:"generated_at"`
	Files       []string `json:"files"`
}

type TaskSummary struct {
	Total   int
	Passed  int
	Failed  int
	Errored int
	Blocked int
}

type Summary struct {
	RunID         string
	GeneratedAt   time.Time
	Verdict       model.Verdict
	ArtifactCount int
	TaskSummary   TaskSummary
}

type Generator struct{}

func NewGenerator() *Generator {
	return &Generator{}
}

func (g *Generator) Generate(runID, runDir string) (Summary, Manifest, string, error) {
	files, err := listFiles(runDir)
	if err != nil {
		return Summary{}, Manifest{}, "", err
	}
	manifest := Manifest{
		RunID:       runID,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Files:       files,
	}
	verdict := loadVerdict(runDir)
	summary := Summary{
		RunID:         runID,
		GeneratedAt:   time.Now().UTC(),
		Verdict:       verdict,
		ArtifactCount: len(files),
		TaskSummary:   loadTaskSummary(verdict),
	}
	markdown := buildMarkdown(summary, manifest)
	return summary, manifest, markdown, nil
}

func (g *Generator) Write(runID, runDir string) (reportPath string, manifestPath string, err error) {
	_, manifest, markdown, err := g.Generate(runID, runDir)
	if err != nil {
		return "", "", err
	}
	reportPath = filepath.Join(runDir, "report.md")
	manifestPath = filepath.Join(runDir, "manifest.json")
	rawManifest, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(reportPath, []byte(markdown), 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(manifestPath, rawManifest, 0o644); err != nil {
		return "", "", err
	}
	return reportPath, manifestPath, nil
}

func (g *Generator) Bundle(runID, runDir, outputZip string) error {
	manifestPath := filepath.Join(runDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		return fmt.Errorf("manifest missing, run report first: %w", err)
	}
	files, err := listFiles(runDir)
	if err != nil {
		return err
	}
	file, err := os.Create(outputZip)
	if err != nil {
		return err
	}
	defer file.Close()

	zipWriter := zip.NewWriter(file)
	defer zipWriter.Close()

	for _, relativePath := range files {
		fullPath := filepath.Join(runDir, relativePath)
		info, err := os.Stat(fullPath)
		if err != nil || info.IsDir() {
			continue
		}
		reader, err := os.Open(fullPath)
		if err != nil {
			return err
		}
		writer, err := zipWriter.Create(relativePath)
		if err != nil {
			_ = reader.Close()
			return err
		}
		if _, err := io.Copy(writer, reader); err != nil {
			_ = reader.Close()
			return err
		}
		_ = reader.Close()
	}
	return nil
}

func buildMarkdown(summary Summary, manifest Manifest) string {
	var b strings.Builder

	// Header
	b.WriteString("# QA-Agent Report\n\n")

	// Verdict section
	b.WriteString("## Verdict\n")
	b.WriteString(fmt.Sprintf("- Run ID: `%s`\n", summary.RunID))
	status := string(summary.Verdict.Status)
	if status == "" {
		status = "unknown"
	}
	b.WriteString(fmt.Sprintf("- Status: `%s`\n", status))
	b.WriteString(fmt.Sprintf("- Generated At: `%s`\n", summary.GeneratedAt.UTC().Format(time.RFC3339Nano)))

	if len(summary.Verdict.Reasons) > 0 {
		b.WriteString("\n### Reasons\n")
		for _, reason := range summary.Verdict.Reasons {
			b.WriteString(fmt.Sprintf("- %s\n", reason))
		}
	}

	// Coverage section
	b.WriteString("\n## Coverage\n")
	b.WriteString(fmt.Sprintf("- Artifact files: `%d`\n", summary.ArtifactCount))
	if len(summary.Verdict.Coverage) > 0 {
		b.WriteString("\n| Criterion | Status | Evidence |\n")
		b.WriteString("|-----------|--------|----------|\n")
		keys := make([]string, 0, len(summary.Verdict.Coverage))
		for k := range summary.Verdict.Coverage {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, criterion := range keys {
			refs := summary.Verdict.Coverage[criterion]
			coverageStatus := "covered"
			if len(refs) == 1 && refs[0] == "missing" {
				coverageStatus = "missing"
			}
			b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", criterion, coverageStatus, strings.Join(refs, ", ")))
		}
	}

	// Findings section
	b.WriteString("\n## Findings\n")
	if len(summary.Verdict.Findings) > 0 {
		for _, finding := range summary.Verdict.Findings {
			b.WriteString(fmt.Sprintf("\n### Finding: %s\n", finding.FindingID))
			b.WriteString(fmt.Sprintf("- **Severity**: %s\n", finding.Severity))
			b.WriteString(fmt.Sprintf("- **Criterion**: %s\n", finding.CriterionID))
			b.WriteString(fmt.Sprintf("- **Summary**: %s\n", finding.Summary))
			if len(finding.ReproSteps) > 0 {
				b.WriteString("- **Repro Steps**:\n")
				for i, step := range finding.ReproSteps {
					b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, step))
				}
			}
			if len(finding.EvidenceRefs) > 0 {
				b.WriteString(fmt.Sprintf("- **Evidence**: %s\n", strings.Join(finding.EvidenceRefs, ", ")))
			}
		}
	} else {
		b.WriteString("- No findings.\n")
	}

	// Task Summary section
	b.WriteString("\n## Task Summary\n")
	ts := summary.TaskSummary
	b.WriteString(fmt.Sprintf("- Total: %d\n", ts.Total))
	b.WriteString(fmt.Sprintf("- Passed: %d\n", ts.Passed))
	b.WriteString(fmt.Sprintf("- Failed: %d\n", ts.Failed))
	b.WriteString(fmt.Sprintf("- Errored: %d\n", ts.Errored))
	b.WriteString(fmt.Sprintf("- Blocked: %d\n", ts.Blocked))

	// Evidence Bundle section
	b.WriteString("\n## Evidence Bundle\n")
	b.WriteString("- See `manifest.json` for the full artifact list.\n")

	return b.String()
}

func loadVerdict(runDir string) model.Verdict {
	verdictPath := filepath.Join(runDir, "verdict.json")
	raw, err := os.ReadFile(verdictPath)
	if err != nil {
		return model.Verdict{}
	}
	var v model.Verdict
	if err := json.Unmarshal(raw, &v); err != nil {
		return model.Verdict{}
	}
	return v
}

func loadTaskSummary(verdict model.Verdict) TaskSummary {
	if len(verdict.Coverage) == 0 {
		return TaskSummary{}
	}

	failedCriteria := map[string]bool{}
	for _, f := range verdict.Findings {
		failedCriteria[f.CriterionID] = true
	}

	ts := TaskSummary{Total: len(verdict.Coverage)}
	for criterion, refs := range verdict.Coverage {
		isMissing := len(refs) == 1 && refs[0] == "missing"
		if failedCriteria[criterion] {
			ts.Failed++
		} else if isMissing {
			ts.Blocked++
		} else {
			ts.Passed++
		}
	}
	return ts
}

func listFiles(runDir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(runDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == runDir {
			return nil
		}
		relative, err := filepath.Rel(runDir, path)
		if err != nil {
			return err
		}
		files = append(files, relative)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}
