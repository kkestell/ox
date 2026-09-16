# Unnecessary machinery and deletion candidates

Scope: repository-wide Go inventory, followed by direct review of session
state/commit/replay, primary and child dispatch, configuration, LSP, filesystem
tools, output capture, MCP transport, and the evaluation client/proxy. This is a
targeted simplification audit, not an exhaustive correctness review of every Go
file. Evaluation fixtures and generated evaluation results are excluded from
line estimates. The working tree was clean before this report.

Mode: general. Selected topics: architecture (duplicate owners and state
representations), resources (copies and ownership), error handling (impossible
failures and recovery), readability (redundant control flow), and dependencies
(capabilities already available in pinned libraries and Go). Security-sensitive
checks were traced where a proposed deletion touches them; this is not a
whole-codebase security audit.

## Conclusion

There is substantial unnecessary code. The highest-value cuts are entire paths,
not scattered nil checks. However, the evidence does not establish thousands of
production lines that can simply be deleted while preserving all contracts.
Thousands across implementation and associated tests are plausible after a
persistence simplification, but that is a refactoring hypothesis, not a verified
deletion count.

Estimates below are rough net reductions in physical implementation lines,
including comments and blank lines, after replacement code. They are not
measured patch sizes. Overlapping alternatives must not be added together.

## Findings

### Medium — Optional checkpoints have become a second persistence implementation

`internal/agent/state.go:244`, `:434`, `:480`, `internal/agent/agent.go:1323`,
`internal/agent/store.go:29`.

The log already reconstructs provider history, including compaction. Checkpoints
add another schema, usage representation, state constructor, ordering rules,
identity conversion, open-turn validator, version, and byte-accounting policy.
`foldRecords` restores every checkpoint it encounters before choosing the last
one; it still walks all record envelopes. ACP replay still decodes the original
transcript. This is an optimization of one part of load, not an alternative to
reading/replaying the log.

It also affects correctness: `commitLocked` encodes the candidate checkpoint
before testing whether it is worth writing. `encodeRecord` rejects payloads over
8 MiB. Thus a checkpoint containing accumulated state over that bound can reject
an otherwise small turn-finished record, even though the checkpoint is supposed
to be optional. This consequence follows from the call path; an oversized-state
reproduction was not run.

Suggested direction: remove checkpoints and fold the authoritative records. Keep
compaction records, intent-before-effect durability, corruption validation, and
torn-tail repair. Estimated reduction: **350–450 implementation lines**, plus
roughly **250–350 checkpoint-specific test lines**; keep the mixed tests'
history, usage, and replay assertions. This changes the documented
implementation design and trades away a load optimization. Measure
representative long-session loads before deciding whether the optimization earns
its machinery.

### Medium — Checkpoint recovery supports states no checkpoint writer can produce

`internal/agent/state.go:601`, `:620`, `:632`.

Even if checkpoints stay, their suspended-exchange, permission, and
tool-execution restoration branches should go. Writers checkpoint only after
`turn_finished` or compaction. A finished turn clears suspension and executions.
Compaction rejects a suspended exchange; starting a tool requires that
suspension, and completing the exchange clears the executions before the next
admission. Consequently neither writer boundary has suspended permission
progress or tool executions to project.

For example, `[user, suspended exchange, tool started]` cannot checkpoint;
`[user, suspended exchange, tool started, tool completed, exchange completed,
compaction]`
can, but its suspension and executions are already cleared.

Delete those projection fields and their reconstruction/validation. A malformed
projection should be refused, not used to justify implementing an unreachable
recovery path. Estimated reduction: **80–110 lines**, already included in the
full-checkpoint deletion estimate above.

### Medium — Two clients implement JSON-RPC machinery already supplied by jrpc2

`internal/lsp/client.go:175`, `:233`, `:321`, `:353`;
`evals/internal/eval/client.go:23`, `:197`, `:219`, `:308`, `:361`.

The LSP adapter manually owns envelopes, request IDs, a pending-request map,
response channels, dispatch, writes, cancellation notifications, and connection
death. The evaluation client separately implements envelopes, IDs, callback
responses, and response matching. The production ACP connection already uses the
pinned `github.com/creachadair/jrpc2` dependency.

Its local source provides `Client.Call`, `Notify`, `OnNotify`, `OnCallback`,
`OnCancel`, and LSP framing. Use that client's request/response machinery in
both places. Preserve LSP initialization, document synchronization, diagnostics,
process ownership, evaluation metrics, and raw event capture.

Important limit: its stock header framing does **not** enforce Ox's header/body
bounds. Retain a small bounded framing adapter; replacing everything with
`channel.LSP` would remove a required protection. Evaluation cancellation must
still send `session/cancel` and wait for the result within its grace period.
Estimated net reductions: **180–240 LSP lines** and **100–160 evaluation
lines**. Exact malformed-envelope behavior needs adapter tests during
replacement.

