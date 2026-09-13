# Whole-codebase review — architecture, readability, correctness, performance, testing

- **Scope:** entire codebase (all production Go packages, `internal/e2e`,
  `integration/`, `evals/`)
- **Mode:** specific-topic, one exhaustive lens per topic
- **Topics:** architecture, readability, correctness, performance, testing
  (requested explicitly)
- **Method:** five parallel review agents, each reading every production file in
  the corpus and the owning contracts (`docs/spec.md`, `eng/architecture.md`,
  `AGENTS.md`), applying the full topic checklist, and verifying suspicions with
  builds, greps, and benchmarks.

## Findings by severity

| #  | Sev    | Topic        | Location                                                                    | Summary                                                                                                                         |
| -- | ------ | ------------ | --------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| 1  | High   | readability  | `internal/agent/loop.go:745-755`, `834-844`                                 | Decision→result mapping written out twice in two shapes inside one 170-line function                                            |
| 2  | High   | readability  | `internal/agent/loop.go:767-833`, `internal/agent/subagent.go:551-580`      | Permission-decision sequence exists in three shapes; four lines byte-identical across two files                                 |
| 3  | Medium | correctness  | `internal/agent/loop.go:242-244`                                            | Duplicate model-emitted tool-call ID skips turn finalization, bricking the live session until close+load                        |
| 4  | Medium | correctness  | `internal/agent/loop.go:180-192`                                            | Cancellation during a partial tool-call stream discards text the client already saw                                             |
| 5  | Medium | correctness  | `internal/lsp/client.go:283-291`, `363-368`                                 | `handle` can send on a channel `markDead` closed — latent panic once LSP is wired                                               |
| 6  | Medium | performance  | `internal/agent/store.go:125-171`, `300-349`                                | `session/list` fully reads and decodes every session log (up to 64 MB) to use three fields                                      |
| 7  | Medium | performance  | `internal/mcp/mcp.go:130-151`                                               | Every MCP tool call re-discovers the whole catalog and re-parses every schema                                                   |
| 8  | Medium | testing      | `internal/agent/agent.go:180`                                               | `session/resume` has no shipped-binary (e2e) test                                                                               |
| 9  | Medium | architecture | `internal/openrouter/catalog.go:136-141`                                    | Catalog freshness rule applies only to first load; in-memory catalog is frozen for process lifetime                             |
| 10 | Medium | architecture | `AGENTS.md:33`, `217-218`                                                   | AGENTS.md claims environment credential storage that does not exist                                                             |
| 11 | Low    | readability  | `internal/agent/loop.go:98` (and ~20 sites)                                 | Free functions name the session parameter `value`; domain word unused                                                           |
| 12 | Low    | readability  | `internal/agent/memory.go:219-227`                                          | Variable holding surviving facts is named `expired`                                                                             |
| 13 | Low    | readability  | `internal/agent/agent.go:1862-1876`, `1913-1927`                            | 14-line `releaseOnce` closure duplicated across `claim` and `claimRecovery`                                                     |
| 14 | Low    | readability  | `internal/agent/loop.go:90-93`, `internal/agent/subagent.go:102`            | `run.subagents` assigned in three places across two functions                                                                   |
| 15 | Low    | readability  | `internal/agent/memory.go:195`                                              | `withDocument` `allowWrite` parameter has no caller passing `false`                                                             |
| 16 | Low    | readability  | `internal/agent/compact.go:430`, `internal/agent/loop.go:1069`              | Two production helpers kept alive only by tests                                                                                 |
| 17 | Low    | readability  | `internal/agent/state.go:1597-1627`                                         | Three hand-built `UsageUpdate` literals and duplicated effective-config blocks in `replay`                                      |
| 18 | Low    | correctness  | `internal/openrouter/types.go:44`                                           | `apiError.Code int` cannot decode a string error code from a stream error object                                                |
| 19 | Low    | correctness  | `internal/openrouter/retry.go:47-56`                                        | Server `Retry-After` clamped to 10 s maxBackoff                                                                                 |
| 20 | Low    | correctness  | `internal/agent/state.go:1082-1091`                                         | Negative provider token counts wrap silently via `uint64` conversion                                                            |
| 21 | Low    | correctness  | `internal/agent/agent.go:516-552`                                           | `relayTurn` has no final `flushDirty` after the event stream closes                                                             |
| 22 | Low    | performance  | `internal/workspace/stream.go:198-204`                                      | `sanitize` does unconditional UTF-8 round trip per 32 KB chunk; `utf8.Valid` fast path is ~6× (measured)                        |
| 23 | Low    | performance  | `internal/workspace/spill.go:102-104`, `221-237`                            | Line collections joined up to four times per spill/measure (measured ~2× allocations)                                           |
| 24 | Low    | performance  | `internal/agent/state.go:720-753`, `internal/agent/agent.go:1252-1296`      | Per-commit deep clone of monotonic identity maps under `stateMu`; quadratic over session life                                   |
| 25 | Low    | performance  | `internal/agent/compact.go:144-255`                                         | Compaction path JSON-marshals the full request up to 5 times per triggering request                                             |
| 26 | Low    | performance  | `internal/agent/loop.go:468-498`                                            | Whole history deep-copied per provider request; possibly pure defensive copying                                                 |
| 27 | Low    | performance  | `internal/lsp/protocol.go:84-93`                                            | `textLine` re-splits the whole document per decoded position (dead until LSP is wired)                                          |
| 28 | Low    | testing      | e2e suite                                                                   | Cross-process session lock error never proven through the binary                                                                |
| 29 | Low    | testing      | e2e suite                                                                   | `session/list`/`session/delete` error paths unproven e2e                                                                        |
| 30 | Low    | testing      | e2e suite                                                                   | Subagent multi-child concurrency proven only below the shipped binary                                                           |
| 31 | Low    | architecture | `internal/agent/loop.go:1069`, `internal/agent/compact.go:430`              | Dead/test-only production surface (`Agent.partition`, `estimateRequestTokens`, `Catalog.ContextWindow/Reasoning`, `Tool.Label`) |
| 32 | Low    | architecture | `internal/agent/loop.go:808-810`, `internal/agent/subagent.go:570-572`      | Allow-always downgrade rule implemented twice                                                                                   |
| 33 | Low    | architecture | `internal/agent/adapter.go:201-218`, `internal/workspace/stream.go:390-401` | UTF-8 tail-trimming logic duplicated across two owners                                                                          |
| 34 | Low    | readability  | `internal/agent/compact.go:364-374`, `395-396`                              | Percent arithmetic restates a ceiling/floor in three steps                                                                      |
| 35 | Low    | readability  | `internal/agent/state.go:1963-1983`, `737-751`                              | Manual map copies where `maps.Clone` is the established word                                                                    |
| 36 | Low    | readability  | `evals/internal/eval/runner.go:413-458`                                     | Two trace scanners in two shapes                                                                                                |
| 37 | Low    | readability  | `internal/lsp/client.go:415-424`                                            | `filepathExtension` reimplements `filepath.Ext`                                                                                 |
| 38 | Low    | readability  | `internal/tools/memory.go:110-111`                                          | 2 KiB bound duplicated as magic literal against `internal/agent/memory.go:23`                                                   |
| 39 | Low    | readability  | `cmd/ox/main.go:152-162`                                                    | Flag validation via a throwaway map literal                                                                                     |
| 40 | Low    | readability  | `internal/settings/settings.go:185-199`                                     | `ResolveProcess` validates twice with no visible difference                                                                     |
| 41 | Low    | readability  | `internal/tools/shell.go:139-155`, `321-345`                                | Shell result composition in two shapes                                                                                          |
| 42 | Low    | readability  | `internal/openrouter/catalog.go:94-96`, `219-237`                           | `bytes.Compare` on strings; `switch` used as a two-arm `if`                                                                     |
| 43 | Low    | readability  | `internal/agent/loop.go:1427`, `subagent.go:365-371`                        | Method named after its return package (`acp()`); two bare-string returns                                                        |
| 44 | Info   | correctness  | `internal/trace/trace.go:177-186`, `internal/tools/write.go:81,86,129`      | Trace tool map never pruned for interrupted calls; write tool never checks model content for UTF-8                              |
| 45 | Info   | architecture | `internal/settings/resolve.go:11`                                           | `settings → openrouter` edge missing from the architecture diagram                                                              |
| 46 | Info   | architecture | `eng/architecture.md:57`                                                    | "No lower package reads the environment" reads as a blanket rule; shell/MCP compose child envs deliberately                     |
| 47 | Info   | architecture | `go.mod`                                                                    | go-git pulled in for one package (`plumbing/format/gitignore`)                                                                  |
| 48 | Info   | testing      | `internal/lsp`                                                              | Dead code with a full passing test suite (737 lines) proving unreachable behavior                                               |

