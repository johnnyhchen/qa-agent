package judge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"qa-agent/internal/model"
)

type Input struct {
	RunID       string            `json:"run_id"`
	FeatureSpec model.FeatureSpec `json:"feature_spec"`
	Tasks       []model.Task      `json:"tasks"`
	Runs        []model.Run       `json:"runs"`
	Evidence    []model.Evidence  `json:"evidence"`
	MaxRounds   int               `json:"max_rounds"`
}

type Output struct {
	NextTasks []model.Task    `json:"next_tasks,omitempty"`
	Verdict   *model.Verdict  `json:"verdict,omitempty"`
	Findings  []model.Finding `json:"findings,omitempty"`
}

type Agent struct{}

func New() *Agent {
	return &Agent{}
}

func (a *Agent) Evaluate(_ context.Context, input Input) (Output, error) {
	if input.MaxRounds <= 0 {
		input.MaxRounds = 1
	}
	if err := input.FeatureSpec.Validate(); err != nil {
		return Output{}, err
	}

	rolling := input
	final := Output{}
	for round := 0; round < input.MaxRounds; round++ {
		out := evaluateRound(rolling, round)
		if out.Verdict != nil {
			final = out
			break
		}
		if len(out.NextTasks) == 0 {
			break
		}
		final.NextTasks = append(final.NextTasks, out.NextTasks...)
		rolling.Tasks = append(rolling.Tasks, out.NextTasks...)
	}
	if err := ValidateOutput(final); err != nil {
		return Output{}, err
	}
	return final, nil
}

func ValidateOutput(output Output) error {
	hasTasks := len(output.NextTasks) > 0
	hasVerdict := output.Verdict != nil
	if hasTasks == hasVerdict {
		return errors.New("judge output must contain exactly one of next_tasks or verdict")
	}
	if hasVerdict {
		if err := output.Verdict.Validate(); err != nil {
			return err
		}
	}
	for i, task := range output.NextTasks {
		if err := task.Validate(); err != nil {
			return fmt.Errorf("next_tasks[%d] invalid: %w", i, err)
		}
	}
	for i, finding := range output.Findings {
		if err := finding.Validate(); err != nil {
			return fmt.Errorf("findings[%d] invalid: %w", i, err)
		}
	}
	return nil
}

