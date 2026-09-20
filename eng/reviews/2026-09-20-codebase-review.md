# Codebase review

Date: 2026-09-20

## Scope

The whole working tree: nine Rust source files totalling about 3,960 lines,
their in-tree tests, `Cargo.toml`, `README.md`, and
`eng/ox-architecture-design.md`. This is a corpus review rather than a diff
review, so the current `rust` branch is assessed as it stands. It reviews the
behavior that is implemented now; features explicitly left for later by the
architecture document are not treated as defects.

Topics selected by the behavior and risks actually present: correctness,
concurrency and cancellation, error handling, provider-boundary validation,
architecture, performance, dependencies, and testing. There is no `unsafe`
code, so that lens does not apply.

## Findings

### Medium: a settlement write failure can be followed by misleading updates and then masked

Source: `src/acp/prompt.rs:313-360`

`Prompt::settle` correctly fills missing tool outcomes before attempting to
commit the pending batch. If that commit fails, however, it changes `exit` to
`Exit::Storage` and continues into the notification loop:

```rust
if !matches!(exit, Exit::Storage(_))
    && let Err(error) = self.commit()
{
    exit = Exit::Storage(...);
}
if !matches!(exit, Exit::Delivery(_)) {
    // terminal updates are still attempted
}
```

This produces two concrete problems:

1. The client can receive terminal tool states for a batch that was not
   persisted and will disappear on reload.
2. If one of those notification attempts also fails, line 345 replaces the
   storage failure with `Exit::Delivery`, losing the persistence failure that
   the architecture explicitly says must not be masked.

The path is reachable through `Exit::Cancelled` or `Exit::ModelCallLimit` with
a pending batch, or through a delivery failure that settles an accepted batch
with unexecuted calls, when the settlement transaction then fails. The existing
failed-append test uses a zero-call final answer, so `unexecuted` is empty and
does not exercise this branch.

Keep the commit result separate from the original exit. Send the synthetic
terminal updates only after the settlement commit succeeds, and return the
storage error without allowing a later delivery attempt to overwrite it. Add
a regression test with a pending multi-call batch, cancellation after the
first result, and an `append_batch` failure.

### Medium: the keyring read stalls the entire connection

Source: `src/acp.rs:51-62`, called from `src/acp.rs:65` and `src/acp.rs:75`