## Topic reports

### Architecture

Dependency direction, state ownership, and boundary placement verified against
`eng/architecture.md` via the actual import graph (`go list`):
agent→acp/credentials/mcp/openrouter/settings/skills/trace/workspace;
tools→agent/shellrules/workspace; mcp→acp; lsp→workspace; skills→workspace;
evals→acp; no cycles, nothing reaches back into its caller. `internal/lsp`
confirmed unimported. No hidden global state (only the build-tagged
`defaultWebFetchNetwork` and an atomic counter).

- **#9 (Medium)** Catalog freshness contract drift: the architecture states a
  stale cached catalog is "refetched on the spot once it is not"; the
  implementation checks freshness only at first load and then serves `c.catalog`
  unconditionally for the process lifetime. Fix: re-check freshness in
  `Catalog()` falling back to the in-memory copy, or narrow the architecture
  text.
- **#10 (Medium)** AGENTS.md:33 says credentials come from "Environment and
  OS-keyring"; `internal/credentials/credentials.go:183-210` resolves only the
  credential file and keyring, and `internal/e2e/config_test.go:132-150` proves
  environment credentials are deliberately ignored. Fix the two sentences.
- **#31, #32, #33 (Low)** Dead/test-only surface; the allow-always downgrade
  rule in two owners; UTF-8 tail-trimming in two owners. Each has one
  implementation that should survive; see table for locations.
