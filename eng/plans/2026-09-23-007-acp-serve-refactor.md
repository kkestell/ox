# Move ACP request handler bodies out of serve

## Goal

`serve` in `src/acp.rs:642` is 183 lines. Almost all of it is request handler
bodies written inline as closures in the builder chain; the prompt handler
alone is about 100 lines. Move each nontrivial body into a `ServerState`
method so `serve` only wires handlers to methods, with no change in behavior:
the same responses, errors, ACP updates, their order, and the same shutdown.

## Related code

- `src/acp.rs:642` — `serve`: clones `ServerState` once per handler, registers
  `on_close` (drains session operations), and one closure per ACP request and
  the cancel notification.
- `src/acp.rs:278`, `:365`, `:468` — `new_session`, `load_session`,
  `delete_session`: the request logic the handlers already call. They keep
  their signatures, since tests call them directly.
- `src/acp.rs:226` — `compact_session`: the `/compact` work, which the prompt
  handler spawns. Keeps its signature.
- `src/acp.rs:426` — `send_available_commands`: sent after the new and load
  responses.
- `src/acp.rs:519` — `reply`: the existing helper for a handler whose result
  maps straight to a response.
- `src/acp.rs:146` — `dispatch`: chooses `/compact`, a skill invocation, or a
  user message from the prompt text.
- `src/acp/prompt.rs:64` — `prompt::run`: builds the prompt run future before
  anything is spawned; its errors become the prompt response error.
- `src/acp/operations.rs:32` — `OperationGuard`: moves into each spawned task
  so the session stays busy until the response is sent.
- `src/acp.rs:1641` — `clean_eof_cancels_the_prompt_and_drains_its_response`,
  `:1742` `shell_permissions_control_execution_and_save_results`, `:2172`
  `acp_shutdown_waits_for_shell_cleanup_saving_and_response`, `:2252`
  `transport_error_stops_the_shell_process_group`: the tests that run `serve`
  over a transport. None sends `session/new` or `session/load` through `serve`.

## Decisions

- **Startup and shutdown stay in `serve`.** Startup is only the per-handler
  state clones, and shutdown is the one-line `on_close`. Neither is worth its
  own function; the length comes from the handler bodies.
- **Handler methods are plain `fn`s on `ServerState`.** Every handler body is
  synchronous today: long work is handed to `connection.spawn`. Each method
  takes the request, the `Responder`, and the `ConnectionTo<Client>` it needs,
  and returns `Result<()>`, so each closure in `serve` is one call.
- **List, config, logout, initialize, and cancel stay inline.** Their closures
  are already one line.
- **One sender for ACP updates.** The load, `/compact`, and prompt paths each
  build the same closure that wraps an update in a `SessionNotification` for
  one session. A free function returns that closure; the three copies call it.
- **The prompt path splits at `dispatch`.** `start_prompt` keeps the checks
  in their current order (prompt text, operation guard, active session), then
  matches on `dispatch`. The `/compact` arm calls `spawn_compaction`; the skill
  and user message arms produce the turn start and skill hook source and call
  `spawn_prompt_run`. The OpenRouter client is still fetched after dispatch, so
  `/compact` on a session with nothing to compact still needs no API key.

## Naming

- `respond_to_new_session` — `ServerState` method: calls `new_session`,
  responds, then sends available commands.
- `respond_to_load_session` — `ServerState` method: takes the load operation
  guard, calls `load_session` with ACP updates for replay, responds, then sends
  available commands.
- `respond_to_delete_session` — `ServerState` method: takes the delete
  operation guard and replies with `delete_session`.
- `start_prompt` — `ServerState` method for a prompt request: the checks,
  `dispatch`, and the call to one of the two spawn methods.
- `spawn_compaction` — `ServerState` method: spawns `compact_session` for
  `/compact`, holding the operation guard until it replies.
- `spawn_prompt_run` — `ServerState` method: builds the prompt run with
  `prompt::run` from the turn start and optional skill hook source, then spawns
  it, holding the operation guard until it replies.
- `acp_update_sender` — free function in `src/acp.rs` taking a
  `ConnectionTo<Client>` and a `SessionId` and returning the closure that sends
  one ACP update for that session.

