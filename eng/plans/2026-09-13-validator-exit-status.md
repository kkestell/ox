# Preserve validator exit status

## Goal

Models bound validation output with their own pipelines:
`make check 2>&1 |
tail -40`, `go test ./... 2>&1 | grep -v ok`. Ox runs the
command through `/bin/sh -c` and reports the command line's exit status, which a
trailing pipeline replaces with the status of `tail` or `grep`. A failing
validator can therefore report `exit code: 0`, and the model may claim a gate
passed.

## Desired outcome

The model-facing shell guidance says output is already bounded and spilled,
tells the model to run validators directly, and warns that piping through
`head`, `tail`, or `grep` hides the validator's status. Deterministic coverage
proves a noisy failing command keeps its own nonzero status with its complete
output retrievable from the spill, and a live evaluation task requires a model
to report both the status and the final output line of a noisy failing
validator.

## Summary of approach

Extend the shell sentence in `sharedToolProse` with the direct-run rule and the
pipeline warning; the shell tool's behavior and schema do not change. Pin the
wording in `prompt_test.go`. Add a shell tool test for a command whose output
exceeds the inline preview, fails, and therefore reports its own nonzero status
with every line retained in the spill. Add a versioned evaluation task whose
fixture validator is noisy and failing, and extend the corpus test to load it
and prove the fixture really is loud and failing.

No shell option is added. The existing recorder already bounds inline output at
200 lines or 50 KiB and keeps accepted overflow in a spill file, so the model
never needs to truncate a command itself. Guidance plus measurement is the whole
change; an output-view option would be new configuration surface without
evidence that instructions are insufficient.

## Related code

- `internal/agent/prompt.go` - `sharedToolProse`, the shared shell sentence.
- `internal/agent/prompt_test.go` - composed-prompt fragment assertions.
- `internal/tools/tools_test.go` - shell result contract at the tool boundary.
- `internal/workspace/stream.go` - the inline bound and spill preview the
  guidance depends on.
- `evals/tasks/v1/` - versioned evaluation tasks and their fixtures.
- `evals/internal/eval/corpus_test.go` - task loading and fixture assertions.

## Current state

- `executeLocalShell` runs `/bin/sh -c` and reports `exit code: <status>` for
  the command line; it does not inspect pipelines.
- `workspace.StreamRecorder` keeps output inline up to `InlineMaxLines` (200) or
  `InlineMaxBytes` (50 KiB), then preserves a bounded head and tail plus the
  full spill path.
- `sharedToolProse` explains that shell output combines streams, spills, and is
  readable from the reported path, but does not connect that to running
  validators directly.
- Tool tests cover a small failing command and a large succeeding command
  separately; no test covers a large failing one, and no evaluation task
  exercises a noisy failing validator.

## Structural considerations

- **Hierarchy:** the change stays inside the agent's prompt composition and the
  evaluation-only tree. Neither the shell tool nor the workspace boundary gains
  responsibility.
- **Abstraction:** no new type, option, or helper. The guidance is one more
  sentence in the shared prose block that already owns shell behavior.
- **Modularization:** the evaluation fixture lives with the task it serves.
- **Encapsulation:** the fixture is data; the corpus test asserts its observable
  properties rather than coupling to its internals.
- **Testability:** the prompt contract is asserted at `composePrompt`, the shell
  contract at the tool boundary, and the fixture in the corpus test. The live
  task measures model behavior outside the required gate.

## Test plan

- **Composed prompt states the rule:** `composePrompt` output runs validators
  directly and names `head`, `tail`, and `grep` as the pipeline stages that hide
  a validator's status.
- **Noisy failure keeps its status:** a shell command that writes far past the
  inline bound and exits nonzero reports `exit code: 3` and points at a spill
  that holds every line, including the final failure line.
- **The evaluation fixture discriminates:** the corpus test loads the new task
  and runs its validator, asserting a nonzero exit status and more output lines
  than the inline preview keeps.
- **What not to test:** `/bin/sh` pipeline semantics and the model's live choice
  of command; those are language and model behavior, not Ox behavior.

## Implementation plan

- Extend the shell sentence in `sharedToolProse` and assert its fragments in
  `prompt_test.go`.
- Add the noisy failing-command shell test beside the existing spill coverage.
- Add the `noisy-validator` evaluation task with its fixture and expected
  report, then extend the corpus test with the task ID and fixture assertions.
- Run the focused tests and `make check`.
- Run one completeness and simplification review, address its findings, and
  complete the finding.

## Documentation updates

- `eng/todo.md` marks the finding complete.
- No `docs/spec.md` or `eng/architecture.md` change: product behavior and
  boundaries are unchanged, and model guidance lives with the prompt it
  composes.

## Impact assessment

- **Code paths affected:** system prompt composition only.
- **Data, protocol, or schema impact:** none. The shell tool schema, result
  format, and durable state are unchanged.
- **Dependency or API impact:** no new dependency, option, or exported API.

## Validation

- `go test ./internal/agent ./internal/tools ./evals/internal/eval`
- `make check`
- A live evaluation run of the new task uses the repository's mandated model and
  remains an explicitly requested, budgeted check.
