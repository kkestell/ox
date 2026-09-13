# Repository rough-edges review

- **Scope:** the complete current working tree at `c6b0813`, including the
  uncommitted MCP, evaluation, documentation, and planning changes present
  during the review.
- **Mode:** general.
- **Selected topics:** correctness for turn and cancellation state; testing for
  the required gate and ACP-visible feature coverage; architecture for platform
  and ownership consistency; performance for repeated provider-boundary work;
  documentation for shipped-feature discoverability and stale repository maps.
- **Known planned work:** auto-approval mode is explicitly incomplete in
  `eng/todo.md` and is not reported as a new finding below.

## Findings

### High: the required test gate contains a test for a removed contract

**Location:** `internal/agent/agent_test.go:549-615`, `docs/spec.md:50-61`,
`eng/architecture.md:213-220`

`TestExclusiveToolSerializesAcrossConcurrentBatches` requires two independent
tool loops to serialize their exclusive calls. The current product contract says
exclusivity applies within one loop's batch and that primary and child loops run
independently. The assertion therefore races the scheduler against behavior the
implementation is meant to permit.

A 50-run focused repetition failed 26 times at line 602, although one complete
`make check` run passed. The required gate is consequently nondeterministic and
can reject the intended implementation.

**Suggested fix:** remove the obsolete cross-loop serialization assertion or
replace it with a deterministic assertion that independent loops can progress.
Keep the existing within-batch fence coverage.

### Medium: the half-present Windows port does not compile

**Location:** `internal/tools/shell.go:95-116`,
`internal/workspace/platform_test.go:1-20`,
`internal/e2e/instructions_test.go:1-12`

The repository contains Windows implementations for session locks and
credential-file security, but the local shell implementation unconditionally
uses `/bin/sh`, `SysProcAttr.Setpgid`, `syscall.Kill`, and Unix signals.
`GOOS=windows go build ./...` fails on `Setpgid` and `syscall.Kill`. A Windows
vet pass also fails because two untagged tests call `syscall.Mkfifo`.

The result is an ambiguous platform contract: meaningful Windows-specific code
is maintained, but the shipped command cannot be built for Windows and the
ordinary gate cannot reveal that on Unix hosts.

**Suggested fix:** either split shell/process-group handling and FIFO tests into
platform files and add a cross-build gate, or state that Ox is Unix-only and
remove the misleading Windows implementation surface.

### Medium: one malformed provider tool-call ID leaves the live session stuck

**Location:** `internal/agent/loop.go:241-244`, `364-391`, `418-465`

When a provider repeats a tool-call ID, `validateToolCallIDs` returns an error
directly. Unlike other failures after a prompt has been accepted, this path does
not call `finishFailed` or otherwise append a terminal turn record. The durable
state therefore retains `openTurn`; later prompts against the active session are
refused until the client closes and reloads it.

Provider output is external input, so an invalid ID should fail one turn rather
than poison the live session.

**Suggested fix:** route tool-call validation failures through `finishFailed`
and add an ACP-visible regression test that sends a second prompt successfully
after the bad completion.

### Medium: cancellation can discard text already streamed to the client

**Location:** `internal/agent/loop.go:165-200`,
`internal/openrouter/stream.go:115-150`, `docs/spec.md:46-48`

The streaming adapter emits text and reasoning immediately. On cancellation it
persists that partial exchange only when the assembled completion has zero tool
calls. A stream that emits text and then begins a tool-call fragment therefore
has a nonempty `ToolCalls` slice and skips persistence, even though the client
already saw the text. The next activation cannot reconstruct the transcript the
client observed, contrary to the specification.

**Suggested fix:** persist the completed text/reasoning/usage portion on
cancellation independently of incomplete tool-call fragments, and cover the
text-then-tool-fragment cancellation sequence end to end.

### Medium: the in-memory OpenRouter catalog never becomes stale

**Location:** `internal/openrouter/catalog.go:15-23`, `134-167`, `212-256`;
`eng/architecture.md:230-237`

`loadCatalog` applies the 24-hour freshness rule to the disk cache, but
`Client.Catalog` returns `c.catalog` forever after the first successful load. A
long-running Ox process therefore never refetches updated context windows,
supported parameters, or model availability, despite the architecture saying
that a stale catalog is refetched on the spot.

**Suggested fix:** retain the in-memory catalog's load time and re-enter the
single-flight load path once it ages out, preserving the existing stale-on-error
fallback.

### Low: every MCP call refreshes and revalidates the server's entire catalog

**Location:** `internal/mcp/mcp.go:129-150`