"ACP update", "prompt request", "prompt run", "operation guard", "active
session", "slash command", and "skill invocation" are as defined in
`eng/glossary.md`.

## Test plan

- Extend `clean_eof_cancels_the_prompt_and_drains_its_response` with a
  `session/new` request for `/workspace` before the prompts. Assert that its
  response arrives before the `available_commands_update` for the returned
  session ID. Also seed a separate saved session with a user message and send
  `session/load` for it through `serve`. Assert that its replayed
  `user_message_chunk` precedes the load response and that its
  `available_commands_update` follows the response. Keep the existing inactive
  session rejection and prompt cancellation checks. No test function is added.
- The other `serve` tests cover the inactive session rejection, a prompt run
  with streamed updates, shell permission requests, cancellation, clean EOF,
  shutdown draining, and transport errors. Run them unchanged.
- `compact_session`, `new_session`, `load_session`, and `delete_session` keep
  their direct tests unchanged.

## Implementation plan

1. Before changing any code, run `rsloc --items` and save its output as
   `eng/scratch/2026-09-23-007-baseline.txt`.
2. Extend `clean_eof_cancels_the_prompt_and_drains_its_response` as described
   and run it against the current code.
3. In `src/acp.rs`, add `acp_update_sender` beside `reply`.
4. Add `respond_to_new_session`, `respond_to_load_session`, and
   `respond_to_delete_session` to `ServerState`, moving each closure body out
   of `serve`.
5. Add `start_prompt`, `spawn_compaction`, and `spawn_prompt_run` to
   `ServerState`, moving the prompt closure body out of `serve`. Use
   `acp_update_sender` in the load, `/compact`, and prompt paths.
6. Reduce each moved closure in `serve` to one call. Run `cargo test acp`,
   then the full validation required by `AGENTS.md`: `cargo fmt --all -- --check`,
   `cargo test --all-targets --all-features`, `cargo build --all-features`,
   `cargo clippy --all-targets --all-features -- -D warnings`,
   `python3 -m unittest discover -s examples/skills/goal/scripts`, and
   `python3 -m unittest discover -s examples/skills/careful/scripts`. Report
   any skipped or failed check.
7. Run `rsloc --items` again and save its output as
   `eng/scratch/2026-09-23-007-final.txt`. Add a Results section to this plan
   in the same form as the one in
   `eng/plans/2026-09-23-006-search-run-and-transcript-validation-refactor.md`:
   `serve` against `serve` and the seven new functions, and the Prod totals
   of `src/acp.rs` and the crate.
8. Inspect the complete test diff and report the test-function and test-code
   delta. Mark the narrowed `serve` item in `eng/todo.md` done with a pointer
   to this plan. Connection setup and shutdown remain in `serve` as decided
   above.

## Results

Measured with `rsloc --items` before and after the change. The raw output is
in `eng/scratch/2026-09-23-007-baseline.txt` and
`eng/scratch/2026-09-23-007-final.txt`.

| Area | Prod before | Prod after | Highest Cog before | Highest Cog after |
| --- | ---: | ---: | ---: | ---: |
| `serve` → `serve` and its seven new functions | 183 | 230 | 20 | 4 |

`serve` itself is now 75 lines with a cognitive score of 0; the largest new
functions are `start_prompt` and `spawn_prompt_run`, 44 lines each.

| File | Prod before | Prod after | Cog before | Cog after |
| --- | ---: | ---: | ---: | ---: |
| `src/acp.rs` | 758 | 805 | 53 | 43 |
| Crate | 6342 | 6389 | — | — |

The test suite has the same 96 test functions. Test code in `src/acp.rs` grew
by 37 lines: `clean_eof_cancels_the_prompt_and_drains_its_response` now sends
`session/new` and `session/load` through `serve` and pins the order of their
responses, replayed updates, and available commands.

`spawn_compaction` and `spawn_prompt_run` take the operation guard and
cancellation as the pair `try_prompt` returns, and `spawn_prompt_run` takes the
turn start and skill hook source as the pair `dispatch` produces, to stay under
Clippy's argument limit without an `allow`.