### Medium — Published immutable values are repeatedly deep-copied as if mutable

`internal/agent/state.go:722`, `:1801`, `:1938`; `internal/agent/loop.go:506`,
`:1540`; `internal/settings/resolve.go:38`.

The implementation explicitly publishes copy-on-write state and already returns
shared configurations from `turnConfiguration`. Nevertheless, every commit
clones configuration, todo, open-turn configuration, and history slice storage;
every model request copies message content, tool calls, reasoning bytes, and
tool schemas again. Suspension copies its immutable provider payload along with
the permission fields that actually change.

The reviewed mutation paths replace configurations/todos, append or replace
history, and change suspension decisions/pending generation. Provider request
translation also constructs replacements rather than writing through its input.
The mutable identity/execution maps genuinely need copying under the current
publication model; they are not deletion candidates merely because they grow.

Share the immutable payloads. Copy only containers changed in place and inputs
crossing an actual mutable caller boundary. Replace manual map-copy helpers with
`maps.Clone`; delete `cloneStoredToolResult`, whose entire body is
`return value`. Keep independent copies where an API genuinely promises mutable
ownership. Estimated reduction: **100–180 lines**, depending on which ownership
promises are retained. Regression and race checks must prove held snapshots
remain stable.

### Medium — Tool dispatch converts internal bugs into ordinary model-visible failures

`internal/agent/loop.go:1155`, `:1179`; `internal/agent/agent_test.go:648`.

`executeOne` catches every panic, returns `tool panicked: ...`, and lets the
agent continue dispatching. There is no narrower invariant or foreign in-process
plugin boundary being protected. Built-in tools run Go code; external MCP
servers are already out of process. This directly contradicts the repository's
policy that internal invariant violations panic.

The nil-executor fallback similarly permits malformed trusted tool definitions
through construction and degrades them at invocation. Require executors when
constructing a tool set, with invariant failure semantics, and remove dispatch's
fallback. Unknown model-chosen tool names remain normal tool failures.

Delete the blanket recovery and replace the panic-as-successful-recovery test.
This is a small cut, approximately **5–15 net lines**, but an important policy
correction. Process termination on a tool bug is an intentional behavior change.

### Low — Already-validated configuration is reinterpreted below its boundary

`internal/settings/settings.go:246`, `internal/agent/language.go:25`,
`internal/lsp/lsp.go:119`; `internal/agent/agent.go:1597`, `:1654`;
`internal/tools/shell.go:177`.

Settings normalizes language-server names/commands/extensions, sorts servers,
and rejects extension collisions. The agent converts that validated result into
another representation. `lsp.New` then sorts, trims, normalizes, clones, and
validates it again, returning user-style configuration errors for trusted input.
Likewise ACP callbacks validate agent-built request fields, and terminal
creation responses are validated both at the callback boundary and inside the
shell tool.

Give validated LSP definitions one representation/owner. At callbacks, retain
validation of **client responses** and validate model arguments before
constructing requests; do not repeatedly validate the trusted session ID,
resolved absolute path, or validated terminal ID. Replace impossible
precondition failures with invariant assertions where useful. Estimated
reduction: **100–170 lines**, including outgoing-only validators made
unnecessary by the change. Direct constructor tests would need to reflect the
trusted-input contract.

### Low — ID generation propagates an error Go cannot return

`internal/agent/agent.go:1768`, `internal/agent/subagent.go:614`,
`internal/workspace/workspace.go:383` and their callers.

Go 1.26.4's `crypto/rand.Read` always fills its buffer and returns nil error;
entropy failure terminates the process irrecoverably. `randomID` and
`temporaryName` nevertheless return errors, and callers carry unreachable error
branches through session creation, prompt admission, response creation, turn
completion, memory, child startup, and temporary-file replacement.

Make these helpers return strings and remove the entropy-error plumbing. Keep
real filesystem errors. Estimated reduction: **35–55 lines**. The child ID
collision loop is a separate, lower-value candidate; do not remove validation of
provider-supplied IDs along with it.

### Low — Session claim and recovery duplicate the same turn lifecycle

`internal/agent/agent.go:1916`, `:1969`.

Both methods construct the same context, active turn, completion channel, and
release closure. Only admission and clearing `recovering` differ. Keep those
conditions explicit, then share turn construction/release. Numeric
`activeTurn.id`/`nextTurn` exist only to identify the same active object on
release; pointer identity already supplies that fact. Estimated reduction:
**25–40 lines**. Keep cancellation and close/admission synchronization, which
serve real races.

### Low — Local helpers rebuild ordinary library operations