- **#45, #46, #47 (Informational)** Diagram edge omission; over-broad
  environment sentence; go-git for one gitignore matcher.

Verdict: faithfully realized — one real contract drift (catalog freshness), one
stale doc claim (credentials), and a small amount of dead or duplicated surface.

### Readability

Comments consistently explain why and never record history; functions mostly
state one rule; happy paths sit at the left margin.

- **#1 (High)** `executeSuspendedBatch` writes the decision→result mapping twice
  in two shapes inside a 170-line function; propose one `applyDecision` helper
  (shape given in the detail above the table).
- **#2 (High)** The "granted → suggest rule → ask → decide → downgrade
  allow-always when unnamed → grant" sequence appears in `loop.go` twice (once
  wrapped in durable records) and again in `subagent.go`; the four lines from
  `decideApproval` through the empty-rule downgrade are byte-identical. Propose
  one `askApproval` helper.
- **#11–#17 (Low)** Naming (`value` for `*session`, `expired` for surviving
  facts, `acp()`), duplicated `releaseOnce` closure across two claim paths,
  three-way `subagents` wiring, the caller-less `allowWrite` parameter,
  test-only helpers, and the repeated usage-update blocks in `replay`.
- **#34–#43 (Low)** A tail of local rewrites, each verified equivalent in place:
  percent arithmetic collapsing to one ceiling expression, `maps.Clone` for
  manual copies, one predicate-parameter scanner in evals, `filepath.Ext`
  instead of `filepathExtension`, the memory bound living in two files, a
  throwaway map in flag validation, the double `validateProcess`, shell result
  composition in two shapes, `strings.Compare`, a two-arm `switch`, and opaque
  string-pair returns.

Verdict: reads well above average; the debt is concentrated in the agent
tool-batch permission path and a handful of names.

### Correctness

~89 production files (~15.4k lines) reviewed against `docs/spec.md` and
`eng/architecture.md`; `go build`, `go vet`, `staticcheck`, and
`go test -race -count=1` across `internal/`, `integration/`, and `evals/` all
pass.

- **#3 (Medium)** Traced: prompt claims turn `T1` → stream returns a tool-call
  ID already in `state.toolCallIDs` → `validateToolCallIDs` fails →
  `return loopOutcome{err}` without `finishTurn`, unlike every other admission
  error. `openTurn` stays open and every later prompt fails in `apply` ("user
  message belongs to another open turn") until `session/close` + `session/load`.
  No test covers duplicate tool-call IDs.
- **#4 (Medium)** The partial-exchange commit requires
  `len(completion.ToolCalls) == 0`; cancelling mid-tool-call-stream discards the
  already-streamed text and reasoning, contradicting "output already streamed to
  the client remains part of the session." Fix: commit the exchange with
  text/reasoning/usage only.
- **#5 (Medium, latent)** `handle` sends a response on a pending channel after
  releasing `pendingMu`; `markDead` holds `pendingMu` and closes every pending
  channel. Interleaving is a `panic: send on closed channel`. Unreachable today
  (no importers); will crash Ox once LSP is wired.
- **#18–#21 (Low)** String error codes in stream API errors; `Retry-After`
  clamped to 10 s; `uint64` conversion silently wrapping negative provider token
  counts; missing final `flushDirty` in `relayTurn`.
- **#44 (Informational)** Trace tool-map entries for interrupted calls never
  deleted (bounded leak); write tool never validates model-supplied content as
  UTF-8, so a later `read_file` of the written file fails.
- Contract-drift checks came back clean: one-prompt-per-session, resume-refusal,
  replay-never-executes-tools, permission generations, compaction boundaries,
  checkpoint gating, MCP caps and deadlines, web-fetch dial/redirect/bounds,
  memory and skills limits, trace sanitization, delegation capability pairing,
  and a durable-record JSON round-trip audit.

Verdict: the core state machine and durable replay are unusually rigorous;
findings are narrow edge paths and none corrupts durable state.

### Performance

All 67 production files read; every candidate judged with stated call frequency;
benchmarks run in a throwaway temp module (nothing written to the repo) on Apple
M4.

- **#6 (Medium)** `session/list` projection uses three fields per session but
  does a full `io.ReadAll` (up to 64 MB) plus per-line envelope decode of every
  log. Measured shape: 20 MB log ≈ 23 ms and 73 MB transient allocation per
  session per list refresh.
- **#7 (Medium)** `Bundle.Call` re-runs `discoverTools` (full catalog round
  trips) and re-parses/re-resolves every tool's schema on every call —
  O(catalog) per invocation where the activation-time descriptor already pins
  the target. Unmeasured (needs a live server) but structurally wasteful.
