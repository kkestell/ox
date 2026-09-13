# Codebase review: architecture, readability, correctness, performance, testing

- Date: 2026-09-12
- Scope: entire working tree — `HEAD 9214bcb` plus the uncommitted
  remove-delegated-tasks refactor (`subagent.go`, `task_queue.go`,
  `internal/tools/task.go` removed; `state.go`, `loop.go`, `agent.go`,
  `compact.go`, `prompt.go`, `tool.go`, tests and docs updated).
- Mode: specific-topic. Reviewed topics: architecture, readability, correctness,
  performance, testing.
- Note: the tree changed during the review (a concurrent process was still
  finalizing the refactor and docs). Line numbers are as of the reviewed
  snapshot; findings were re-verified against the code after the doc churn.

## Summary

Five parallel topic reviews found one confirmed high-severity data race, a
bounded-output contract that is not actually bounded, a read-before-edit
evidence rule that is documented but only enforced for overwrites, a large
amount of dead/vestigial structure left by the delegated-task removal, and
measured per-commit and per-request costs that grow quadratically over a
session's life. The test suite is strong at its boundaries; the gaps are
specific and named below.

## Correctness

### C1 (high, confirmed) — Data race on `session.state` when a config option changes during a turn

- Write: `internal/agent/agent.go:1230` (`value.state = next` in `commitLocked`,
  under `stateMu`).
- Unlocked turn-goroutine reads: `internal/agent/loop.go:1700-1708`
  (`publishUsage` reading `value.state.cost` and `turnConfiguration()`), plus
  terminal `value.state.usage.acp()` reads at `loop.go:119,162,253,267,345` and
  `agent.go:1380,1448,463`.
- Concurrent writer: `session/set_config_option` →
  `internal/agent/config_options.go` commits on its own RPC goroutine;
  `claimConfigChange` (`agent.go:1775`) only rejects a closing session. The spec
  explicitly permits a change during a turn (it takes effect next turn), so the
  setter and the turn are designed to overlap.

Confirmed independently twice with a temporary `-race` probe (created and
deleted): `commitLocked` at `agent.go:1230` vs `publishUsage` at `loop.go:1702`.
Consequence: torn `durableState` reads (wrong cost/usage/context window) and, in
the worst case, a panic while cloning or marshalling an inconsistent value.

Fix: snapshot usage/cost/configuration under `stateMu` (a `session.usage()`
accessor, or have `finishTurn` return the terminal usage) so no turn-goroutine
read touches `value.state` without the lock.

### C2 (high) — `read_file` is not bounded: a large single-line file is returned whole

- `internal/tools/read.go:131-141`: the `bytes+cost > InlineMaxBytes` break is
  guarded by `end > start`, so the first line is never capped. For a one-line
  file, `start == 0 && end == total` returns the content unchanged with no
  footer (`read.go:142-148`).
- `internal/workspace/workspace.go:96`: `io.ReadAll` reads the whole file with
  no cap (same at `workspace.go:187` for edits).

Confirmed: a 5 MiB single-line file is returned in full. Above `maxRecordBytes`
(8 MiB, `store.go:19`) the resulting `recordModelExchange` append fails at
`store.go:240`, sets `poisoned = true`, and stops the session. Even below that,
the model-visible "bounded" contract in `docs/spec.md` and `eng/architecture.md`
(Workspace boundary) is violated.

Fix: cap the first line the same way later lines are capped and elide with a
footer; bound `Workspace.ReadFile` with `io.LimitReader` and a clear over-limit
error or spill.

### C3 (medium) — Exact edits require no read/glob/search evidence, contradicting the owning docs

