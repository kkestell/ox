# Consolidated codebase review

- **Date:** 2026-09-12
- **Scope:** deduplicated findings from the three codebase reviews in this
  directory.
- **Sources:** `2026-09-12-001-codebase-review.md`,
  `2026-09-12-001-whole-codebase-five-lens-review.md`, and
  `2026-09-12-003-entire-codebase-topic-review.md`.

This document consolidates confirmed findings. Repeated symptoms and their
missing regression tests are one finding. Small related cleanup findings are
grouped where they make sense to fix together.

## High severity

### F01. Session state is read without its lock during concurrent configuration changes

- **Area:** correctness and testing
- **Evidence:** `internal/agent/loop.go` and `internal/agent/agent.go` read
  `session.state` fields from a live turn while `commitLocked` can replace the
  state under `stateMu`. Multiple reviews reproduced the race under `-race`.
- **Required outcome:** snapshot every turn-visible state value under `stateMu`,
  document the lock boundary, and add a regression test that overlaps a
  configuration change with a running turn without introducing a happens-before
  edge.

### F02. Reusable shell rules can widen to an interpreter or wildcard grant

- **Area:** correctness and security
- **Evidence:** `internal/shellrules.Suggest` can derive a bare interpreter such
  as `sh` from `sh -c ...`, and its fallback can derive `*`. Prefix and wildcard
  matching then authorize unrelated commands for the rest of the activation.
- **Required outcome:** refuse allow-always suggestions when the safe literal
  prefix is empty, is a wildcard, is a bare interpreter, or is followed by
  unbounded non-literal arguments. Cover both interpreter and wildcard cases.

### F03. `read_file` can return an unbounded single line

- **Area:** correctness, durability, and testing
- **Evidence:** `internal/tools/read.go` exempts the first selected line from
  the inline byte limit. `internal/workspace.ReadFile` first materializes the
  whole file. A sufficiently large single line can exceed the session-record
  limit and poison persistence.
- **Required outcome:** enforce the inline bound on the first line, retain a
  useful elision footer, avoid materializing unnecessary content, and test the
  exact limit and oversized single-line files without a trailing newline.

### F04. Per-turn checkpoints make session logs grow quadratically

- **Area:** durability and performance
- **Evidence:** every turn boundary serializes, validates, appends, and syncs a
  full-state checkpoint while the JSONL log only grows. An aged session repeats
  its full history until it reaches the 64 MiB load limit and becomes
  unloadable.
- **Required outcome:** bound the durable log by compacting or replacing the
  checkpointed prefix, avoid JSON round trips used only for self-validation, and
  add a growth and reload test over many realistic turns.

### F05. Every durable commit deep-clones growing session state

- **Area:** performance
- **Evidence:** `commitLocked` clones the full history and records slice for
  every record. Several nested values are cloned through JSON marshal/unmarshal,
  and checkpoint restore repeats clones for pending tool executions. Total work
  and allocation grow quadratically with session age.
- **Required outcome:** keep append-only record history out of copy-on-write
  live state, replace JSON clone helpers with explicit copies, and measure the
  resulting commit cost at increasing session sizes.

## Medium severity

### F06. Permission approval has two implementations that have drifted

- **Area:** architecture, readability, and testing
- **Evidence:** `executeSuspendedBatch` and the test-only
  `executeBatchWith`/`executeBatch` path independently implement lookup,
  cancellation, suggestion, permission, downgrade, grant, and batch-result
  rules. Their cancellation and downgrade behavior already differs, while some
  approval tests exercise only the non-production copy.
- **Required outcome:** retain one production approval path, express the durable
  reissue step explicitly, and move rule-scoping tests onto that path.

### F07. Multimodal input is advertised and validated but always rejected

- **Area:** correctness
- **Evidence:** ACP capabilities advertise image and audio prompt support and
  boundary validation accepts both, but provider-request estimation rejects any
  request containing either kind. This contradicts `docs/spec.md`.
- **Required outcome:** conservatively estimate supported multimodal content so
  advertised prompts can be admitted, with ACP-visible coverage.

### F08. Exact edits bypass the documented read-evidence rule

- **Area:** correctness
- **Evidence:** `write_file` checks recorded evidence before changing an
  existing file, but `edit_file` does not. The specification and architecture
  require earlier read, glob, or search evidence, although glob and grep do not
  currently record it.
- **Required outcome:** enforce one coherent evidence rule for writes and exact
  edits, implement every documented evidence source, and test blind and stale
  edits.

### F09. Exact-edit replacement and text-format semantics are inconsistent

- **Area:** correctness and testing
- **Evidence:** overlapping matches are counted even though Go replacement
  operations replace non-overlapping matches. Text restoration can strip all
  trailing line endings or normalize mixed endings across untouched content,
  despite the promise to preserve file format.
