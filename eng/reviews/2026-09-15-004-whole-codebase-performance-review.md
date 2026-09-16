# Go performance review

- **Scope:** Whole Go codebase shipped by the `ox` binary: `cmd/ox`, and every
  `internal/` package (`acp`, `agent`, `credentials`, `lsp`, `mcp`,
  `openrouter`, `settings`, `shellrules`, `skills`, `trace`, `tools`,
  `workspace`), read as production source with their call paths. `evals/`,
  `integration/`, `internal/e2e`, and `eval-results/` fixtures were treated as
  test-only and not inventoried in depth.
- **Mode:** Specific-topic review.
- **Topic:** Performance — call frequency, algorithmic complexity, repeated
  work, allocations and copies, hot loops, and I/O patterns.

## Call-frequency framing

Frequencies established from the code before judging anything: JSON-RPC methods
run per client call; session activation and settings resolution run per session;
the turn loop, state commits, and token estimation run per provider request (a
few times per turn); the event adapter, SSE deltas, and streamed notifications
run per streamed model event; SSE line scanning, pipe output recording, and LSP
frame reading run per byte or per chunk. A finding without a plausible frequency
is labeled as a suspicion.

## Findings

### Medium — LSP position decoding re-splits the whole document per decoded range

`internal/lsp/protocol.go:117-127` — `textLine` does `strings.Split(text, "\n")`
over the entire document on every call. `decodePosition` calls it once per
position and `decodeRange` twice, so `decodeLocations`, `decodeDocumentSymbols`,
and `decodeWorkspaceSymbols` (`internal/lsp/protocol.go:210-251`, `:262-330`,
`:336-370`) pay O(locations × document bytes) with a full line-slice allocation
per range. The per-path `textCache` caches file contents but not the split.

Measured on a 1.6 MB document with a temporary benchmark (Apple M4, since
removed): 39.8 ms and 65 MB allocated per 100 decoded ranges; 389 ms and 655 MB
per 1000. A `lsp_references` answer of 1000 hits on a large file spends roughly
0.4 s and hundreds of megabytes of transient allocation in decoding alone,
inside a serialized session query (`sessionLanguages` holds the turn while this
runs).

Suggested fix: split each distinct document once per query (a
`map[string][]string` next to the existing `textCache`, or an offset index) and
make `textLine` slice that. The same fix removes the per-rune
`utf16.Encode([]rune{value})` allocation in `encodedRuneLength`
(`internal/lsp/protocol.go:132-146`) by encoding the line once instead of one
rune at a time.

### Medium — `session/list` reads and scans every session log in full per listing

`internal/agent/store.go:161-184` (`list` → `project` → `readRecords`,
`internal/agent/store.go:280-351`) reads each log with `io.ReadAll` up to
`maxSessionBytes` (64 MB) and decodes every record envelope, then
`foldListProjection` (`internal/agent/state.go:452-485`) uses only the creation
record, the first user message, and one timestamp. A client that pages through
`session/list` (page size 50, `internal/agent/agent.go:944`) pays O(total bytes
of all logs) per page, repeated as the store grows.

Not measured; the structural cost is the point. Suggested fix: read the first
record for identity/title and tail-read the last records for the timestamp (or
maintain the projection in the checkpoint), instead of materializing each log.

### Low — catalog sort comparator allocates per comparison

`internal/openrouter/catalog.go:113-117` —
`bytes.Compare([]byte(left.ID), []byte(right.ID))` converts two strings to
`[]byte` on every comparison of a several-thousand-model sort. `strings.Compare`
does the same ordering without the allocations. Runs once per catalog load, so
this is hygiene rather than a bottleneck.

### Low — durable-state commits are quadratic in session age