`docs/spec.md:56` ("Ox requires evidence from an earlier read, glob, or search
before a model may change an existing file") and `eng/architecture.md:335-338`
("Writes and exact edits require that evidence"). `write_file` enforces it
(`internal/tools/write.go:79,149`), but `edit_file` never consults `FileReads`
(`internal/tools/edit.go:86-145`), and `glob`/`grep` never call
`FileReads.Record`. So the documented glob/grep evidence path is
unimplementable, and a model can blind-edit any existing file as its first
operation. Fix: add the same hash/staleness check to `edit.go`, or narrow the
docs to say exact-match editing is its own evidence — one home for the fact.

### C4 (medium) — Multimodal input is advertised and accepted, then always refused by admission

`internal/agent/agent.go:211-215` advertises `Image`/`Audio`;
`internal/acp/validate.go:373-413` accepts them; but `estimateProviderRequest`
(`internal/agent/compact.go:324-327`) rejects any request containing
image/audio. `docs/spec.md:199` says estimates include multimodal content. A
conforming client that sends an image gets a failed admission every turn. Fix:
size multimodal content conservatively, or stop advertising it and record the
omission.

### C5 (low) — Retry classification keys on an error string

`internal/openrouter/client.go:143-146` treats any error containing
`"read OpenRouter stream"` as retryable. The string is produced at `sse.go:47`;
editing it silently changes retry behavior. Use a sentinel alongside
`errStreamEnded`.

### C6 (low) — Unbounded provider catalog read

`internal/openrouter/catalog.go:307` reads `/models` with `io.ReadAll` and no
limit, though every other network read in the package is bounded
(`client.go:64,201`). Add an `io.LimitReader`.

## Architecture

### A1 (medium) — `internal/lsp` is unreachable while the architecture claims it is wired

No package imports `internal/lsp`. Yet `eng/architecture.md:67-68,114,124` lists
it as an implemented owner with `agent -> lsp` and `tools -> lsp` edges, and
`docs/spec.md` specifies it as shipped behavior. `eng/todo.md` marks LSP
integration as pending. Fix: mark the edges/owner as planned in
`eng/architecture.md`, or wire the adapter; the three documents currently
disagree.

### A2 (medium) — Workspace confinement is reimplemented outside `internal/workspace`

`internal/agent/prompt.go:126-178` (`loadRootInstructions`) and
`internal/skills/skills.go:231-267` (`readBoundedRegular`) hand-roll the same
`os.OpenRoot` + symlink check + `O_NONBLOCK` regular-file open pattern that
`internal/workspace/platform.go:11-26` already owns. Three copies of a
security-sensitive open that must stay synchronized. Expose one bounded
regular-read helper on the workspace boundary and keep only each caller's
size/encoding policy.

### A3 (medium) — Docs still describe the removed delegated-task/subagent model

`eng/architecture.md:79,88,197,242,273,351` and `docs/spec.md:197,218,251` still
assign responsibilities and behavior to child agents, subagents, and the task
queue. Whatever the concurrent doc pass fixes, the final tree must have no
clause describing machinery that no longer exists.

### A4 (low) — Dependency graph and responsibility text disagree with imports

`eng/architecture.md:102-137` omits real edges (`tools -> acp`,
`settings -> openrouter`) and lists planned `-> lsp` edges among implemented
ones. `eng/architecture.md:79-80` says `internal/tools` owns the tool
_contracts_, but `Tool`/`Invocation`/`ClientFileSystem`/`FileReads` live in
`internal/agent/tool.go`.

### A5 (low) — Provider package reads the environment and starts an unowned background goroutine

`internal/openrouter/cache.go:23` resolves `XDG_CACHE_HOME`/`HOME` itself,
outside the settings layer. On a cache hit, `catalog.go:209` starts
`go c.refreshCatalog(path)` on a `context.Background()` timeout — work with no
owner and no cancellation that outlives the ACP request that triggered it.
Resolve the cache path at `cmd/ox` and make the refresh synchronous or
context-bound.

### A6 (low) — XDG path resolution is duplicated with inconsistent semantics

`settings.GlobalPath`, `openrouter.catalogCachePath`, `agent.SessionPath`, and
`agent.MemoryPath` each resolve a base. Settings/cache require an absolute XDG
value; session/memory accept any non-empty value, so a relative `XDG_DATA_HOME`
silently places durable state under the process cwd. One helper, one owner.

### A7 (low) — Test helpers ship in the production binary

`internal/agent/prompt_test_unix.go`, `prompt_test_windows.go`,
`internal/skills/skills_test_unix.go`, and `skills_test_windows.go` are named
`*_test_<os>.go`, not `*_<os>_test.go`. Because `prompt_test_unix.go` imports
`testing`, `go list -deps ./cmd/ox` includes `testing` in the shipped binary.
Rename to `prompt_unix_test.go` / `skills_unix_test.go`.

### A8 (low) — Empty `internal/config/`

No Go files, no importer, no owner in the package map. Remove or populate.

## Readability

### R1 (medium) — The turn pipeline threads the same values through 10–14 parameters

`runFrom` (`loop.go:88`), `executeSuspendedBatch` (`loop.go:773`),
`executeBatchWith` (`loop.go:1033`), `dispatchApprovedBatch` (`loop.go:1208`),
and `executeOne` (`loop.go:1381`) all pass `ask`, `elicit`, `fileSystem`,
`terminal`, `events`, `turn`, and `parent` together, sometimes in a different
order. A small `turnIO` struct would let a call site be read without re-reading
the callee signature and would make the same-typed swaps impossible.

### R2 (medium) — Two implementations of the approval rule and of turn admission

- `executeSuspendedBatch` (`loop.go:773-978`) is the production approval path;
  `executeBatchWith` (`loop.go:1033-1206`) is a second, differently written
  approval loop reachable only from tests. They encode the same
  cancel/grant/suggest/batch-cancel rules; a change to one silently diverges.
- `session.claim` (`agent.go:1735`) and `session.claimRecovery`
  (`agent.go:1788`) are near-identical; the 25-line release closure is copied
  verbatim. Extract `beginTurn` with each caller keeping its own guard.
- `Prompt` (`agent.go:1419-1439`) and `recoverSession` (`agent.go:478-498`)
  duplicate the event-drain loop.

### R3 (medium) — Dead/vestigial structure left by the refactor

- Unused symbols: `maxQueuedTasks` and the `task*` state constants
  (`state.go:49-58`), `allocateToolCallID` (`loop.go:434`), `applyCompaction`
  (`state.go:1152`), `estimateRequestTokens` (`compact.go:458`),
  `combinedUsage`'s variadic form (`state.go:1470`), `executeBatch`/`partition`.
- `parent` / `ParentCallID` is now always empty but threads through `event.go`,
  `adapter.go`, `state.go`, `trace.go`, `compact.go`, and eight signatures.
  `toolEventMetadata` now returns `nil` on every production call.
- `Tool.Label` is rejected when non-nil (`agent.go:96`) yet still branched on in
  `toolTitle` (`loop.go:1632-1641`); `Tool.ParentOnly` (`tool.go:18`) is never
  read; `MetaSubagent` (`acp/types.go:14`) and `ProviderSubagent`
  (`trace/trace.go:21`) have no production producer.
- `childFileReads`/`readScopes`/`readScopesMu`/`clearFileReads`
  (`reads.go:31-57`) exist only for the removed sibling-scope feature.
- The `"tool call cancelled before start"` result is written in 11 places and
  `"the user rejected this tool call"` in 3, with inconsistent
  `approval`/`target` fields.

### R4 (low) — Small clarity items

- `agent.go:1337-1341` computes `userMessage` then discards it
  (`_ = userMessage`); call it for validation only.
- `windowed` (`read.go:37-43`) carries four fields (`start`, `end`, `total`,
  `windowed`) that no caller reads.
- `streamAttempt` (`openrouter/client.go:175`) returns an unnamed 5-tuple
  `(*Completion, int, string, bool, error)`; a struct names the two ints and the
  bool.
- Locals named `copy` shadow the builtin (`state.go:719,729`,
  `config_options.go:239`).
- `replayToolCall`'s `meta` parameter is `nil` at both call sites.

## Performance

All numbers below are measured with `go test -bench -benchmem` via an overlay
(no repository files created), on an Apple M4.

### P1 (high) — Every commit deep-clones the whole state, and the `records` slice makes it quadratic

`commitLocked` does `next := value.state.clone()` (`agent.go:1208`;
`state.go:670-704`), copying `history`, `openTurnBase`, all maps, and the entire
`records` slice. A turn commits per provider start, exchange pause, tool
start/complete, permission decision, and turn finish — roughly `4 + 2K` commits
for K tools. Measured: `durableState.clone` at 20k messages = **941 µs / 5.6 MB
per call**; the records-only slice copy at 200k records = **1.30 ms / 16 MB per
commit**. Because the slice is recopied each commit, a session with R commits
pays Θ(R²) copying and sustained GC pressure.

Fix: keep the append-only `records` out of the copy-on-write value, and
copy-on-write only the fields the specific record mutates.

### P2 (high) — Every turn rewrites and re-validates the entire history as a checkpoint

At each `recordTurnFinished`/`recordCompaction`, `newCheckpointRecord`
(`state.go:388-427`) marshals the full projection, `restoreCheckpoint`
(`state.go:433`) unmarshals it back to validate, and `sessionLog.append`
(`store.go:236`) marshals it a second time before fsync. Measured at 20k
messages: `newCheckpointRecord` + `restoreCheckpoint` = **21.0 ms / 40.6 MB /
160k allocs per turn** (plus the second marshal). The log therefore grows
Θ(turns × history); a long session approaches and can exceed `maxSessionBytes`
(64 MB) and stop loading. Fix: validate the projection in memory instead of
round-tripping its own bytes, reuse the encoded record for append, and
checkpoint periodically rather than rewriting the full history every turn.

### P3 (medium) — The request is JSON-marshaled 2–3× per model request, some of it unconditionally

`providerRequestBytes` (`loop.go:178,1536`) and `prefixFingerprint`
(`loop.go:173,576-617`) are evaluated at the call site, so they marshal the
whole request even when tracing is disabled (the default) and `trace.Provider`
returns immediately. `estimateProviderRequest` (`compact.go`) and
`Client.Stream` (`openrouter/client.go:104`) marshal it again. Measured for a
~150 KB request: `estimateProviderRequest` 846 µs/1.57 MB,
`providerRequestBytes` 856 µs/1.61 MB, stream body 960 µs/1.57 MB. Fix: memoize
the frozen prefix fingerprint per turn, and gate/short-circuit byte counting on
tracing being enabled.

### P4 (medium) — `turnConfiguration()` JSON-round-trips the frozen configuration on every read

`state.go:1843-1848` → `cloneConfiguration` → `cloneResolved`
(`json.Marshal`+`Unmarshal` of `settings.Resolved`) plus `cloneTools`. It is
called per tool call (`loop.go:1444`), per batch, per request, and twice per
commit, though the turn configuration is frozen. Materialize it once at turn
start and return an immutable view.

### P5 (low–medium) — Glob patterns are re-normalized and re-parsed per walked file

`internal/tools/glob.go:85` calls `doublestar.Match(normalizeGlob(pattern), …)`
inside the walk callback; same at `grep.go:83`. Measured: 253 ns + 1 alloc/file
vs 200 ns + 0 when the pattern is normalized once. Hoist normalization and
validation before the walk.

### P6 (low) — JSON-round-trip clones for suspended exchanges and tool executions

`cloneSuspendedExchange` (`state.go:1850`) is called per commit and repeatedly
inside the suspended-batch loop; measured at **46 µs / 22.7 KB / 22 allocs** for
8 calls. Hand-write the clone as `cloneMessages` already does.

### P7 (low) — Unbounded reads on model-chosen files

`workspace.ReadFile`/`Edit` (`workspace.go:96,187`) and `credentials.LoadFile`
(`credentials.go:93`) allocate the whole input. For model-chosen paths this is
unbounded; stream/hash in chunks under a size cap.

Not a finding: MCP re-discovers the catalog on every call
(`internal/mcp/mcp.go:156`) is the intended identity re-check that binds grants
to server/tool identity; it is a deliberate cost.

## Testing

### T1 (high) — The confirmed race is untested, and the one relevant test is `-race`-blind

`integration/agent_loop_test.go:3091`
(`TestConfigurationChangeDuringTurnAppliesToNextTurn`) blocks the model on a
channel, calls `setConfig`, then closes the channel. That close/receive is a
happens-before edge, so `-race` can never observe the overlap. No e2e test sets
config mid-stream. Add a test that starts a multi-request turn and calls
`set_config_option` from another goroutine without a channel handshake, under
`-race`.

### T2 (medium) — Oversized single-line `read_file` is untested

`TestReadFileValidatesBoundsUTF8AndHugeLines`
(`internal/tools/tools_test.go:1144`) builds `InlineMaxBytes+1` **plus a second
line**, so it never hits the single-line boundary and in fact asserts the huge
line is preserved. Add a table at `InlineMaxBytes`, `InlineMaxBytes+1`, and
`InlineMaxBytes*N` with no trailing newline.

### T3 (medium) — `session/list` pagination and cursors are untested

`ListSessions` (`agent.go:740-799`) implements a 50-item page, `NextCursor`,
cwd-mismatch and stale-cursor rejection, and `encodeCursor`/`decodeCursor`
(`store.go:330-348`); every test lists one short page. Add a 50/51-session
boundary test plus stale/mismatched/malformed cursor cases.

### T4 (medium) — `SetSessionConfigOption` error branches are untested

`internal/agent/config_options.go` has no unit test. Unknown session, unknown
option id, unknown model, unknown reasoning value, `applySelections` failure,
and the persist/notify failure paths are uncovered. Add a table test asserting
each `InvalidParams` message.

### T5 (medium) — Tests that pin removed behavior were left in place

Four e2e tests still drive `task_add`/`task_run`/`task_list`
(`internal/e2e/mcp_test.go:232`, `session_test.go:458,981`, `trace_test.go:167`)
and the helper `queuedTaskRunResponse` (`model_test.go:307`);
`assertNestedTaskUpdates` (`integration/agent_loop_test.go:1500`) is uncalled.
The two compaction e2e tests (`session_test.go:308,392`) fail because the
tool-set shrink from 18 to 13 lowered estimated occupancy below their fixtures'
compaction threshold. The suite is currently red as a result. Rewrite the four
tests parent-only, retune the compaction fixtures, and keep an e2e assertion on
compaction timing so a future tool-set change is caught.

### T6 (low) — Wall-clock dependence in a retry test

`TestClientRetriesRetryAfterAndSucceeds`
(`internal/openrouter/client_test.go:149-177`) sleeps ~2 s and asserts a 1.8–3.5
s window; assert the parsed delay through the injectable `retryWait` instead.

### Strength

Beyond the gaps, coverage is genuinely strong: non-e2e `go test -race` is clean
across the tree; the e2e harness (real binary, isolated HOME/XDG, refused
default provider, `hold`/named responses, stdout purity) is exemplary; and batch
ordering, permission flow, cancellation, loop accounting, state folding,
confinement, secret redaction, and executor parity all retain focused tests
after the delegated-task removal.

## Checks run

- `go build ./...` and `go vet ./...` — clean.
- `go test -race -count=1` per package (acp, agent, openrouter, workspace,
  tools, settings, lsp, mcp, skills, credentials, shellrules, trace, cmd/ox,
  integration) — pass; `go test -cover` used for the percentages cited above.
- Temporary `-race` probe (created, run, deleted) — reproduced C1.
- Temporary read-size test (created, run, deleted) — reproduced C2.
- `go test -bench -benchmem` via `-overlay` (no repo files created) —
  P1/P2/P3/P5/P6 numbers.
- `go test -count=1 ./internal/e2e/` — currently red on the six tests in T5.
- `golangci-lint run -E unused` — used in place of `staticcheck`, which is
  broken in this environment (cannot decode Go 1.27 export data).

## Verdicts

- Correctness: one confirmed race (C1) and two contract drifts (C2, C3) plus a
  multimodal contradiction (C4); the durable-state validation and recovery logic
  otherwise read as intentionally strict and correct.
- Architecture: boundaries are mostly clean, but the refactor left dead
  subsystem plumbing (A2, A3, A7) and the LSP/dependency graph disagrees with
  the imports (A1, A4).
- Readability: solid; the duplication and dead symbols from the removal (R2, R3)
  are the real cleanup work.
- Performance: P1 and P2 are measured and grow quadratically with session
  length; P3 wastes a full request marshal per call in the default
  configuration.
- Testing: strong and appropriately layered; T1–T5 are the named gaps.
