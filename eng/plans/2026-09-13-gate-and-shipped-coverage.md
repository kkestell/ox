# A deterministic gate and shipped-process coverage

## Goal

Make the required gate deterministic, and prove through the real binary the
paths that today stop below the process boundary.

## Desired outcome

`make check` no longer races the scheduler against behavior the implementation
is meant to permit. `session/resume`, both remaining language tools, successful
multi-child coordination, and the evaluation runner's relative output path each
fail a test when they break.

## Summary of approach

Replace the obsolete cross-loop serialization assertion with one that proves
what the contract actually says: a call blocked in one loop does not fence an
independent loop. Waiting for both executions to start makes that deterministic,
because a fence would block rather than lose a timing guess.

Add the missing shipped-process scenarios. The multi-child one needs the model
mock to answer differently depending on who is asking and on what a request
already contains, which is what the in-process integration harness gets from a
routed model. Give the mock the same three capabilities: a response restricted
to the primary agent, a response built from the request, and a repeating primary
fallback so a loop whose length depends on when children finish does not have to
be counted in advance.

## Related code

- `internal/agent/agent_test.go` - Holds the obsolete serialization assertion
  and the within-batch fence coverage that stays.
- `internal/agent/loop.go` - Partitions a batch, which is where exclusion lives.
- `internal/e2e/model_test.go` - The queued model endpoint every process test
  scripts.
- `internal/e2e/session_test.go`, `lsp_test.go`, `subagent_test.go` - Where the
  new scenarios belong.
- `integration/agent_loop_test.go` - The two-child scenario being ported.
- `evals/internal/eval/runner.go` - Resolves a relative output directory before
  starting Ox in a separate workspace.

## Current state

- `TestExclusiveToolSerializesAcrossConcurrentBatches` requires two independent
  loops to serialize. Exclusion is implemented only as batch partitioning, so
  the assertion races a scheduler against permitted behavior and fails often.
- `eng/architecture.md` still calls the group's shared state a "session-wide
  exclusion lock", contradicting its own later sentence and the specification.
- No process test sends `session/resume`, so neither its lack of replay nor its
  refusal of a pending permission wait is covered anywhere.
- The language test invokes three of five tools. `lsp_workspace_symbols` is only
  checked as advertised and `lsp_references` is absent.
- The only process-level subagent test covers cancellation.
- Every evaluation test passes an output path under `t.TempDir()`, so the
  normalization that protects the CLI's relative default is unexercised.

## Structural considerations

- **Abstraction:** The mock gains three small primitives that each name one
  thing a routed model does. It does not become a scripting language.
- **Testability:** Ordering comes from what a request contains and who sent it,
  not from sleeps or arrival races.

## Test plan

- **Key behaviors to verify:** Independent loops both run a fenced tool; resume
  returns options and replays nothing while history still reaches the model;
  resume refuses a pending permission wait and load still recovers it;
  references and workspace symbols render through the binary; two children run
  concurrently, a parent message reaches one, a child report reaches the parent,
  and both results come back; evaluation artifacts land under a relative output
  directory resolved from the caller's working directory.
- **Test levels:** One focused agent test for the exclusion boundary; the rest
  through the shipped binary and the evaluation smoke build.
- **Edge cases and failure modes:** A workspace-wide language query reaches
  every configured server and fails when one cannot start; a reference outside
  the workspace is omitted rather than reported.
- **What not to test:** The within-batch fence and the blocked-child integration
  scenario, which already hold.

## Implementation plan

- Replace the obsolete serialization test and correct the architecture's
  description of what a child shares.
- Add resume coverage, extend the language fixture and tests, and port the
  two-child coordination scenario behind the new mock primitives.
- Add the evaluation relative-output-path smoke case.

## Documentation updates

- `eng/architecture.md` drops the session-wide exclusion lock, which the
  implementation and the specification both contradict.

## Impact assessment

- Code paths affected: Tests and test harness only, plus one architecture
  sentence.
- Data, protocol, or schema impact: None.
- Dependency or API impact: None.

## Validation

- Tests to write and run: The tests above, repeated runs of the replaced
  exclusion test and the new coordination test, then `make check` and
  `make test-eval`.
