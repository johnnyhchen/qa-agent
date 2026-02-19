package stability

import (
	"errors"

	"qa-agent/internal/model"
)

type Outcome string

const (
	OutcomeStablePass   Outcome = "stable_pass"
	OutcomeStableFail   Outcome = "stable_fail"
	OutcomeFlaky        Outcome = "flaky"
	OutcomeInconclusive Outcome = "inconclusive"
	OutcomeBlocked      Outcome = "blocked"
	OutcomeError        Outcome = "error"
)

type Budget struct {
	PerAssertion int
	PerTask      int
	PerRun       int
	Global       int
}

type Policy struct {
	Budget                 Budget
	RequireConsecutivePass int
}

type Decision struct {
	Retry bool
	Final Outcome
}

func NewPolicy(policy Policy) Policy {
	if policy.Budget.PerAssertion <= 0 {
		policy.Budget.PerAssertion = 3
	}
	if policy.Budget.PerTask <= 0 {
		policy.Budget.PerTask = 5
	}
	if policy.Budget.PerRun <= 0 {
		policy.Budget.PerRun = 200
	}
	if policy.Budget.Global <= 0 {
		policy.Budget.Global = 1000
	}
	if policy.RequireConsecutivePass <= 0 {
		policy.RequireConsecutivePass = 2
	}
	return policy
}

func (p Policy) Decide(history []model.RunOutcome, taskAttempts, assertionAttempts, runAttempts, globalAttempts int) (Decision, error) {
	p = NewPolicy(p)
	if len(history) == 0 {
		return Decision{}, errors.New("history is required")
	}

	if taskAttempts >= p.Budget.PerTask || assertionAttempts >= p.Budget.PerAssertion || runAttempts >= p.Budget.PerRun || globalAttempts >= p.Budget.Global {
		return Decision{Retry: false, Final: Classify(history, p.RequireConsecutivePass)}, nil
	}

	current := Classify(history, p.RequireConsecutivePass)
	switch current {
	case OutcomeStablePass, OutcomeStableFail, OutcomeBlocked:
		return Decision{Retry: false, Final: current}, nil
	case OutcomeFlaky, OutcomeInconclusive, OutcomeError:
		return Decision{Retry: true, Final: current}, nil
	default:
		return Decision{Retry: true, Final: current}, nil
	}
}

func Classify(history []model.RunOutcome, requiredConsecutivePasses int) Outcome {
	if requiredConsecutivePasses <= 0 {
		requiredConsecutivePasses = 2
	}
	if len(history) == 0 {
		return OutcomeError
	}

	hasPass := false
	hasFail := false
	hasBlocked := false
	hasError := false
	consecutivePasses := 0

	for _, result := range history {
		switch result {
		case model.RunOutcomePass:
			hasPass = true
			consecutivePasses++
		case model.RunOutcomeFail:
			hasFail = true
			consecutivePasses = 0
		case model.RunOutcomeBlocked:
			hasBlocked = true
			consecutivePasses = 0
		case model.RunOutcomeError, model.RunOutcomeFlaky:
			hasError = true
			consecutivePasses = 0
		default:
			hasError = true
			consecutivePasses = 0
		}
	}

	if hasBlocked && !hasPass && !hasFail {
		return OutcomeBlocked
	}
	if hasFail && !hasPass {
		return OutcomeStableFail
	}
	if hasPass && !hasFail && !hasError && consecutivePasses >= requiredConsecutivePasses {
		return OutcomeStablePass
	}
	if hasPass && hasFail {
		return OutcomeFlaky
	}
	if hasError {
		return OutcomeError
	}
	if hasPass {
		return OutcomeInconclusive
	}
	return OutcomeError
}
