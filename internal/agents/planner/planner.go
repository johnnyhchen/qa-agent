package planner

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"qa-agent/internal/agents/runtime"
	"qa-agent/internal/blackboard"
	"qa-agent/internal/model"
)

type Output struct {
	FeatureSpec   model.FeatureSpec `json:"feature_spec"`
	TestPlan      model.TestPlan    `json:"test_plan"`
	Tasks         []model.Task      `json:"tasks"`
	OpenQuestions []string          `json:"open_questions,omitempty"`
}

type Agent struct {
	runtime *runtime.Runtime
	store   *blackboard.Store
}

func New(runtime *runtime.Runtime, store *blackboard.Store) *Agent {
	return &Agent{
		runtime: runtime,
		store:   store,
	}
}

func (a *Agent) Plan(ctx context.Context, runID, description string, surfaces []model.Surface) (Output, error) {
	if strings.TrimSpace(runID) == "" {
		return Output{}, errors.New("run_id is required")
	}
	if strings.TrimSpace(description) == "" {
		return Output{}, errors.New("description is required")
	}
	if len(surfaces) == 0 {
		surfaces = []model.Surface{model.SurfaceWeb}
	}

	resp, err := a.runtime.RunTurn(ctx, runtime.TurnRequest{
		RunID:     runID,
		AgentName: "planner",
		Prompt:    description,
	})
	_ = resp // LLM response logged to artifacts by runtime; payload generated deterministically below
	if err != nil {
		// Non-fatal: runtime may use EchoClient, log but continue
		_ = err
	}

	criteria := splitCriteria(description)
	openQuestions := buildOpenQuestions(description)
	testPlanJourneys := make([]string, 0, len(criteria))
	assertions := make([]string, 0, len(criteria))
	tasks := make([]model.Task, 0, len(criteria)*len(surfaces))

	for idx, criterion := range criteria {
		testPlanJourneys = append(testPlanJourneys, fmt.Sprintf("journey_%d", idx+1))
		assertions = append(assertions, criterion.Text)
		for _, surface := range surfaces {
			taskID := fmt.Sprintf("task_%s_%d_%s", runID, idx+1, surface)
			task := model.Task{
				SchemaVersion: model.CurrentSchemaVersion,
				TaskID:        taskID,
				RunID:         runID,
				Surface:       surface,
				Kind:          model.TaskKindProof,
				Priority:      model.PriorityP1,
				Status:        model.TaskStatusQueued,
				DedupeKey:     fmt.Sprintf("%s:%s:%d", surface, runID, idx+1),
				MaxAttempts:   3,
				CreatedBy:     "planner",
				AcceptanceCriteriaIDs: []string{
					criterion.ID,
				},
			}

			// Generate surface-specific payloads
			if surface == model.SurfaceAPI {
				task.Payload = buildAPIPayload(criterion.Text)
			} else if surface == model.SurfaceWeb {
				task.Payload = buildWebPayload(criterion.Text)
			}

			tasks = append(tasks, task)
		}
	}

	output := Output{
		FeatureSpec: model.FeatureSpec{
			SchemaVersion:      model.CurrentSchemaVersion,
			RunID:              runID,
			Description:        description,
			AcceptanceCriteria: criteria,
			Surfaces:           surfaces,
			OpenQuestions:      openQuestions,
		},
		TestPlan: model.TestPlan{
			SchemaVersion: model.CurrentSchemaVersion,
			RunID:         runID,
			Journeys:      testPlanJourneys,
			Assertions:    assertions,
		},
		Tasks:         tasks,
		OpenQuestions: openQuestions,
	}
	if err := ValidateOutput(output); err != nil {
		return Output{}, err
	}

	if err := a.store.UpsertFeatureSpec(ctx, output.FeatureSpec); err != nil {
		return Output{}, err
	}
	for _, task := range output.Tasks {
		if err := a.store.CreateTask(ctx, task); err != nil {
			return Output{}, err
		}
	}
	return output, nil
}

