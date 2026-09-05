package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
)

const ComparisonSchemaVersion = 1

const (
	CandidateExact    = "exact"
	CandidateAnchored = "anchored"
)

type ComparisonConfig struct {
	ExactRoot          string
	AnchoredRoot       string
	AnchoredSafetyPass bool
	OutputPath         string
}

type ComparisonReport struct {
	Schema          int                  `json:"schema"`
	Model           string               `json:"model"`
	TaskCount       int                  `json:"task_count"`
	Repetitions     int                  `json:"repetitions"`
	SafetyPass      bool                 `json:"anchored_safety_pass"`
	UsageComplete   bool                 `json:"usage_complete"`
	Runs            []ComparisonRun      `json:"runs"`
	Candidates      []CandidateAggregate `json:"candidates"`
	Decision        string               `json:"decision"`
	DecisionReasons []string             `json:"decision_reasons"`
}

type ComparisonRun struct {
	Candidate          string  `json:"candidate"`
	BinaryDigest       string  `json:"binary_digest"`
	TaskID             string  `json:"task_id"`
	TaskRevision       string  `json:"task_revision"`
	Repetition         int     `json:"repetition"`
	Success            bool    `json:"success"`
	TotalTokens        *uint64 `json:"total_tokens"`
	LatencyMS          int64   `json:"latency_ms"`
	ProviderRetries    int     `json:"provider_retries"`
	FailedEditAttempts int     `json:"failed_edit_attempts"`
	FailureClass       string  `json:"failure_class,omitempty"`
}

type CandidateAggregate struct {
	Candidate          string         `json:"candidate"`
	BinaryDigest       string         `json:"binary_digest"`
	Runs               int            `json:"runs"`
	Successes          int            `json:"successes"`
	SuccessRate        float64        `json:"success_rate"`
	MedianTotalTokens  *float64       `json:"median_total_tokens"`
	MedianLatencyMS    float64        `json:"median_latency_ms"`
	ProviderRetries    int            `json:"provider_retries"`
	FailedEditAttempts int            `json:"failed_edit_attempts"`
	FailureCategories  map[string]int `json:"failure_categories"`
}

func Compare(config ComparisonConfig) (ComparisonReport, error) {
	if config.ExactRoot == "" || config.AnchoredRoot == "" {
		return ComparisonReport{}, errors.New("exact and anchored artifact roots are required")
	}
	exact, err := loadCandidateEvidence(config.ExactRoot, CandidateExact)
	if err != nil {
		return ComparisonReport{}, fmt.Errorf("exact evidence: %w", err)
	}
	anchored, err := loadCandidateEvidence(config.AnchoredRoot, CandidateAnchored)
	if err != nil {
		return ComparisonReport{}, fmt.Errorf("anchored evidence: %w", err)
	}
	if err := matchEvidence(exact, anchored); err != nil {
		return ComparisonReport{}, err
	}

	runs := append(comparisonRuns(exact), comparisonRuns(anchored)...)
	exactAggregate := aggregateCandidate(exact)
	anchoredAggregate := aggregateCandidate(anchored)
	usageComplete := exactAggregate.MedianTotalTokens != nil && anchoredAggregate.MedianTotalTokens != nil
	report := ComparisonReport{
		Schema: ComparisonSchemaVersion, Model: exact.model, TaskCount: len(exact.indexes),
		Repetitions: 3, SafetyPass: config.AnchoredSafetyPass, UsageComplete: usageComplete,
		Runs: runs, Candidates: []CandidateAggregate{exactAggregate, anchoredAggregate},
		Decision: CandidateExact,
	}
	if !successThreshold(exactAggregate, anchoredAggregate) {
		report.DecisionReasons = append(report.DecisionReasons, "anchored success rate improved by less than five percentage points")
	}
	if !usageComplete {
		report.DecisionReasons = append(report.DecisionReasons, "token usage is incomplete")
	} else if *anchoredAggregate.MedianTotalTokens > *exactAggregate.MedianTotalTokens*1.10 {
		report.DecisionReasons = append(report.DecisionReasons, "anchored median total tokens increased by more than ten percent")
	}
	if !config.AnchoredSafetyPass {
		report.DecisionReasons = append(report.DecisionReasons, "anchored safety tests did not pass")
	}
	if len(report.DecisionReasons) == 0 {
		report.Decision = CandidateAnchored
		report.DecisionReasons = []string{"anchored editing met every adoption threshold"}
	}
	if config.OutputPath != "" {
		if err := writeJSON(config.OutputPath, report); err != nil {
			return ComparisonReport{}, fmt.Errorf("write comparison report: %w", err)
		}
	}
	return report, nil
}