- **Required outcome:** count the matches that will actually be replaced and
  preserve bytes outside the edited region, including deliberate trailing blank
  lines and mixed line endings.

### F10. Read evidence and write verification can come from different filesystems

- **Area:** correctness
- **Evidence:** ACP read and write capabilities are selected independently. A
  client-buffer read can record evidence that a later local-disk write can never
  satisfy when the editor has unsaved changes.
- **Required outcome:** bind evidence to its executor or reject an incoherent
  capability combination, update the owning contract, and test divergent client
  and disk content.

### F11. Tool-output tail handling can discard valid data and scan quadratically

- **Area:** correctness and performance
- **Evidence:** `outputTail` repeatedly validates the entire retained string
  while deleting one leading byte. One invalid byte in the middle can remove all
  preceding valid output and cause roughly quadratic scanning.
- **Required outcome:** trim only a leading partial rune and replace other
  invalid UTF-8 in one pass.

### F12. Context occupancy mixes byte counts with token counts

- **Area:** correctness
- **Evidence:** estimated request bytes are published and compared as tokens
  after compaction. The client meter jumps and automatic compaction can run far
  earlier than the selected model's actual token window requires.
- **Required outcome:** keep conservative byte accounting for admission while
  converting estimates to one documented token unit for occupancy and compaction
  decisions.

### F13. A partial tool-call stream can be retried as if no output was observed

- **Area:** correctness
- **Evidence:** the provider client marks a stream as emitted only for text or
  reasoning callbacks. Tool-call fragments and reasoning-detail blocks can be
  observed before a transient failure but still trigger a full retry.
- **Required outcome:** treat every observed response delta as emitted content
  and cover a tool-call-only partial stream.

### F14. Model catalog caching can poison a running process

- **Area:** correctness, resource ownership, and performance
- **Evidence:** a stale or empty parseable cache is accepted indefinitely. A
  detached refresh updates only disk, not the memoized in-process catalog, and
  the catalog response body is read without a limit.
- **Required outcome:** reject empty caches, define freshness, bound network
  reads, and make refresh ownership and in-process installation explicit.

### F15. `session/list` folds every full log and fails on one corrupt session

- **Area:** correctness, performance, and testing
- **Evidence:** every listing reads and folds every session log before cwd
  filtering and pagination. A single unreadable log prevents all sessions from
  being listed, and page/cursor behavior has no boundary coverage.
- **Required outcome:** obtain list projections without folding full histories,
  isolate corrupt entries, and test 50/51-session paging plus stale, mismatched,
  and malformed cursors.

### F16. Provider requests are encoded repeatedly even when tracing is disabled

- **Area:** performance
- **Evidence:** admission, trace byte counting, prefix fingerprinting, and the
  HTTP client each marshal the same full request. Trace arguments are evaluated
  before the disabled trace sink can return.
- **Required outcome:** reuse request measurements or encoded data and make
  trace-only work lazy.

### F17. Streamed output tails are repeatedly copied and rescanned

- **Area:** performance
- **Evidence:** agent tool-output accumulation concatenates and copies the full
  retained tail for every chunk. Workspace preview capture similarly rebuilds
  line-boundary state for every write.
- **Required outcome:** use bounded byte buffers or incremental boundary state
  so work scales with new output rather than retained output.

### F18. Frozen turn configuration is deep-cloned inside per-call loops

- **Area:** performance
- **Evidence:** `turnConfiguration` clones tools, maps, MCP evidence, skills,
  and resolved settings on each read, including repeated reads inside record and
  tool-call loops.
- **Required outcome:** materialize one immutable configuration per turn and use
  direct lookup accessors where only one field is needed.

### F19. `grep` can hide valid earlier matches

- **Area:** correctness and performance
- **Evidence:** a long or invalid line causes `scanFile` to discard matches
  already collected from that file without a diagnostic. In `files_with_matches`
  mode it also continues scanning after the first match.
- **Required outcome:** return prior matches with a clear truncation note and
  stop at the first match when only filenames are requested.

### F20. The LSP subsystem is unreachable while documentation treats it as wired

- **Area:** architecture
- **Evidence:** nothing imports `internal/lsp`, but the architecture lists live
  `agent -> lsp` and `tools -> lsp` edges and shipped responsibilities.
  `AGENTS.md`, `docs/spec.md`, and `eng/todo.md` do not describe one consistent
  implementation state.
- **Required outcome:** keep the adapter explicitly planned until the existing
  integration work wires it, and make all ownership and dependency documents
  describe that state consistently.

### F21. Confined regular-file reading has three owners

- **Area:** architecture and security
- **Evidence:** workspace reads, root-instruction loading, and skill loading
  separately implement the same symlink rejection, nonblocking open, regular
  file check, size bound, and UTF-8 policy.
