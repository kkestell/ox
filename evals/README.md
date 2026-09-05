# Ox evaluations

`ox-eval` is an evaluation-only ACP client. It starts a built `ox` executable,
runs a task from `tasks/v1`, verifies the resulting workspace, and writes an
index plus one artifact directory per repetition. Each run has fresh workspace,
home, configuration, cache, and data directories.

Run the deterministic fake-provider lifecycle check with:

```sh
make test-eval
```

Live runs are never part of `make check`. After explicitly deciding to spend a
provider budget, run one task with the repository-mandated model:

```sh
make eval-live TASK=evals/tasks/v1/edit
```

The live target loads `OPENROUTER_API_KEY` from `.env` in the evaluation client,
writes it to a private temporary credential file, and passes only that file's
path to Ox. Results default to `eval-results/`; set `OUTPUT` to retain them
elsewhere and `REPETITIONS` to request fresh repeated trials.

## Task format

Each versioned task directory contains `task.json` and an optional `workspace/`
seed. The manifest declares ordered prompt or restart phases, the permission
policy for each prompt, a wall-clock and provider-request budget, and objective
success criteria. Success may require exact file contents, an argument-vector
command, or a minimum number of rejected permission requests. A verifier overlay
is copied into the workspace only after Ox exits so the agent cannot replace
verifier-owned files.

The task revision is a digest of the complete task directory. Run records also
contain the Ox revision, candidate label, evaluated binary digest, model and
provider labels, repetitions, budget, latency, provider attempts and retries,
failed edit attempts, stop reasons, permission and rejection counts, failure
classification, and ACP usage or cost when supplied. Missing usage remains
`null`. Provider authorization headers are never written to artifacts.

A `mutate` phase copies a task-owned overlay into the fresh workspace between
prompts. This is used only for deterministic stale-read evaluations; the overlay
remains outside the agent workspace until the phase runs.

## Edit comparison

The versioned edit corpus is under `tasks/edit-v1`. Run every task three times
through separately built exact and anchored binaries, passing `-candidate
exact`
or `-candidate anchored` and keeping each task's artifacts in a distinct
directory. Once both artifact trees are complete, produce the decision record
with:

```sh
go run ./evals/cmd/ox-eval \
  -compare-exact eval-results/edit-v1/exact \
  -compare-anchored eval-results/edit-v1/anchored \
  -anchored-safety-passed \
  -comparison-out evals/results/edit-v1/comparison.json
```

The report rejects mismatched tasks, prompts, models, providers, budgets,
binaries, or repetition counts. Anchored editing is selected only when it has at
least a five-point absolute success advantage, no more than ten percent higher
median total-token use, complete usage, and passing safety checks.