- `internal/tools/grep.go:211`: `readBoundedLine` manually assembles line
  chunks, handles buffer-full/EOF, trims endings, and validates UTF-8. A
  configured `bufio.Scanner` handles assembly/EOF. Preserve the exact 4 MiB
  content bound, CRLF behavior, and explicit stopped-scan note; Scanner's buffer
  overhead needs allowance. Estimated reduction: **20–30 lines**, not the entire
  grep tool.
- `evals/internal/eval/gateway.go:70`: `proxyHandler` implements HTTP proxying
  with `http.Client` and `io.Copy`. Use `httputil.ReverseProxy` with the
  existing path rewrite and budget wrapper. This also gives SSE flushing and
  proper proxy header handling. Estimated reduction: **20–30 lines**.
- `internal/agent/loop.go:1000`: `startFailed` cannot reach its later check:
  setting it also writes a non-nil indexed error, and the error scan returns
  first. Delete the flag and branch, approximately **6 lines**.
- `internal/agent/subagent.go:335`: every caller passes `observe=true`; remove
  the flag and conditional. `drainInbox` can transfer its slice before clearing
  it, rather than cloning storage it no longer owns. Small cuts, not subsystems.

## Larger opportunities requiring a design decision

The persistence path stores the same provider exchange first in
`suspendedModelExchangeRecord`, then again in `modelExchangeRecord`. Tool
results are stored at tool completion and again in the completed exchange.
Validation then proves the redundant copies agree (`state.go:1462`), and replay
contains logic to avoid displaying both copies (`state.go:1551`).

One authoritative exchange payload, with dispatch/completion records referring
to its calls, could remove assembly, equality checks, and replay branching.
Preserve crash recovery, never retry uncertain effects, and represent
never-started failures explicitly. This is the most promising route toward a
thousand-line persistence reduction including tests, but no replacement design
or net patch count has been verified. It overlaps with checkpoint/copy savings.

The LSP query gate is another candidate: `sessionLanguages` forwards five
methods solely to serialize them, while the manager contains startup and
document synchronization locks. Moving the cancellable gate to the manager could
remove the forwarding layer and simplify startup ownership. Do not merely delete
the gate: primary and child loops genuinely issue conflicting language queries.

## Checks that should stay

Do not delete workspace confinement, regular-file/nonblocking-open checks,
bounded reads, credential-file security, redirect credential stripping, MCP
identity evidence, durable dispatch intent, or cancellation cleanup. Each
protects an external boundary or a documented crash/effect contract. In
particular, an `os.Root` does not make an arbitrary file safe to read: a FIFO
can still block. A ripgrep subprocess is not automatically equivalent to the
confined walker either.

MCP's HTTP cancellation tracker looks suspiciously elaborate, but removing it
requires proving the pinned SDK cancels the underlying response body under Ox's
call context. No such proof was established here. It is not a confirmed cut.

## Verification

- Inventoried 22,661 non-test Go lines across `cmd`, `internal`, and evaluation
  implementation, excluding task fixtures. Read the owning architecture and
  product contracts; searched construction, mutation, checkpoint, and callback
  paths. Line estimates are approximate, not post-refactor measurements.
- Inspected pinned local primary sources: Go 1.26.4 `crypto/rand.Read` and
  `os.File.Write`; jrpc2 v1.3.5 client options and header framing. No dependency
  addition or upgrade is proposed.
- `go test -race -count=1 ./internal/agent -run
  'Test(FoldRestoresLatestCheckpointAndAppliesItsTail|FoldRejectsInvalidCheckpoints|CompactionAcceptsOpenTurnBoundariesAndRejectsIncompleteToolGroups|TurnConfigurationIsSharedButNeverRewritten|ToolFailuresEachProduceOneTerminalResult)$'`
  passed.
- `go test -race -count=1 ./internal/lsp -run
  'Test(ManagerRoundTripAndLazyLifecycle|TimeoutCancellationCrashAndMalformedReply|ConcurrentResponseCorrelation)$'`
  passed.
- `go test -count=1 -tags=evalsmoke ./evals/internal/eval` passed, using the
  fake provider only.
- These prove the current relevant behavior, not an unimplemented rewrite.
  Snapshot/race tests, exact scanner-bound tests, transport-error tests, and
  long-log load measurements remain necessary when implementing candidates.

## Topic verdicts

- Architecture: checkpoints, redundant persistence payloads, and duplicate RPC
  clients are the largest opportunities.
- Resources: immutable payload copies are removable; mutable publication maps
  and external ownership boundaries still require care.
- Error handling: blanket panic recovery and entropy-error plumbing should go.
- Readability: duplicate lifecycle construction and unreachable branches earn
  direct cuts; a generic execution framework would not.
- Dependencies: use the existing pinned RPC library and standard scanner/proxy
  capabilities; retain Ox-specific bounds and semantics.
