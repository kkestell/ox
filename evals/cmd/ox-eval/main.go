package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	evaluation "github.com/kkestell/ox/evals/internal/eval"
)

const liveModel = "openai/gpt-5.6-luna"

func main() {
	var (
		oxBinary    = flag.String("ox", "./ox", "path to the built Ox executable")
		taskPath    = flag.String("task", "", "path to a versioned evaluation task")
		outputPath  = flag.String("out", "eval-results", "artifact output directory")
		model       = flag.String("model", "test/model", "model id")
		provider    = flag.String("provider", "local", "nonsecret provider label")
		providerURL = flag.String("provider-url", "", "provider API base URL")
		candidate   = flag.String("candidate", evaluation.CandidateExact, "evaluated edit candidate label")
		repetitions = flag.Int("repetitions", 1, "number of fresh runs")
		live        = flag.Bool("live", false, "affirm an on-demand real-provider run")
		exactRoot   = flag.String("compare-exact", "", "exact-candidate artifact tree")
		anchorRoot  = flag.String("compare-anchored", "", "anchored-candidate artifact tree")
		reportPath  = flag.String("comparison-out", "", "comparison report output path")
		safetyPass  = flag.Bool("anchored-safety-passed", false, "record that all anchored safety tests passed")
	)
	flag.Parse()
	if *exactRoot != "" || *anchorRoot != "" {
		if *exactRoot == "" || *anchorRoot == "" || *reportPath == "" {
			fail("-compare-exact, -compare-anchored, and -comparison-out are required together")
		}
		report, err := evaluation.Compare(evaluation.ComparisonConfig{
			ExactRoot: *exactRoot, AnchoredRoot: *anchorRoot,
			AnchoredSafetyPass: *safetyPass, OutputPath: *reportPath,
		})
		if err != nil {
			fail(err.Error())
		}
		fmt.Printf("edit comparison: selected %s; report: %s\n", report.Decision, *reportPath)
		return
	}
	if *taskPath == "" {
		fail("-task is required")
	}
	credential := os.Getenv("OPENROUTER_API_KEY")
	if *live {
		if *model != liveModel {
			fail("live evaluations must use " + liveModel)
		}
		if *providerURL == "" {
			*providerURL = "https://openrouter.ai/api/v1"
		}
		*provider = "openrouter"
		if credential == "" {
			fail("OPENROUTER_API_KEY is required for a live evaluation")
		}
	} else if *providerURL == "" {
		fail("-provider-url is required unless -live is set")
	}
	revision := gitRevision()
	index, err := evaluation.Run(context.Background(), evaluation.Config{
		OxBinary: *oxBinary, OxRevision: revision, Candidate: *candidate, TaskPath: *taskPath,
		OutputDir: *outputPath, Model: *model, Provider: *provider,
		ProviderURL: *providerURL, Credential: credential, Repetitions: *repetitions,
		AllowRemote: *live,
	})
	if err != nil {
		fail(err.Error())
	}
	failed := 0
	for _, result := range index.Results {
		if !result.Success {
			failed++
		}
	}
	fmt.Printf("%s: %d/%d passed; artifacts: %s\n", index.TaskID, len(index.Results)-failed, len(index.Results), *outputPath)
	if failed != 0 {
		os.Exit(1)
	}
}

func gitRevision() string {
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir, _ = filepath.Abs(filepath.Dir("."))
	output, err := command.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "ox-eval:", message)
	os.Exit(2)
}