func evaluateRound(input Input, round int) Output {
	criterionToTask := map[string][]model.Task{}
	for _, task := range input.Tasks {
		for _, criterionID := range task.AcceptanceCriteriaIDs {
			criterionToTask[criterionID] = append(criterionToTask[criterionID], task)
		}
	}

	pendingHighPriority := false
	for _, task := range input.Tasks {
		if (task.Priority == model.PriorityP0 || task.Priority == model.PriorityP1) && (task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusClaimed) {
			pendingHighPriority = true
			break
		}
	}

	hasBlockedRuns := false
	for _, run := range input.Runs {
		if run.Outcome == model.RunOutcomeBlocked {
			hasBlockedRuns = true
			break
		}
	}

	coverage := map[string][]string{}
	findings := []model.Finding{}
	failedCriteria := []string{}
	missingProof := []string{}
	evidenceByRun := map[string][]model.Evidence{}
	for _, item := range input.Evidence {
		evidenceByRun[item.RunID] = append(evidenceByRun[item.RunID], item)
	}

	for _, criterion := range input.FeatureSpec.AcceptanceCriteria {
		criterionID := criterion.ID
		criterionTasks := criterionToTask[criterionID]
		passFound := false
		failFound := false

		for _, task := range criterionTasks {
			for _, run := range input.Runs {
				if run.TaskID != task.TaskID {
					continue
				}
				switch run.Outcome {
				case model.RunOutcomePass:
					passFound = true
					coverage[criterionID] = append(coverage[criterionID], run.TraceRef)
				case model.RunOutcomeFail:
					failFound = true
					coverage[criterionID] = append(coverage[criterionID], run.TraceRef)
				}
			}
		}

		if failFound && passFound {
			missingProof = append(missingProof, criterionID)
		} else if failFound {
			failedCriteria = append(failedCriteria, criterionID)
			findings = append(findings, model.Finding{
				SchemaVersion: model.CurrentSchemaVersion,
				FindingID:     fmt.Sprintf("finding_%s_%d", criterionID, round),
				RunID:         input.RunID,
				CriterionID:   criterionID,
				Severity:      "high",
				Summary:       "Stable counterexample found",
				ReproSteps:    []string{"Replay failed task and inspect evidence bundle"},
				EvidenceRefs:  safeEvidenceRefs(coverage[criterionID]),
			})
		}

		if !passFound && !failFound {
			missingProof = append(missingProof, criterionID)
		}
	}

	if len(failedCriteria) > 0 {
		verdict := &model.Verdict{
			SchemaVersion: model.CurrentSchemaVersion,
			VerdictID:     fmt.Sprintf("verdict_fail_%d", time.Now().UTC().UnixNano()),
			RunID:         input.RunID,
			Status:        model.VerdictFail,
			Reasons:       []string{"At least one acceptance criterion has stable failure evidence"},
			Coverage:      ensureCoverage(input.FeatureSpec.AcceptanceCriteria, coverage),
		}
		return Output{
			Verdict:  verdict,
			Findings: findings,
		}
	}

	if hasBlockedRuns || len(input.FeatureSpec.OpenQuestions) > 0 {
		verdict := &model.Verdict{
			SchemaVersion: model.CurrentSchemaVersion,
			VerdictID:     fmt.Sprintf("verdict_cannot_verify_%d", time.Now().UTC().UnixNano()),
			RunID:         input.RunID,
			Status:        model.VerdictCannotVerify,
			Reasons:       []string{"Run blocked or specification remains ambiguous"},
			Coverage:      ensureCoverage(input.FeatureSpec.AcceptanceCriteria, coverage),
		}
		return Output{
			Verdict: verdict,
		}
	}

	if len(missingProof) == 0 && !pendingHighPriority {
		verdict := &model.Verdict{
			SchemaVersion: model.CurrentSchemaVersion,
			VerdictID:     fmt.Sprintf("verdict_pass_%d", time.Now().UTC().UnixNano()),
			RunID:         input.RunID,
			Status:        model.VerdictPass,
			Reasons:       []string{"All acceptance criteria have proof evidence and no pending P0/P1 tasks"},
			Coverage:      ensureCoverage(input.FeatureSpec.AcceptanceCriteria, coverage),
		}
		return Output{
			Verdict: verdict,
		}
	}

	nextTasks := make([]model.Task, 0, len(missingProof))
	for idx, criterionID := range missingProof {
		nextTasks = append(nextTasks, model.Task{
			SchemaVersion: model.CurrentSchemaVersion,
			TaskID:        fmt.Sprintf("judge_task_%d_%d", round, idx+1),
			RunID:         input.RunID,
			Surface:       firstSurface(input.FeatureSpec.Surfaces),
			Kind:          model.TaskKindCounterexample,
			Priority:      model.PriorityP1,
			Status:        model.TaskStatusQueued,
			DedupeKey:     fmt.Sprintf("judge:%s:%d", criterionID, round),
			MaxAttempts:   2,
			CreatedBy:     "judge",
			AcceptanceCriteriaIDs: []string{
				criterionID,
			},
			Payload: map[string]any{
				"target_criterion_id": criterionID,
				"round":               round,
				"goal":                "seek counterexample or missing proof",
			},
		})
	}

	return Output{
		NextTasks: nextTasks,
	}
}

func firstSurface(surfaces []model.Surface) model.Surface {
	if len(surfaces) == 0 {
		return model.SurfaceWeb
	}
	return surfaces[0]
}

func ensureCoverage(criteria []model.AcceptanceCriterion, coverage map[string][]string) map[string][]string {
	out := map[string][]string{}
	for _, criterion := range criteria {
		refs := coverage[criterion.ID]
		if len(refs) == 0 {
			out[criterion.ID] = []string{"missing"}
			continue
		}
		out[criterion.ID] = safeEvidenceRefs(refs)
	}
	return out
}

func safeEvidenceRefs(refs []string) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		trimmed := strings.TrimSpace(ref)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return []string{"missing"}
	}
	return out
}