Before dispatching one selected tool, `Bundle.Call` paginates `tools/list` for
the whole server and rebuilds every descriptor and schema identity. This makes
each call O(total server catalog), consumes part of the same 120-second call
deadline, and lets an unrelated catalog-page failure prevent a stable selected
tool from running. The specification asks Ox to refresh the selected definition,
but MCP exposes only catalog listing, so the current correctness rule carries a
potentially large availability and latency cost.

**Suggested fix:** make this an explicit design decision. Options include a
bounded per-server freshness cache or subscribing to tool-list changes; either
requires reconciling the before-dispatch identity guarantee in `docs/spec.md`.

### Low: several shipped paths stop below the process boundary in tests

**Location:** `internal/agent/agent.go:616-692`,
`internal/e2e/lsp_test.go:60-121`, `internal/e2e/subagent_test.go:10-50`

No end-to-end test sends `session/resume`, including its no-replay behavior and
pending-permission refusal. The language-server process test invokes three of
five language tools but only checks that `lsp_workspace_symbols` is advertised
and never mentions `lsp_references`. The only shipped-process subagent test
covers parent completion cancelling a child; successful child completion,
messaging, reports, waits, and multi-child progress are proved only by the
in-process integration harness.

These features have good lower-level coverage, but dispatch, JSON-RPC wiring,
streaming updates, or process-lifecycle regressions can pass the required gate.

**Suggested fix:** add one focused shipped-binary scenario for resume, extend
the existing LSP fixture with references and workspace symbols, and port the
deterministic two-child coordination scenario from `integration/`.

### Low: repository and user documentation has visible drift

**Location:** `AGENTS.md:24-33`, `eng/review-targets.md:80-86`,
`README.md:26-51`, `docs/zed.md:50-53`

- `AGENTS.md` still says `internal/lsp` is unimported even though the completed
  todo and imports show it is active, and it still describes environment-backed
  credential storage that the shipped process intentionally removed.
- `eng/review-targets.md` points reviewers at deleted `task.go` and
  `task_queue.go` files and task-state behavior removed before subagents were
  introduced.
- The README's feature inventory omits all language tools.
- The Zed guide links to `docs/worktrees.md`, which does not exist.

These do not change runtime behavior, but they make setup fail at a dead link
and direct maintainers toward the wrong architecture and review corpus.

**Suggested fix:** update the two repository maps, add language intelligence to
the README, and either add the promised worktree guide or remove the link.

### Low: evaluation newline tolerance is LF-specific

**Location:** `evals/internal/eval/task.go:256-275`,
`evals/internal/eval/task_test.go:152-172`

The current working-tree change documents that one trailing newline difference
does not affect file verification, but `strings.TrimSuffix(value, "\n")` leaves
the carriage return in a CRLF terminator. A file ending in `\r\n` and an
otherwise identical expected value with no terminator still fail comparison. The
new test covers LF only.

**Suggested fix:** define whether the rule is specifically LF or a platform line
ending. If the latter, normalize one final `\r\n` or `\n` on both sides and add
the CRLF case.

## Unresolved suspicions

- Windows may be intentionally unsupported, but the platform-specific lock and
  credential implementations make that intent unclear. The disposition of the
  Windows finding depends on the desired platform contract.
- The MCP catalog-refresh cost is structurally O(catalog) but was not measured
  against a large live server.
- The current worktree changed while this review was running: the bounded
  auto-approval plan and todo entry appeared after the initial inventory. They
  were treated as known planned work and not reviewed as implementation.

## Checks run

- `make check` — passed once, including `go vet`, `staticcheck`, and
  `go test -race -count=1 ./...`.
- `go test -count=50 ./internal/agent -run
  '^TestExclusiveToolSerializesAcrossConcurrentBatches$'`
  — failed 26 of 50 runs at the obsolete cross-loop assertion.
- `GOOS=linux go vet ./...` — passed.
- `GOOS=windows go vet ./...` — failed on Unix-only shell code and two untagged
  FIFO tests.
- `GOOS=windows go build ./...` — failed on `Setpgid` and `syscall.Kill` in
  `internal/tools/shell.go`.
- Focused searches mapped resume, language-tool, and subagent behaviors to their
  unit, integration, and end-to-end coverage.

## Topic verdicts

- **Correctness:** narrow but real recovery and cancellation defects remain in
  the primary turn state machine; the current evaluation normalization also has
  a platform edge.
- **Testing:** broad overall, but one obsolete test makes the required gate
  flaky and several user-visible paths lack shipped-process coverage.
- **Architecture:** package ownership is generally coherent; platform support is
  the major unresolved contract ambiguity.
- **Performance:** the ordinary provider path is reasonable; MCP calls retain an
  unmeasured whole-catalog refresh cost.
- **Documentation:** several stale maps and one broken user-guide link should be
  cleaned up before treating the repository as finished.