`internal/agent/agent.go:1300-1350` (`commitLocked`) clones the whole
`durableState` per record (`internal/agent/state.go:687-724`): the identity
maps, tool-execution map, configuration, and history slice header array. Each
commit is O(messages + tool calls + records), so a long session pays
quadratically over its record count. The checkpoint gate already bounds the
_disk_ cost with the same reasoning ("instead of growing with the square of the
session's age", `internal/agent/agent.go:1329-1332`), but the in-memory clone is
not covered by it. Unmeasured: commits are serialized and records are small, so
this likely matters only for very long-lived sessions; a benchmark folding a
multi-thousand-record fixture would settle it. Record as a watch item before
adding caching — the copy-on-write design is what keeps readers lock-cheap.

### Low — token estimation marshals the whole request per provider request

`internal/agent/compact.go:340-358` (`planRequestAdmission` →
`estimateProviderRequest` → `promptTokens`) JSON-serializes the full request
once per model request to estimate tokens. When compaction planning engages,
`planCompaction` and `compactionShapeFits` add further full-payload marshals per
candidate group. Compaction bounds requests near the context window and provider
latency dominates, so this is acceptable today; do not add caching here without
evidence the marshal shows up in a profile.

## Checked and found sound

- **SSE parsing** (`internal/openrouter/sse.go`): bounded scanner, per-event
  join and one copy to the assembler; event counts are token-scale, and the 16
  MB cap bounds a pathological line.
- **Streamed notifications** (`internal/agent/agent.go` `relayTurn`,
  `internal/agent/adapter.go`): one JSON-RPC notification per text delta is
  per-event traffic inherent to ACP streaming; the unbuffered event channel
  keeps ordering, and tool output is batched on a 100 ms tick with the head/tail
  bound amortized (`appendOutput`).
- **Trace** (`internal/trace/trace.go`): records are per tool/turn/provider
  event, not per byte; disabled turns skip the request marshal entirely
  (`tracedRequestBytes`).
- **Tool output capture** (`internal/workspace/stream.go`): per-chunk sanitize
  copies are bounded by pipe chunk size; spill writing is lazy; the mutex across
  the spill write is required to keep concurrent writers ordered.
- **grep/glob** (`internal/tools/grep.go`, `internal/tools/glob.go`): single
  pass, reusable line buffer, per-directory gitignore matchers, cancellation
  checked every 1024 lines.
- **read/edit/write** (`internal/tools/read.go`, `edit.go`, `write.go`): bounded
  at `MaxFileBytes`; edit splices rather than re-encoding the body; `read`'s
  `splitLines` allocates the whole file's lines even for a narrow window, which
  is fine at the 8 MB bound.
- **Concurrency** (locks, channels): no lock held across network I/O
  (`mcp.server.currentTools` documents and implements this); subagent wait uses
  a closed-channel signal, not polling.
- **Session load** (`internal/agent/store.go` `readRecords`,
  `internal/agent/state.go` `foldRecords`): checkpoint restore skips re-applying
  covered records; per-load cost is once per activation, not per request.

## Suspicions not settled

- The `durableState.clone()` per-commit cost above (Low) — needs a long-session
  fixture benchmark (`foldRecords` at 5k–50k records plus `commit` timing) to
  decide whether it justifies structural change. Given the repository's
  simplicity priority, only a measured problem should trigger one.
- `frameReader.Read` (`internal/mcp/transport.go:66-90`) scans its buffer per
  byte to bound wire frames, up to 2 MiB per message. MCP tool calls are rare
  and bounded; left alone deliberately.

## Checks

- `gofmt -l`, `go vet ./cmd/... ./internal/...`,
  `staticcheck ./cmd/... ./internal/...` — all clean.
- Temporary benchmark `decodeRange` over 100/1000 locations on a 1.6 MB document
  — measured as reported above; file removed after measurement.
- No repository benchmarks exist for the durable-state or listing paths; the two
  suspicions there are unmeasured by construction.

## Verdict

- **Performance:** one confirmed measured finding (LSP range decoding), one
  structural scaling finding (`session/list`), and three low-severity items. The
  hot per-byte and per-event paths (SSE, stream capture, notifications, trace)
  are simple and bounded, consistent with the repository's simplicity priority.
