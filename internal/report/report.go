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
)

type Manifest struct {
	RunID       string   `json:"run_id"`
	GeneratedAt string   `json:"generated_at"`
	Files       []string `json:"files"`
}

type Summary struct {
	RunID         string
	GeneratedAt   time.Time
	Verdict       string
	ArtifactCount int
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
	summary := Summary{
		RunID:         runID,
		GeneratedAt:   time.Now().UTC(),
		Verdict:       detectVerdict(runDir),
		ArtifactCount: len(files),
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
	lines := []string{
		"# QA-Agent Report",
		"",
		"## Verdict",
		fmt.Sprintf("- Run ID: `%s`", summary.RunID),
		fmt.Sprintf("- Status: `%s`", summary.Verdict),
		fmt.Sprintf("- Generated At: `%s`", summary.GeneratedAt.UTC().Format(time.RFC3339Nano)),
		"",
		"## Coverage",
		fmt.Sprintf("- Artifact files: `%d`", summary.ArtifactCount),
		"",
		"## Evidence Bundle",
		"- See `manifest.json` for the full artifact list.",
		"",
		"## Findings",
		"- See trace and transcript artifacts for detailed findings.",
	}
	return strings.Join(lines, "\n")
}

func detectVerdict(runDir string) string {
	verdictPath := filepath.Join(runDir, "verdict.json")
	raw, err := os.ReadFile(verdictPath)
	if err != nil {
		return "unknown"
	}
	var payload struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "unknown"
	}
	if strings.TrimSpace(payload.Status) == "" {
		return "unknown"
	}
	return payload.Status
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