- **Required outcome:** put the security-sensitive confined read in the
  workspace boundary while leaving caller-specific content policy with each
  consumer.

### F22. Removing delegated tasks left dead production structure

- **Area:** architecture and readability
- **Evidence:** test-only batch dispatch, task constants, parent call metadata,
  child read scopes, unused tool fields, unused compaction/token helpers, and
  repeated cancellation-result construction remain after the child-agent runtime
  was removed.
- **Required outcome:** delete obsolete state and paths, retain one parent tool
  loop, and make tests exercise production entry points.

### F23. Turn orchestration obscures its state and duplicates lifecycle plumbing

- **Area:** readability
- **Evidence:** turn functions pass 10–14 positional values, `runFrom` mutates
  its `suspended` and `reissue` parameters as loop state, activation calls use
  mystery booleans, claim/recovery duplicate release logic, and prompt/recovery
  duplicate event relays.
- **Required outcome:** introduce a small turn context, name activation options,
  make pending exchange state local and explicit, and share lifecycle plumbing
  without hiding admission rules.

### F24. End-to-end tests still depend on removed delegated-task behavior

- **Area:** testing
- **Evidence:** several e2e tests and fixtures still invoke removed task tools,
  while compaction fixtures assume the old tool-set size. The suite cannot act
  as a reliable gate in this state.
- **Required outcome:** rewrite retained scenarios through the parent tool loop,
  remove obsolete helpers, and make compaction fixtures assert behavior rather
  than an obsolete tool count.

## Low severity

### F25. Provider retry classification depends on error text

- **Area:** correctness and readability
- **Evidence:** the OpenRouter client checks whether an error message contains
  `read OpenRouter stream`; rewording the wrapped error silently changes retry
  behavior.
- **Required outcome:** classify the condition with an error value that callers
  can test directly.

### F26. Editing and credential loading materialize whole files

- **Area:** performance and robustness
- **Evidence:** workspace editing and credential file loading use unbounded
  whole-file reads. Their input allocation has no boundary-specific limit.
- **Required outcome:** define appropriate per-boundary limits and stream or
  bound reads before allocation.

### F27. Atomic replacement uses a deterministic temporary filename

- **Area:** durability
- **Evidence:** `<base>.ox-tmp` with `O_EXCL` is left visible after a crash and
  permanently blocks later writes to that target. It also makes independent
  writers collide.
- **Required outcome:** use a unique same-directory temporary file, preserve
  atomic rename and directory sync, and clean up failed attempts.

### F28. The parked LSP adapter has lifecycle and implementation weaknesses

- **Area:** correctness, performance, and readability
- **Evidence:** `Manager.Close` can miss a concurrently starting client,
  diagnostics are emitted in map iteration order, protocol writes use two
  unbuffered syscalls, and manager methods repeat the same setup sequence.
- **Required outcome:** give client startup manager-owned cancellation,
  synchronize close, sort diagnostics, write framed messages efficiently, and
  share the manager preamble before integration.

### F29. Process path resolution is fragmented and inconsistently validated

- **Area:** architecture and correctness
- **Evidence:** the provider resolves its cache path directly from the
  environment, while settings, cache, sessions, and memory each implement XDG
  rules with different treatment of relative values.
- **Required outcome:** resolve process paths once at startup with one absolute
  XDG policy and pass concrete paths to lower boundaries.

### F30. ACP activation input is validated by multiple packages

- **Area:** architecture
- **Evidence:** ACP request validation, agent activation, and MCP activation
  repeat checks for server configuration and additional directories, although
  `internal/acp` owns client-input validation.
- **Required outcome:** keep wire-input validation at the ACP boundary and
  retain only state-dependent checks downstream.

### F31. Architecture and specification text retain removed or inaccurate relationships

- **Area:** architecture and documentation
- **Evidence:** documents still mention child agents, subagents, and task-queue
  behavior. The dependency graph also omits real edges, while tool-contract
  ownership does not match the code.
- **Required outcome:** update each owning document once so runtime status,
  responsibilities, and dependency direction match the tree.

### F32. Small safety and protocol rules are duplicated

- **Area:** architecture and readability
- **Evidence:** ID validation, loopback-host checks, directory sync, memory
  limits, localhost detection, and MCP catalog byte accounting have multiple
  implementations or restatements.
- **Required outcome:** select one owner for each rule and remove only the
  duplicates whose semantics are truly identical.

### F33. Configuration cloning copies the full model catalog unnecessarily

- **Area:** performance
- **Evidence:** activation and configuration changes deep-clone the entire model
  catalog even though turn selections need a much smaller immutable projection.
- **Required outcome:** freeze or project catalog data once and copy only
  session-owned selections.

### F34. Workspace discovery repeats parsing and allocation per entry