`model_client` calls the operating-system keyring synchronously inside a
request handler while holding the model mutex. The protocol crate drives all
handlers and all spawned tasks as one `FuturesUnordered` set on a single task
(`process_stream_concurrently` in the crate's `util.rs`), with no thread
offload anywhere. A blocking call inside any handler therefore stalls the
whole connection, including `session/cancel` notifications and unrelated
sessions.

Evidence: with the keychain prompting, a live `initialize` succeeded and the
following `session/new` produced no response at all within 90 seconds, neither
a result nor an error. This matches the hang already recorded for this project.

Remedy: wrap the credential read in `tokio::task::spawn_blocking`. That makes
`model_client` async and ripples into `new_session` and `load_session`.
Reading the key once at startup is smaller, but gives up picking up a key
saved by `ox auth login` without a restart, which the doc comment marks as
deliberate.

### Medium: the stream assembler accepts contradictory tool-call fragments

Sources: `src/model.rs:298-310`, `src/model.rs:416-429`,
`src/sessions.rs:85-112`

`ToolCallDelta.index` is `#[serde(default)]`, so it falls back to zero when
absent. For a given index, `Assembly::tool_call` unconditionally replaces any
earlier nonempty call ID and function name while concatenating argument
fragments. Final validation checks only that the assembled IDs and names are
nonempty and that final IDs are unique.

Two fragments that omit the index therefore both land on key 0, where the ID
and name are overwritten and the arguments concatenated, and the result passes
validation as one call. The damage is a fidelity loss rather than an execution
hazard: the concatenated arguments are almost always invalid JSON, so
`tools::execute` returns a failed result rather than running anything, but two
requested calls silently collapse into one and the model receives a single
failure instead of two results.

Make tool-call assembly fallible. Require an index, retain the first nonempty
ID and name for each index, and reject later conflicting values. Argument
fragments may continue to append. Add fixture cases for a missing index and
for conflicting IDs and names at one index, asserting that no completion is
accepted.

### Low: `initialize` echoes whatever protocol version the client sent

Source: `src/acp.rs:170`

`InitializeResponse::new(initialize.protocol_version)` replies with the
client's number rather than the newest version the agent supports. Version
negotiation is the client's decision, and it needs the agent's real answer.

Evidence, live:

| Client sent | Agent replied |
| --- | --- |
| 0 | 0 |
| 2 | 2 |
| 99 | 99 |

Version 0 is documented in the schema crate as a pre-release that should be
treated as unsupported, and 99 does not exist. A client speaking a future
version would be told Ox speaks it, then served v1 semantics.

Remedy: reply with `ProtocolVersion::V1`, which the schema crate also exposes
as `LATEST`.

### Low: ACP logout reports success while the environment key still authenticates

Source: `src/acp.rs:121-129`, `src/auth.rs:16-21`

`logout` clears the cached client and deletes the keyring entry, then returns
success. `api_key` checks `OPENROUTER_API_KEY` first, so the next request
re-authenticates from the environment and the agent stays logged in. The
terminal path at `src/main.rs:68` warns about exactly this; the ACP path is
silent.

Remedy: either fail the request when the variable is set, explaining that it
takes precedence, or accept the gap deliberately and note it where the
capability is advertised.

## Settled questions

**Null assistant content is not a defect.** `src/model.rs:136` encodes an
assistant message with empty text as `"content": null`. That state is
reachable and committed: a `content_filter` finish with no text produces
`ModelStop::Refused`, and `converse` commits the batch before returning the
exit. Whether a provider accepts `content: null` with no accompanying
`tool_calls` was tested directly by storing such a message in a real session
and prompting again. OpenRouter accepted it and the prompt returned
`end_turn`. The stored transcript confirms the message was in the history sent.
No change needed.

## Checks run

- `cargo test --all-targets`: 38 passed.
- `cargo clippy --all-targets -- -D warnings`: clean.
- `git diff --check`: passed.
- `cargo fmt --all -- --check`: failed on formatting-only differences in
  `src/acp/operations.rs` and `src/acp.rs`.
- Live stdio session against OpenRouter using the key from `.env`:
  `initialize`, `session/new`, `session/prompt` with a real `get_weather` tool
  call and `end_turn`, `session/list`, and `session/load` with replay.
- Live negative cases: null-content continuation, cross-model reasoning
  metadata, and repeated prompts against the broken session.
- Connection-reuse measurement in a scratch project pinned to the same
  `reqwest` version and features.
- TLS handshake timing against `openrouter.ai`.
- Keyring: `keyring` 4.2.0 resolves the default `v1` feature, which enables the
  native macOS keychain store, so `src/auth.rs` is durable as written.
- Protocol crate source read to establish the single-task dispatch model.

## Coverage gaps

No Zed client was driven, so the terminal authentication method marked by the
`TODO` at `src/acp.rs:160` is still unverified. Behavior on Linux and Windows
keyring backends was not exercised. The connection-reuse measurement used a
local server modelling OpenRouter's framing rather than counting sockets
against the live host. Concurrent multi-session load was not tested. No paid
inference beyond the live sessions listed above was performed.

## Verdict

The design is sound and matches its own brief. Concrete types throughout, one
settlement path in the prompt loop, closed-batch validation owned by the store,
per-session admission with a clear guard, and tests placed at boundaries that
will survive refactoring. For a codebase this size the test coverage is
unusually well targeted.

The findings cluster at the edges rather than the core. The `MODEL` finding is
the one worth acting on now: it turns a routine constant edit into silent,
permanent, self-amplifying data damage, and the fix is smaller than the code it
replaces. Settlement and stream acceptance are the two hardest boundaries and
should be fixed before effectful tools are introduced. The connection-reuse and
keyring findings are straightforward and bounded. The two low findings are
protocol politeness and can wait.