type candidateEvidence struct {
	candidate    string
	binaryDigest string
	model        string
	indexes      map[string]RunIndex
}

func loadCandidateEvidence(root, wantCandidate string) (candidateEvidence, error) {
	evidence := candidateEvidence{candidate: wantCandidate, indexes: make(map[string]RunIndex)}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "index.json" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var index RunIndex
		if err := json.Unmarshal(raw, &index); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		if err := validateRunIndex(index, wantCandidate); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if _, exists := evidence.indexes[index.TaskID]; exists {
			return fmt.Errorf("duplicate task %q", index.TaskID)
		}
		if evidence.binaryDigest == "" {
			evidence.binaryDigest, evidence.model = index.BinaryDigest, index.Model
		} else if evidence.binaryDigest != index.BinaryDigest {
			return errors.New("candidate uses more than one binary digest")
		} else if evidence.model != index.Model {
			return errors.New("candidate uses more than one model")
		}
		evidence.indexes[index.TaskID] = index
		return nil
	})
	if err != nil {
		return candidateEvidence{}, err
	}
	if len(evidence.indexes) < 30 {
		return candidateEvidence{}, fmt.Errorf("found %d tasks, want at least 30", len(evidence.indexes))
	}
	return evidence, nil
}

func validateRunIndex(index RunIndex, candidate string) error {
	if index.Schema != ResultSchemaVersion {
		return fmt.Errorf("result schema = %d, want %d", index.Schema, ResultSchemaVersion)
	}
	if index.Candidate != candidate {
		return fmt.Errorf("candidate = %q, want %q", index.Candidate, candidate)
	}
	if index.TaskID == "" || index.TaskRevision == "" || index.PromptDigest == "" || index.BinaryDigest == "" || index.Model == "" {
		return errors.New("missing task, prompt, binary, or model identity")
	}
	if index.Repetitions != 3 || len(index.Results) != 3 {
		return fmt.Errorf("task %q has %d/%d repetitions, want 3", index.TaskID, len(index.Results), index.Repetitions)
	}
	seen := make(map[int]bool)
	for _, result := range index.Results {
		if result.Schema != index.Schema || result.TaskID != index.TaskID || result.TaskRevision != index.TaskRevision ||
			result.Candidate != index.Candidate || result.BinaryDigest != index.BinaryDigest || result.PromptDigest != index.PromptDigest ||
			result.Model != index.Model || result.Provider != index.Provider || !reflect.DeepEqual(result.Budget, index.Budget) || result.Repetitions != 3 {
			return fmt.Errorf("task %q contains a run with mismatched identity", index.TaskID)
		}
		if result.Repetition < 1 || result.Repetition > 3 || seen[result.Repetition] {
			return fmt.Errorf("task %q has invalid repetition %d", index.TaskID, result.Repetition)
		}
		seen[result.Repetition] = true
		if result.UsageComplete && result.TotalTokens == nil {
			return fmt.Errorf("task %q repetition %d claims complete usage without total tokens", index.TaskID, result.Repetition)
		}
		if result.Success == (result.Failure != nil) {
			return fmt.Errorf("task %q repetition %d has an inconsistent success outcome", index.TaskID, result.Repetition)
		}
		if result.LatencyMS < 0 || result.ProviderRetries < 0 || result.FailedEditAttempts < 0 {
			return fmt.Errorf("task %q repetition %d has a negative metric", index.TaskID, result.Repetition)
		}
	}
	return nil
}