- **Area:** performance
- **Evidence:** glob patterns are normalized and parsed inside file walks, and
  ignore matching allocates path forms and scans every accumulated pattern for
  every entry.
- **Required outcome:** compile stable match inputs before walking and reduce
  per-entry allocation without changing ignore semantics.

### F35. ACP session-update values use inconsistent literal spellings

- **Area:** readability
- **Evidence:** constants exist for some `sessionUpdate` values, while adapter
  and replay code mix those constants with string literals for the same concept.
- **Required outcome:** define the complete constant set and use it at every
  producer and consumer.

### F36. Synchronization and concurrency intent is undocumented at key sites

- **Area:** readability
- **Evidence:** the session's seven mutexes do not state what they guard, the
  lock around prompt commit does not explain its relationship to configuration
  changes, and the intentionally huge handler concurrency value is unexplained.
- **Required outcome:** add short invariant comments at the owning fields or
  locks and explain only the non-obvious concurrency decisions.

### F37. Durable-state transitions contain avoidable duplicated and inline logic

- **Area:** readability
- **Evidence:** model-exchange validation and application live in one long
  switch case, compaction is both helperized and open-coded,
  `sameRequestConfiguration` mutates its inputs, and adjacent marshal cases use
  inconsistent styles.
- **Required outcome:** extract the named state rules, keep one compaction path,
  and make comparisons and marshaling direct and side-effect free.

### F38. Test-only code and mutable seams ship in production builds

- **Area:** architecture and testing
- **Evidence:** OS-specific test helpers are misnamed so they compile into the
  binary, and production globals or fields such as the web-fetch network and
  rename hook exist only for tests.
- **Required outcome:** rename test files correctly and inject failure seams
  through construction or package-local test boundaries.

### F39. Small dead and misleading code remains across packages

- **Area:** readability
- **Evidence:** examples include the discarded `userMessage` binding, unread
  `windowed` fields, a five-value stream tuple, locals named `copy`, always-nil
  replay metadata, a redundant range rebinding, a one-field MCP wrapper,
  conflicting `toolTitle` names, backwards error juggling, an empty
  `internal/config` directory, undocumented impossible panics, substring-based
  escape labeling, and repeated tool-cancellation messages.
- **Required outcome:** remove dead pieces and make the remaining local code
  state its purpose directly without introducing new abstractions.

### F40. Settings merge results can alias their inputs

- **Area:** architecture and correctness
- **Evidence:** merge helpers return an input pointer when the other side is
  nil. The non-mutation contract currently holds only because callers do not
  mutate the returned nested values.
- **Required outcome:** return an independent merged value and add a mutation
  test for the nil-side cases.

### F41. Hidden-file discovery behavior is missing from the product specification

- **Area:** documentation
- **Evidence:** glob and grep skip every path component beginning with `.`, but
  only tool descriptions mention hidden-file exclusion. `docs/spec.md` does not
  own the behavior.
- **Required outcome:** either specify the shipped exclusion or change discovery
  behavior, then test the chosen contract for dotfiles and dot-directories.

### F42. Configuration-option error behavior lacks focused coverage

- **Area:** testing
- **Evidence:** unknown sessions, option IDs, models, and reasoning values plus
  selection, persistence, and notification failures are not directly tested.
- **Required outcome:** add focused table coverage for validation errors and
  narrow fault-injection coverage for state-changing failures.

### F43. Workspace edit sync failure is untested

- **Area:** testing
- **Evidence:** `workspace.Edit` has no coverage for failure while syncing the
  parent directory after rename, although `WriteFile` covers the equivalent
  durability path.
- **Required outcome:** mirror the existing write proof for exact edits and
  assert the durable outcome after the injected sync failure.

### F44. MCP and trace error paths lack focused coverage

- **Area:** testing
- **Evidence:** MCP protocol-revision mismatch and call argument errors are
  untested. Trace span bookkeeping has no focused unit test, and its e2e check
  accepts an always-zero elapsed duration.
- **Required outcome:** add the smallest package-level tests for the MCP
  branches and assert meaningful trace span timing and correlation.

### F45. Tests use wall-clock heuristics where deterministic signals exist

- **Area:** testing
- **Evidence:** an OpenRouter retry test sleeps for a real `Retry-After` window,
  and the integration harness infers quiescence from a 25 ms delay.
- **Required outcome:** capture the injected retry delay and use an explicit
  end-of-turn signal in the harness.

## Reconciled exclusions

- Per-call MCP tool re-discovery is not carried forward as a defect. One review
  flagged its latency, while the later reviews identified it as the intentional
  identity re-check that prevents server metadata from expanding an active
  turn's authority. Optimizing it requires preserving that invariant and a
  separate design decision.
- Items labeled unresolved suspicions in the source reviews are not findings.
  They need reproduction or a product decision before entering `eng/todo.md`.
