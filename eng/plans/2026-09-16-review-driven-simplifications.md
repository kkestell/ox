# Remove redundant persistence and transport machinery

## Goal

Apply the supported deletion and simplification findings from
`eng/reviews/2026-09-16-001-unnecessary-machinery.md`, preserving recorded
history, recovery safety, external-input validation, and resource bounds.
Internal tool bugs must panic rather than become ordinary tool failures.

This is review-driven repair, not completion of a top-level TODO item.

## Related code

- `docs/spec.md` — Session lifecycle and recovery, workspace operations,
  concurrent subagents, and language intelligence contracts.
- `eng/architecture.md` — Immutable activation/turn inputs, authoritative
  session records, checkpoint projections, and transport ownership.
- `internal/agent/state.go`, `store.go`, `agent.go` — Record folding,
  copy-on-write publication, checkpoint writing, callbacks, and turn claims.
- `internal/agent/loop.go`, `subagent.go`, `config_options.go` — Provider
  request ownership, dispatch invariants, permission progress, and selections.
- `internal/settings/settings.go`, `resolve.go`, `internal/agent/language.go`,
  `internal/lsp/lsp.go` — Validated process definitions and activation-scoped
  language queries.
- `internal/lsp/client.go`, `evals/internal/eval/client.go` — Handwritten RPC
  clients; pinned jrpc2 v1.3.5 supplies correlation and callback/cancel hooks.
- `internal/tools/grep.go`, `shell.go`, `internal/workspace/workspace.go`,
  `evals/internal/eval/gateway.go` — Bounded scanning, delegated terminal
  access, temporary names, and provider proxying.

## Decisions

- Delete checkpoints completely, including unreachable recovery projections.
  Fold and validate every authoritative record, including compaction records.
  Checkpoint-bearing logs become unsupported; add no migration or legacy reader
  and do not delete existing logs. Retained record shapes remain unchanged.
  Measure long-session load time and allocations before and after removal.
- Immutable payloads may be shared internally. Keep writable ownership at
  `settings.Profiles.Select` and other caller-facing boundaries; keep fresh
  outer slices before `slices.DeleteFunc`, truncation followed by append, or
  operations that can overwrite a retained snapshot. Copy suspension decisions
  and pending generations, not its immutable provider payload. Execution results
  are immutable; mutable maps still require independent successors.
- Settings validates and returns `[]lsp.Definition`; remove
  `ResolvedLanguageServer` and the agent conversion. LSP consumes trusted,
  normalized definitions without sorting or revalidating them. Retain genuine
  workspace/process failures and path-extension normalization for query input.
- Move the cancellable language-query gate into `lsp.Manager`, covering each
  complete public query, including synchronization and result decoding. Keep
  startup/close synchronization; acquire the gate once, not recursively through
  shared query helpers. The session owns the manager directly.
- Use existing jrpc2 clients, not a shared Ox RPC framework. LSP keeps a bounded
  channel adapter enforcing the 8 KiB header and 4 MiB message limits, sends
  `$/cancelRequest` through `OnCancel`, and retains process shutdown ownership.
  Configure unsupported server callbacks to receive method-not-found replies.
- Evaluation keeps line framing with a channel decorator recording raw sent and
  received messages. Permission policy is frozen per call and callback counters
  are synchronized. Account for preceding `session/update` messages before
  returning a call result. ACP cancellation sends `session/cancel`; the prompt
  RPC stays alive through the existing two-second deadline grace, rather than
  disappearing when the run context expires.
- Scanner preserves the current 4 MiB raw-line bound, including LF/CRLF bytes,
  not a newly expanded content limit. Allow scanner lookahead overhead, enforce
  the actual bound in splitting, and retain UTF-8 checks and stopped-scan notes.
- The authoritative-exchange redesign and MCP HTTP cancellation tracker are
  outside this plan; their deletions were not established by the review.

## Implementation plan

- In `internal/agent/state_test.go`, add representative long-log fold/load
  benchmarks alongside `BenchmarkCommitCopyAtSessionSize`; establish the
  checkpoint-era baseline before changing persistence.
- Remove checkpoint constants, payloads, constructors, restoration validators,
  and checkpoint-only helpers from `state.go`. Make `foldRecords` apply all
  records in order. In `agent.go` and `store.go`, remove candidate encoding and
  `sinceCheckpoint` accounting; simplify append/read signatures where batching
  or extra return values existed only for checkpoints. Preserve sync-before-
  publication, corruption rejection, locking, and torn-tail repair.
- Convert mixed checkpoint tests in `state_test.go`, `store_test.go`, and
  `internal/e2e/session_test.go` into authoritative-fold tests. Replace
  `assertChangedFilesCheckpoint` with evidence from restored state or
  ACP-visible changed-file metadata. Delete only projection-specific assertions.