func matchEvidence(exact, anchored candidateEvidence) error {
	if exact.model != anchored.model {
		return fmt.Errorf("model mismatch: exact %q, anchored %q", exact.model, anchored.model)
	}
	if len(exact.indexes) != len(anchored.indexes) {
		return errors.New("candidate task sets do not match")
	}
	for id, left := range exact.indexes {
		right, ok := anchored.indexes[id]
		if !ok {
			return fmt.Errorf("anchored evidence is missing task %q", id)
		}
		if left.TaskRevision != right.TaskRevision || left.PromptDigest != right.PromptDigest ||
			!reflect.DeepEqual(left.Budget, right.Budget) || left.Provider != right.Provider {
			return fmt.Errorf("task %q has unmatched revision, prompt, budget, or provider", id)
		}
	}
	return nil
}

func comparisonRuns(evidence candidateEvidence) []ComparisonRun {
	ids := make([]string, 0, len(evidence.indexes))
	for id := range evidence.indexes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var runs []ComparisonRun
	for _, id := range ids {
		results := append([]RunResult(nil), evidence.indexes[id].Results...)
		sort.Slice(results, func(i, j int) bool { return results[i].Repetition < results[j].Repetition })
		for _, result := range results {
			failureClass := ""
			totalTokens := result.TotalTokens
			if !result.UsageComplete {
				totalTokens = nil
			}
			if result.Failure != nil {
				failureClass = result.Failure.Class
			} else if !result.Success {
				failureClass = "unknown"
			}
			runs = append(runs, ComparisonRun{
				Candidate: evidence.candidate, BinaryDigest: evidence.binaryDigest,
				TaskID: id, TaskRevision: result.TaskRevision, Repetition: result.Repetition,
				Success: result.Success, TotalTokens: totalTokens, LatencyMS: result.LatencyMS,
				ProviderRetries: result.ProviderRetries, FailedEditAttempts: result.FailedEditAttempts,
				FailureClass: failureClass,
			})
		}
	}
	return runs
}

func aggregateCandidate(evidence candidateEvidence) CandidateAggregate {
	aggregate := CandidateAggregate{
		Candidate: evidence.candidate, BinaryDigest: evidence.binaryDigest,
		FailureCategories: make(map[string]int),
	}
	var tokens []uint64
	var latencies []int64
	usageComplete := true
	for _, run := range comparisonRuns(evidence) {
		aggregate.Runs++
		if run.Success {
			aggregate.Successes++
		} else {
			aggregate.FailureCategories[run.FailureClass]++
		}
		aggregate.ProviderRetries += run.ProviderRetries
		aggregate.FailedEditAttempts += run.FailedEditAttempts
		latencies = append(latencies, run.LatencyMS)
		if run.TotalTokens == nil {
			usageComplete = false
		} else {
			tokens = append(tokens, *run.TotalTokens)
		}
	}
	if aggregate.Runs > 0 {
		aggregate.SuccessRate = float64(aggregate.Successes) / float64(aggregate.Runs)
	}
	aggregate.MedianLatencyMS = medianInt64(latencies)
	if usageComplete {
		median := medianUint64(tokens)
		aggregate.MedianTotalTokens = &median
	}
	return aggregate
}

func successThreshold(exact, anchored CandidateAggregate) bool {
	if exact.Runs == 0 || exact.Runs != anchored.Runs {
		return false
	}
	return (anchored.Successes-exact.Successes)*100 >= exact.Runs*5
}

func medianUint64(values []uint64) float64 {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return float64(values[middle])
	}
	return float64(values[middle-1])/2 + float64(values[middle])/2
}

func medianInt64(values []int64) float64 {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return float64(values[middle])
	}
	return float64(values[middle-1])/2 + float64(values[middle])/2
}