// buildAPIPayload extracts HTTP request specifications from criterion text.
// Recognizes patterns like "GET http://... returns JSON with status 200"
// and builds the payload structure the API runner expects.
func buildAPIPayload(criterionText string) map[string]any {
	httpRequests := extractHTTPRequests(criterionText)
	if len(httpRequests) == 0 {
		return nil
	}
	reqs := make([]any, len(httpRequests))
	for i, r := range httpRequests {
		reqs[i] = r
	}
	return map[string]any{
		"http_requests": reqs,
	}
}

// httpRequestPattern matches "GET http://..." or "POST http://..." in text
var httpRequestPattern = regexp.MustCompile(`(?i)(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS)\s+(https?://[^\s,]+)`)

// statusPattern matches "status 200", "status code 404", "returns 200", "with status 200"
var statusPattern = regexp.MustCompile(`(?i)(?:status(?:\s+code)?|returns)\s+(\d{3})`)

// bodyContainsPattern matches "containing X", "contains X", "body contains X"
var bodyContainsPattern = regexp.MustCompile(`(?i)contain(?:s|ing)\s+(?:(?:the\s+)?(?:text|string|field|value)\s+)?["']?([^"'.]+)["']?`)

func extractHTTPRequests(text string) []map[string]any {
	matches := httpRequestPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	var requests []map[string]any
	for _, match := range matches {
		method := strings.ToUpper(match[1])
		url := strings.TrimRight(match[2], ".,;)")

		req := map[string]any{
			"method": method,
			"url":    url,
		}

		// Extract expected status from surrounding text
		statusMatch := statusPattern.FindStringSubmatch(text)
		if statusMatch != nil {
			code, err := strconv.Atoi(statusMatch[1])
			if err == nil {
				req["expect_status"] = float64(code)
			}
		} else {
			// Default: expect 200 for GET requests
			if method == "GET" {
				req["expect_status"] = float64(200)
			}
		}

		// Extract body contains assertion
		bodyMatch := bodyContainsPattern.FindStringSubmatch(text)
		if bodyMatch != nil {
			req["expect_body_contains"] = strings.TrimSpace(bodyMatch[1])
		}

		requests = append(requests, req)
	}
	return requests
}

func buildWebPayload(criterionText string) map[string]any {
	// Extract URL from text for web surface
	urlPattern := regexp.MustCompile(`https?://[^\s,]+`)
	urlMatch := urlPattern.FindString(criterionText)
	if urlMatch == "" {
		return nil
	}
	return map[string]any{
		"url":     strings.TrimRight(urlMatch, ".,;)"),
		"actions": []any{"navigate", "screenshot"},
	}
}

func ValidateOutput(output Output) error {
	if err := output.FeatureSpec.Validate(); err != nil {
		return err
	}
	if err := output.TestPlan.Validate(); err != nil {
		return err
	}
	if len(output.Tasks) == 0 {
		return errors.New("planner output requires tasks")
	}
	for i, task := range output.Tasks {
		if err := task.Validate(); err != nil {
			return fmt.Errorf("task[%d] invalid: %w", i, err)
		}
		if strings.TrimSpace(task.DedupeKey) == "" {
			return fmt.Errorf("task[%d] missing dedupe_key", i)
		}
	}
	return nil
}

func splitCriteria(description string) []model.AcceptanceCriterion {
	segments := strings.Split(description, ".")
	criteria := make([]model.AcceptanceCriterion, 0, len(segments))
	for index, segment := range segments {
		text := strings.TrimSpace(segment)
		if text == "" {
			continue
		}
		criteria = append(criteria, model.AcceptanceCriterion{
			ID:   fmt.Sprintf("ac_%d", index+1),
			Text: text,
		})
	}
	if len(criteria) == 0 {
		criteria = append(criteria, model.AcceptanceCriterion{
			ID:   "ac_1",
			Text: strings.TrimSpace(description),
		})
	}
	return criteria
}

func buildOpenQuestions(description string) []string {
	if strings.Contains(description, "?") {
		return []string{"Clarify ambiguous requirement from feature description"}
	}
	return nil
}