- In `state.go`, `loop.go`, `subagent.go`, and `config_options.go`, replace
  internal deep copies with immutable sharing or shallow container copies at
  actual mutation points. Handle refusal rollback without allowing later appends
  to overwrite older histories. Use `maps.Clone` for successor maps; remove
  `cloneStoredToolResult` and helpers left without ownership obligations. Keep
  settings' independently mutable selected profiles.
- In `newToolSet`, panic on a trusted tool with no executor before publishing
  the catalog. Remove `executeOne`'s blanket recovery and nil-executor fallback.
  Keep unknown provider-selected names and returned executor errors as ordinary
  tool failures. Replace the panic-recovery case in `agent_test.go` with a
  subprocess test proving an executor panic terminates dispatch.
- Make `randomID`, `allocateToolCallID`, and `temporaryName` return strings;
  remove entropy-error branches from callers, including memory operations. Keep
  collision reservations and filesystem failure handling. Share live-turn
  construction/release after explicit `claim`/`claimRecovery` admission checks;
  remove `activeTurn.id` and `nextTurn`, using active pointer identity instead.
  Remove `startFailed`, `snapshotsLocked`'s always-true flag, and the inbox copy
  before ownership transfer.
- Replace language definition conversion/revalidation with the single type and
  relocate the query gate in `settings.go`, `language.go`, `lsp.go`, and agent
  activation/close wiring. Update settings, manager, and activation tests to
  supply validated definitions and test invalid configuration at settings.
- Replace LSP envelopes, IDs, pending maps, response channels, dispatch, and
  write synchronization with jrpc2 in `client.go`. Retain protocol-specific
  initialization, diagnostics, document versions, encoding, deadlines, and
  graceful/forced shutdown. Ensure adapter closure unblocks pending reads and
  transport failure cannot leave a server process alive.
- Replace the evaluation client's RPC envelopes and matching loop with jrpc2.
  Retain artifact capture, permission outcomes, metrics, cancellation timing,
  and process/credential cleanup. In `gateway.go`, use `httputil.ReverseProxy`
  with explicit path/query rewriting, streaming flush, and the existing
  request-budget wrapper; do not forward credentials across redirects or
  manufacture forwarding authority from incoming headers.
- Remove outgoing-only callback validation from `agent.go` and unused request
  validators in `internal/acp` after tracing their callers. Preserve model
  argument checks and client-response validation at the callback boundary.
  Remove duplicate terminal-create response validation from `shell.go`; update
  terminal test doubles to obey the validated adapter contract.
- Replace `readBoundedLine` with the bounded scanner in `grep.go`; retain scan
  cancellation, early filename-match termination, and partial-match reporting.

## Test plan

- Agent folding/storage and shipped-binary restart tests: exact compacted
  provider history versus unchanged ACP replay, todo/selections/usage/changed
  files, open-turn compaction, permission generations, unknown dispatched
  outcomes, corruption, torn tails, and failure-before-publication. Add a log
  whose individual records fit 8 MiB but accumulated state exceeds it; finishing
  a small turn must succeed without any checkpoint records.
- Retained-snapshot tests across append, refusal then another turn, compaction,
  todo replacement, model/reasoning changes, plan-tool filtering, permission
  reissue/decision, and tool completion. Exercise concurrent model reads and
  child loops under the race detector; compare commit-copy allocations.
- Dispatch and claim tests: nil executor panics at construction, executor panic
  is not converted into a result, unknown tools and returned errors each emit
  one terminal failure, repeated release is safe, and close/cancel/recovery
  admission retains its synchronization.
- LSP adapter/helper tests: concurrent/out-of-order and late replies, exact
  header/body bounds, malformed envelopes, unsupported callbacks, cancellation
  IDs, diagnostics versions, authoritative text/position encodings, no restart
  after failure, and graceful/forced shutdown. Move the query-serialization
  regression to the manager and test cancellation while waiting for its gate.
- Evaluation fake-provider smoke tests: permission rejection, restart and
  multi-phase metrics, raw event artifacts, response-after-cancel grace and
  timeout, final updates counted before results, budget enforcement for retries
  and children, and process cleanup. Proxy tests cover incremental SSE before
  upstream EOF, path/query/header rewriting, cancellation, and upstream errors.
- `internal/tools/tools_test.go`: scanner boundaries at limit minus one, limit,
  and limit plus one with EOF/LF/CRLF, empty lines, invalid UTF-8, read failure
  after a match, early filename termination, and cancellation. Delegated
  terminal regressions keep malformed client responses rejected at the adapter
  and preserve kill-before-release behavior.

## Documentation updates

- Update `eng/architecture.md` to remove checkpoint ownership and guarantees,
  describe settings' validated LSP definitions and new dependency edge, put
  query serialization with the manager, and replace the Eta-client statement
  with the bounded jrpc2 adapter boundary.
