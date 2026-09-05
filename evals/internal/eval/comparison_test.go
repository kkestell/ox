package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareSelectsAnchoredAtInclusiveThresholds(t *testing.T) {
	exactRoot := t.TempDir()
	anchoredRoot := t.TempDir()
	writeComparisonEvidence(t, exactRoot, CandidateExact, evidenceOptions{successes: 45, tokens: 100})
	writeComparisonEvidence(t, anchoredRoot, CandidateAnchored, evidenceOptions{successes: 50, tokens: 110})
	output := filepath.Join(t.TempDir(), "comparison.json")
	report, err := Compare(ComparisonConfig{
		ExactRoot: exactRoot, AnchoredRoot: anchoredRoot,
		AnchoredSafetyPass: true, OutputPath: output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != CandidateAnchored || !report.UsageComplete || report.TaskCount != 30 || len(report.Runs) != 180 {
		t.Fatalf("report = %#v", report)
	}
	if report.Candidates[0].SuccessRate != 0.5 || report.Candidates[1].MedianTotalTokens == nil || *report.Candidates[1].MedianTotalTokens != 110 {
		t.Fatalf("aggregates = %#v", report.Candidates)
	}
	if raw, err := os.ReadFile(output); err != nil || !strings.Contains(string(raw), `"decision": "anchored"`) {
		t.Fatalf("comparison artifact = %q, %v", raw, err)
	}
}

func TestCompareFallsBackToExactDeterministically(t *testing.T) {
	for name, change := range map[string]func(*evidenceOptions, *ComparisonConfig){
		"success below threshold": func(anchor *evidenceOptions, _ *ComparisonConfig) { anchor.successes = 49 },
		"tokens above threshold":  func(anchor *evidenceOptions, _ *ComparisonConfig) { anchor.tokens = 111 },
		"missing usage":           func(anchor *evidenceOptions, _ *ComparisonConfig) { anchor.missingUsage = true },
		"safety failed":           func(_ *evidenceOptions, config *ComparisonConfig) { config.AnchoredSafetyPass = false },
	} {
		t.Run(name, func(t *testing.T) {
			exactRoot := t.TempDir()
			anchoredRoot := t.TempDir()
			exact := evidenceOptions{successes: 45, tokens: 100}
			anchored := evidenceOptions{successes: 50, tokens: 110}
			config := ComparisonConfig{ExactRoot: exactRoot, AnchoredRoot: anchoredRoot, AnchoredSafetyPass: true}
			change(&anchored, &config)
			writeComparisonEvidence(t, exactRoot, CandidateExact, exact)
			writeComparisonEvidence(t, anchoredRoot, CandidateAnchored, anchored)
			report, err := Compare(config)
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != CandidateExact || len(report.DecisionReasons) == 0 {
				t.Fatalf("report = %#v", report)
			}
			if name == "missing usage" && report.UsageComplete {
				t.Fatal("missing usage was reported complete")
			}
		})
	}
}

func TestCompareRejectsIncompleteAndUnmatchedEvidence(t *testing.T) {
	for name, alter := range map[string]func(*evidenceOptions, *evidenceOptions){
		"too few tasks":     func(exact, anchored *evidenceOptions) { exact.tasks, anchored.tasks = 29, 29 },
		"incomplete runs":   func(_, anchored *evidenceOptions) { anchored.repetitions = 2 },
		"revision mismatch": func(_, anchored *evidenceOptions) { anchored.revision = "different" },
		"prompt mismatch":   func(_, anchored *evidenceOptions) { anchored.prompt = "different" },
		"budget mismatch":   func(_, anchored *evidenceOptions) { anchored.budget = Budget{TimeoutMS: 2, ProviderRequests: 1} },
		"model mismatch":    func(_, anchored *evidenceOptions) { anchored.model = "other/model" },
		"identical binary":  func(_, anchored *evidenceOptions) { anchored.binaryDigest = "sha256:exact" },
	} {
		t.Run(name, func(t *testing.T) {
			exactRoot := t.TempDir()
			anchoredRoot := t.TempDir()
			exact := evidenceOptions{successes: 45, tokens: 100}
			anchored := evidenceOptions{successes: 50, tokens: 100}
			alter(&exact, &anchored)
			writeComparisonEvidence(t, exactRoot, CandidateExact, exact)
			writeComparisonEvidence(t, anchoredRoot, CandidateAnchored, anchored)
			if _, err := Compare(ComparisonConfig{ExactRoot: exactRoot, AnchoredRoot: anchoredRoot, AnchoredSafetyPass: true}); err == nil {
				t.Fatal("invalid comparison evidence accepted")
			}
		})
	}
}

func TestAggregateIncludesRetriesAndFailureCategories(t *testing.T) {
	root := t.TempDir()
	writeComparisonEvidence(t, root, CandidateExact, evidenceOptions{
		successes: 88, tokens: 10, providerRetries: 2, failedEdits: 3,
	})
	evidence, err := loadCandidateEvidence(root, CandidateExact)
	if err != nil {
		t.Fatal(err)
	}
	aggregate := aggregateCandidate(evidence)
	if aggregate.ProviderRetries != 180 || aggregate.FailedEditAttempts != 270 || aggregate.FailureCategories["verifier"] != 2 {
		t.Fatalf("aggregate = %#v", aggregate)
	}
}

type evidenceOptions struct {
	tasks           int
	repetitions     int
	successes       int
	tokens          uint64
	missingUsage    bool
	revision        string
	prompt          string
	model           string
	binaryDigest    string
	budget          Budget
	providerRetries int
	failedEdits     int
}

func writeComparisonEvidence(t *testing.T, root, candidate string, options evidenceOptions) {
	t.Helper()
	if options.tasks == 0 {
		options.tasks = 30
	}
	if options.repetitions == 0 {
		options.repetitions = 3
	}
	if options.revision == "" {
		options.revision = "task-revision"
	}
	if options.prompt == "" {
		options.prompt = "prompt-digest"
	}
	if options.model == "" {
		options.model = "test/model"
	}
	if options.binaryDigest == "" {
		options.binaryDigest = "sha256:" + candidate
	}
	if options.budget == (Budget{}) {
		options.budget = Budget{TimeoutMS: 1000, ProviderRequests: 4}
	}
	runNumber := 0
	for taskNumber := 1; taskNumber <= options.tasks; taskNumber++ {
		id := fmt.Sprintf("task-%03d", taskNumber)
		index := RunIndex{
			Schema: ResultSchemaVersion, TaskID: id, TaskRevision: options.revision,
			Candidate: candidate, BinaryDigest: options.binaryDigest,
			PromptDigest: options.prompt, Model: options.model, Provider: "openrouter",
			Budget: options.budget, Repetitions: options.repetitions,
		}
		for repetition := 1; repetition <= options.repetitions; repetition++ {
			runNumber++
			total := options.tokens
			result := RunResult{
				Schema: index.Schema, TaskID: id, TaskRevision: index.TaskRevision,
				Candidate: candidate, BinaryDigest: index.BinaryDigest, PromptDigest: index.PromptDigest,
				Model: index.Model, Provider: index.Provider, Budget: index.Budget,
				Repetitions: options.repetitions, Repetition: repetition,
				Success: runNumber <= options.successes, UsageComplete: true, TotalTokens: &total,
				LatencyMS: int64(runNumber), ProviderRetries: options.providerRetries,
				FailedEditAttempts: options.failedEdits,
			}
			if !result.Success {
				result.Failure = &Failure{Class: "verifier", Message: "did not match"}
			}
			if options.missingUsage && runNumber == 1 {
				result.UsageComplete, result.TotalTokens = false, nil
			}
			index.Results = append(index.Results, result)
		}
		if err := writeJSON(filepath.Join(root, id, "index.json"), index); err != nil {
			t.Fatal(err)
		}
	}
}
