package stability

import (
	"testing"

	"qa-agent/internal/model"
)

func TestClassifySequences(t *testing.T) {
	cases := []struct {
		name     string
		history  []model.RunOutcome
		expected Outcome
	}{
		{
			name: "stable pass with consecutive confirmations",
			history: []model.RunOutcome{
				model.RunOutcomePass,
				model.RunOutcomePass,
			},
			expected: OutcomeStablePass,
		},
		{
			name: "stable fail with one repro",
			history: []model.RunOutcome{
				model.RunOutcomeFail,
			},
			expected: OutcomeStableFail,
		},
		{
			name: "flaky with mixed pass and fail",
			history: []model.RunOutcome{
				model.RunOutcomePass,
				model.RunOutcomeFail,
			},
			expected: OutcomeFlaky,
		},
		{
			name: "inconclusive with single pass",
			history: []model.RunOutcome{
				model.RunOutcomePass,
			},
			expected: OutcomeInconclusive,
		},
		{
			name: "blocked when only blocked outcomes",
			history: []model.RunOutcome{
				model.RunOutcomeBlocked,
			},
			expected: OutcomeBlocked,
		},
		{
			name: "error for runner errors",
			history: []model.RunOutcome{
				model.RunOutcomeError,
			},
			expected: OutcomeError,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := Classify(testCase.history, 2)
			if actual != testCase.expected {
				t.Fatalf("Classify() = %s, want %s", actual, testCase.expected)
			}
		})
	}
}

func TestDecideRetryStopsAtBudgets(t *testing.T) {
	policy := NewPolicy(Policy{
		Budget: Budget{
			PerAssertion: 2,
			PerTask:      3,
			PerRun:       10,
			Global:       100,
		},
		RequireConsecutivePass: 2,
	})

	history := []model.RunOutcome{model.RunOutcomePass}
	decision, err := policy.Decide(history, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if !decision.Retry {
		t.Fatal("Decide() expected retry while pass stability not yet proven")
	}
	if decision.Final != OutcomeInconclusive {
		t.Fatalf("Decide().Final = %s, want %s", decision.Final, OutcomeInconclusive)
	}

	history = []model.RunOutcome{model.RunOutcomePass, model.RunOutcomePass}
	decision, err = policy.Decide(history, 2, 2, 2, 2)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Retry {
		t.Fatal("Decide() expected stop at budget limit")
	}
	if decision.Final != OutcomeStablePass {
		t.Fatalf("Decide().Final = %s, want %s", decision.Final, OutcomeStablePass)
	}
}

func TestDecideFlakyRetriesUntilLimit(t *testing.T) {
	policy := NewPolicy(Policy{
		Budget: Budget{
			PerAssertion: 3,
			PerTask:      4,
			PerRun:       10,
			Global:       100,
		},
		RequireConsecutivePass: 2,
	})

	history := []model.RunOutcome{
		model.RunOutcomePass,
		model.RunOutcomeFail,
	}
	decision, err := policy.Decide(history, 1, 1, 1, 1)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if !decision.Retry {
		t.Fatal("Decide() expected retry for flaky sequence under budget")
	}

	decision, err = policy.Decide(history, 4, 4, 4, 4)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Retry {
		t.Fatal("Decide() expected stop when budgets exhausted")
	}
	if decision.Final != OutcomeFlaky {
		t.Fatalf("Decide().Final = %s, want %s", decision.Final, OutcomeFlaky)
	}
}