- **#22, #23 (Low, measured)** UTF-8 sanitize round trip (~6× gap, 26.6→4.5 μs
  per 33 KB chunk) and up to four joins per spilled line collection (~2×
  allocations at the 10 MB cap). Cheap fast paths, not urgent.
- **#24–#27 (Low)** Per-commit identity-map cloning under `stateMu` (quadratic
  over session life, ~0.5 ms at 21 k entries); 5× request marshaling in the
  compaction path (~0.55 ms per 1 MB per marshal at ~1.9 GB/s); whole-history
  deep copy per request (possibly pure defensive copying — flagged as a design
  call needing the resources lens); LSP `textLine` re-splitting the document per
  position (dead until wired).
- Informational items checked and found acceptable: per-delta unbuffered stdout
  writes (the ACP streaming contract), trace mutex across a small local file
  write, per-directory gitignore opens during walks.

Verdict: no user-visible hot spot in the shipped turn path; the two Medium
findings are `session/list` and MCP catalog re-discovery.

### Testing

Every production package has tests; the only gap in mapping is below.

- **#8 (Medium)** `session/resume` is implemented, validated, and specified
  (`docs/spec.md:158`) but never sent by any e2e test — proven only at the
  in-process integration boundary. A dispatch wiring regression would pass
  `make check`. Suggested: e2e test asserting no replay updates, no MCP
  reactivation, and the error shape for an invalid resume.
- **#28–#30 (Low)** Cross-process lock error, `session/list`/`delete` error
  paths, and multi-child subagent concurrency are each proven only at
  unit/integration boundaries, not through the binary.
- **#48 (Informational)** `internal/lsp` dead code with a passing test suite;
  also noted: a few e2e error assertions check the JSON-RPC code but not the
  message.
- Spot-verified mapping of the major capabilities (session lifecycle/replay,
  permissions/recovery, stream assembly and retry, tool confinement, settings,
  skills, MCP incl. deadlines and redaction, trace sanitization, credentials,
  harness self-tests). Harness rules hold throughout: `sse`/`ev*` builders,
  seeded files, named queued responses, `hold` for cancellation, `--no-keyring`,
  unmatched-queue cleanup failures.
- Checks: `go test -race -count=1 ./...` all 17 packages pass; `make check-go`,
  `make check-docs`, `make test-eval` clean.

Verdict: coverage unusually strong; the one genuine gap is `session/resume`'s
absence from the shipped-binary suite.

## Unresolved suspicions

- Whether the in-memory catalog freeze (#9) is deliberate
  one-process-one-catalog policy or drift.
- Whether OpenRouter ever emits string `code` in stream error objects (#18) —
  needs a captured payload or schema.
- Whether `newSubagentGroup`'s self-assignment of `run.subagents` (#14) is
  required by a later struct copy; consolidation proposed without asserting
  which assignment is removable.
- Whether `ResolveProcess`'s first `validateProcess` (#40) is load-bearing
  fail-fast behavior a test depends on.
- Whether the `Read`-side overlap trim in `workspace/stream.go:243-246` can
  mis-trim on spilled streams with invalid UTF-8/CRLF divergence;
  bounds-checked, cosmetic; a targeted test would settle it.
- Whether `modelRequest`'s history deep copy (#26) is load-bearing for aliasing
  safety somewhere untraced.
