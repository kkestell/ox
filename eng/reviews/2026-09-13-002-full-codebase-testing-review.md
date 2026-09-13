# Full-codebase testing review

- **Scope:** the complete Go codebase: the shipped `ox` process, every
  `internal/` package, `integration/`, and the evaluation runner and command.
  Existing `eval-results/` artifacts and versioned evaluation workspaces were
  treated as fixtures rather than implementation.
- **Mode:** specific-topic review.
- **Topic:** testing.
- **Baseline:** working tree at `c6b0813`, including the existing uncommitted
  changes present during the review.

## Findings

### High: obsolete cross-loop serialization test makes the required gate flaky

**Location:** `internal/agent/agent_test.go:549-604`

`TestExclusiveToolSerializesAcrossConcurrentBatches` still requires two
concurrent tool loops sharing a session to serialize one another. That contract
was deliberately removed by `d525b58` so that a blocked child cannot stall the
primary agent or a sibling. The current product contract says unsafe tools are
serialized within one loop's batch, while primary and child loops run
independently (`docs/spec.md:59-61`, `eng/architecture.md:213-218`).

The test now races scheduling against its obsolete expectation. A focused 20-run
repetition failed 5 times at line 602 because the second call started early; a
single `make check` happened to pass. This makes the project's required gate
nondeterministic and rejects correct behavior.

**Suggested fix:** remove this cross-batch serialization assertion or replace it
with an assertion that independent loops may make progress concurrently. Keep
`TestExclusiveToolFencesParallelGroups` for the still-supported within-loop
fence, and keep the integration scenario that proves a blocked child's shell
does not stall another child.

### Medium: `session/resume` is not exercised through the shipped process

**Location:** `internal/agent/agent.go:616-634`, `internal/e2e/session_test.go`

The shipped-binary suite thoroughly covers `session/load`, including replay and
permission recovery, but never sends `session/resume`. Direct agent tests cover
capability and model-setting activation, and an in-process integration test
proves that a resumed session can accept another prompt. No test proves the
ACP-visible distinction that resume emits no historical replay, and no test at
any boundary reaches the explicit refusal of a session with a pending permission
request (`internal/agent/agent.go:682-692`).

A regression that makes resume replay like load, mishandles its response, or
accepts a suspended permission turn can therefore pass the current suite.

**Suggested fix:** add a process-restart e2e test that creates history, closes
the session, calls `session/resume`, asserts that no historical `session/update`
notifications arrive, checks returned configuration options, and verifies the
next provider request still contains the durable history. Add a second case that
crashes during a permission wait and asserts that resume rejects it while load
remains the recovery path.

### Low: two of five language tools stop below the ACP boundary

**Location:** `internal/e2e/lsp_test.go:60-120`

The shipped-binary language test invokes document symbols, definition, and
diagnostics. It only checks that `lsp_workspace_symbols` is advertised and does
not invoke it; it does not mention `lsp_references` at all. Both operations have
good tool-unit and manager-protocol coverage, but their complete path from a
model tool call through the active session and real LSP subprocess is absent.

A registration, argument-forwarding, or rendered-result regression in either
ACP-visible tool can pass `make check` despite every lower-level test remaining
green.

**Suggested fix:** extend the existing language-server fixture with
`textDocument/references` and `workspace/symbol` responses, invoke both tools
through the binary, and assert that their rendered results reach the following
provider requests.

### Low: successful multi-child coordination is not proven through the binary

**Location:** `internal/e2e/subagent_test.go:10-50`,
`integration/agent_loop_test.go:129-282`

The sole subagent e2e test proves that parent completion cancels one live child
and that child tool scope is restricted. Successful starts, child reports,
parent-to-child messages, waits, final results, and two-child concurrency are
covered only by the in-process integration harness.

The distinction matters because these behaviors cross provider multiplexing, ACP
tool-update streaming, and process shutdown wiring that the integration harness
does not instantiate. A shipped-process regression in the successful
coordination path can pass while the cancellation-only e2e remains green.

**Suggested fix:** port the existing two-child routed-model scenario to the e2e
harness, preserving its channel-based handshakes. Assert both children run,
messages and reports reach the parent, final results are returned, and one
blocked child does not prevent the other from completing.

### Low: the real relative evaluation-output path has no regression test

**Location:** `evals/internal/eval/runner.go:116-120`,
`evals/cmd/ox-eval/main.go:21`

The runner now resolves `Config.OutputDir` to an absolute path before it starts
Ox in a separate workspace. That protects the CLI's default relative
`eval-results` path from being reinterpreted under a repetition workspace. Every
runner and fake-provider smoke test supplies an output path rooted at
`t.TempDir()`, so none exercises the real relative-path case this normalization
serves. The evaluation command itself has no test file.

A future removal or reordering of the normalization can strand private logs,
credentials, traces, or result files under the wrong working directory without
failing `make test-eval`.

**Suggested fix:** add a fake-provider smoke case with a relative output
directory under a controlled temporary current directory. Assert that
`index.json`, repetition results, and private artifacts all appear under the
caller's resolved output root and that the spawned Ox process completes.

## Coverage assessment

The suite maps the rest of the major behavior at an appropriate boundary:
session creation/load/close/delete and durable replay, prompt streaming and
cancellation, permission recovery, executor conformance, MCP transports and
bounds, web access, language-manager protocol behavior, credentials, settings,
skills, tracing, workspace confinement, and the evaluation runner's main success
and failure paths. Tests generally use deterministic model queues, named routes,
channel handshakes, fresh processes, and the repository's SSE builders.
Assertions usually check both the protocol result and the downstream provider
conversation rather than merely accepting no error.

The coverage run excluding the obsolete flaky test passed all packages. Raw
in-process coverage ranged from 18.7% for `cmd/ox` and 38.3% for the evaluation
runner to 98.7% for `internal/shellrules`; the low command percentages are
expected because shipped-process e2e coverage is recorded in child processes,
not the parent coverage profile. The uncovered boundary behaviors above were
identified by tracing product contracts to unit, integration, and e2e tests, not
by treating line coverage as the goal.

## Checks run

- `make check` — passed once; not a reliable result because the focused stress
  run below reproduces a suite flake.
- `go test -count=20 ./internal/agent -run
  '^TestExclusiveToolSerializesAcrossConcurrentBatches$'`
  — failed 5 of 20 executions at `internal/agent/agent_test.go:602`.
- `go test -count=1 -skip
  '^TestExclusiveToolSerializesAcrossConcurrentBatches$' -coverprofile=... ./...`
  — passed all packages.
- `make test-eval` — passed.

## Unresolved suspicions

None. The findings above are confirmed by direct test mapping or a reproduced
failure.
