package processor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
	"qa-agent/internal/runner"
)

type Processor struct {
	store       *blackboard.Store
	maxFileSize int
}

type ProcessRequest struct {
	Run          model.Run
	RunnerResult runner.Result
	ArtifactDir  string
}

type ProcessResult struct {
	RunSummary string
	Evidence   []model.Evidence
}

func New(store *blackboard.Store, maxFileSize int) *Processor {
	if maxFileSize <= 0 {
		maxFileSize = 256 * 1024
	}
	return &Processor{
		store:       store,
		maxFileSize: maxFileSize,
	}
}

func (p *Processor) Process(ctx context.Context, request ProcessRequest) (ProcessResult, error) {
	if err := request.Run.Validate(); err != nil {
		return ProcessResult{}, err
	}
	if err := p.store.CreateRun(ctx, request.Run); err != nil {
		return ProcessResult{}, err
	}

	files := make([]string, 0, len(request.RunnerResult.EvidenceFiles))
	for _, filePath := range request.RunnerResult.EvidenceFiles {
		if strings.TrimSpace(filePath) == "" {
			continue
		}
		if filepath.IsAbs(filePath) {
			files = append(files, filePath)
			continue
		}
		files = append(files, filepath.Join(request.ArtifactDir, filePath))
	}

	evidenceRows := make([]model.Evidence, 0, len(files))
	for index, filePath := range files {
		row, err := p.normalizeFile(request.Run.RunID, filePath, index)
		if err != nil {
			return ProcessResult{}, err
		}
		if err := p.store.CreateEvidence(ctx, row); err != nil {
			return ProcessResult{}, err
		}
		evidenceRows = append(evidenceRows, row)
	}

	runSummary := request.RunnerResult.Summary
	if runSummary == "" {
		runSummary = request.Run.Summary
	}
	if runSummary == "" {
		runSummary = "run completed"
	}
	runSummary = fmt.Sprintf("%s; evidence=%d; outcome=%s", runSummary, len(evidenceRows), request.RunnerResult.Outcome)

	return ProcessResult{
		RunSummary: runSummary,
		Evidence:   evidenceRows,
	}, nil
}

func (p *Processor) normalizeFile(runID, sourcePath string, index int) (model.Evidence, error) {
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		return model.Evidence{}, err
	}
	originalSize := len(raw)

	redacted := redactSecrets(raw)
	truncated := false
	normalizedPath := sourcePath

	if len(redacted) > p.maxFileSize {
		redacted = redacted[:p.maxFileSize]
		for len(redacted) > 0 && !utf8.Valid(redacted) {
			redacted = redacted[:len(redacted)-1]
		}
		truncated = true
	}
	if !equalBytes(raw, redacted) || truncated {
		normalizedPath = sourcePath + ".normalized"
		if err := os.WriteFile(normalizedPath, redacted, 0o644); err != nil {
			return model.Evidence{}, err
		}
	}

	digest := sha256.Sum256(redacted)
	summary := map[string]string{
		"source_path": filepath.Base(sourcePath),
		"sha256":      hex.EncodeToString(digest[:]),
		"truncated":   fmt.Sprintf("%t", truncated),
		"bytes_raw":   fmt.Sprintf("%d", originalSize),
		"bytes_saved": fmt.Sprintf("%d", len(redacted)),
	}

	return model.Evidence{
		SchemaVersion: model.CurrentSchemaVersion,
		EvidenceID:    fmt.Sprintf("ev_%d_%d", time.Now().UTC().UnixNano(), index),
		RunID:         runID,
		Kind:          inferEvidenceKind(sourcePath),
		Path:          normalizedPath,
		MIME:          inferMIME(sourcePath),
		Bytes:         int64(len(redacted)),
		SummaryFields: summary,
		CreatedAt:     time.Now().UTC(),
	}, nil
}

func redactSecrets(raw []byte) []byte {
	output := string(raw)
	replacements := map[*regexp.Regexp]string{
		regexp.MustCompile(`(?im)authorization:\s*.+`):           "authorization: [redacted]",
		regexp.MustCompile(`(?im)cookie:\s*.+`):                  "cookie: [redacted]",
		regexp.MustCompile(`(?im)set-cookie:\s*.+`):              "set-cookie: [redacted]",
		regexp.MustCompile(`(?im)(token|api[_-]?key)["=: ]+\S+`): "$1=[redacted]",
	}
	for pattern, replacement := range replacements {
		output = pattern.ReplaceAllString(output, replacement)
	}
	return []byte(output)
}

func inferEvidenceKind(path string) model.EvidenceKind {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg":
		return model.EvidenceKindScreenshot
	case ".json", ".har":
		return model.EvidenceKindTranscript
	default:
		return model.EvidenceKindLog
	}
}

func inferMIME(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		return "application/json"
	case ".har":
		return "application/json"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	default:
		return "text/plain"
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func ValidateProcessResult(result ProcessResult) error {
	if strings.TrimSpace(result.RunSummary) == "" {
		return errors.New("run summary is required")
	}
	if len(result.Evidence) == 0 {
		return errors.New("at least one evidence row is required")
	}
	return nil
}
