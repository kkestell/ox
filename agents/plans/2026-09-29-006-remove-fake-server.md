# Remove the fake server

## Goal

`crates/ox-fake-server/` is 856 production lines that exist only to test the Ox
client. Delete it and run every client test against the Ox server instead, with
model requests going to the scripted OpenRouter test fixture that the server's
own tests already use. When done, the crate is gone, the client tests exercise
the server that `ox` ships with, and production code is about 825 lines smaller.

## Related code

- `crates/ox-fake-server/src/lib.rs`, `main.rs` — the crate to delete.
- `crates/ox/src/acp.rs` `tests` — `with_session`, `turn`, and the ACP tests
  that script the fake server by prompt text (`stream`, `tools`, `running`,
  `reject`, `fail`, `title`, `unloadable`).
  `rejects_an_unsupported_protocol_version` already builds a minimal agent
  inline, which is the pattern for the one test the Ox server cannot serve.
- `crates/ox/src/tui.rs` `tests` — terminal logic tests that use `with_session`
  and assert the fake server's config options (`gemma`, `deepseek`, `pace`,
  `_meta` prices).
- `crates/ox/tests/tui.rs` — the tmux tests. `Tmux::new` points a `servers`
  entry at the `ox-fake-server` binary.
- `crates/ox-server/src/openrouter.rs:877` `fixture` — the scripted OpenRouter
  test fixture: `Server::start`, `Server::routed`, `Reply`, `CATALOG`, `NOW`,
  `DEFAULT_MODEL`, and the reply helpers. It is `#[cfg(test)] pub(crate)`.
- `crates/ox-server/src/openrouter.rs:189` `catalog` — installs `CATALOG` on
  first use under `#[cfg(test)]`.
- `crates/ox-server/src/openrouter.rs:162,194,332` — the three uses of
  `ENDPOINT`.
- `crates/ox-server/src/acp.rs:997` `serve` — serves one ACP connection over any
  transport; `serve_stdio` is its only production caller.
- `crates/ox-server/src/acp.rs:1220` `test_settings`, `no_home`, `state_over` —
  how the server tests build a `ServerState`.
- `crates/ox-server/src/sessions.rs:672` `SessionStore::in_memory`.

## Decisions

- **In-process tests serve the Ox server through a `test-support` feature.**
  `ox-server` gains a `test-support` feature. The OpenRouter fixture and a new
  ACP fixture compile under `#[cfg(any(test, feature = "test-support"))]`, so
  rsloc counts them as test code. `crates/ox` enables the feature through a
  dev-dependency on `ox-server`. `SessionStore::in_memory` and the fixture
  catalog in `catalog` move to the same condition.
- **The tmux tests run the real `ox` binary with its bundled server.** The
  harness drops the `servers` entry, so `ox` launches `ox acp` as a user's
  install does. The mock OpenRouter server runs in the test process on a Tokio
  runtime the harness owns. `ox acp` reaches it through a new environment
  variable, `OX_OPENROUTER_ENDPOINT`, which replaces `ENDPOINT` wherever it is
  set. This is the only production code the change adds (about four lines). The
  API key comes from `OPENROUTER_API_KEY` and the database from `OX_DATA_DIR`,
  both of which the server already reads.
- **The tmux catalog is shifted to the current time.** `ox acp` fetches the
  catalog at startup and filters it by the real clock. The first reply of every
  tmux script is the fixture catalog with each `created` moved by `now - NOW`,
  so the same four models pass the filter on any date.
- **Each test scripts its model replies up front.** A client session's model
  requests all share one first message, so `Server::start` answers them in
  order. A test lists one reply per model request its prompts cause, in prompt
  order. Tests with subagents use `Server::routed`, keyed by each subagent's
  task.
- **Guarantees the Ox server cannot produce are rewritten or dropped:**
  - Simultaneous permission requests come from two subagents that each run a
    shell command in Ask mode. That is the only way the Ox server sends two at
    once.
  - A permission request that arrives after cancellation is dropped from the
    cancellation test. The Ox server never sends one.
  - Resume without advertised list, load, and close capabilities uses a minimal
    inline agent, as `rejects_an_unsupported_protocol_version` does.
  - The `options` and `pace` scripts (server-side option changes and an
    uncategorized option) have no Ox equivalent. The status line test uses the
    settings file and the mode shortcut instead.
  - The resume test no longer claims to follow list pages. The Ox server returns
    one page.
  - The server-failure tmux test kills the `ox acp` process. The harness finds
    it as the child of the client, which is the child of the tmux pane's
    process.
- **Tmux tests work in a temporary workspace.** The harness creates `workspace/`
  under its root and runs `ox --dir` on it. Its settings file sets `model` to
  `DEFAULT_MODEL`, because the built-in default is not in the fixture catalog.
  Tools run for real there, so the rendering test creates its files first.

## Naming

- `test-support` — the `ox-server` feature that exposes the test fixtures to
  `crates/ox` tests.
- `ox_server::fixture` — the public test module: the OpenRouter fixture
  re-exported, plus `serve_connection`.
- `serve_connection(openrouter, transport)` — serves one ACP connection from an
  in-memory session store, the test settings, and a home directory that does not
  exist, with model requests going to `openrouter`. Lives in
  `crates/ox-server/src/acp.rs` in a `fixture` module.
- `echo_reply()` — a reply that answers `you said: <the latest user message>`,
  replacing the fake server's default script.
- `catalog_reply()` — the fixture catalog as a `GET /models` reply, shifted to
  the current time.
- `OX_OPENROUTER_ENDPOINT` — the OpenRouter API base URL when set.
- Transcript, turn, session mode, subagent, skill, and composer as defined in
  `agents/glossary.md`.

## Test plan

Each existing guarantee keeps its owning test. Only the scripting and the
expected text change, unless a line below says otherwise.

`crates/ox/src/acp.rs`:

- `multiple_prompts_share_one_session_and_stream_in_order` — a streamed reply of
  three text deltas, then `echo_reply()`.
- The resume test, renamed
  `resume_lists_and_loads_a_different_session_after_close` — the session title
  is the one the Ox server derives from the first prompt. The commands are the
  Ox server's (`compact`). The config options are model, effort, and mode.
- `failed_load_can_be_followed_by_another_selection` — loads a session ID the
  store has never seen.
- `resume_requires_advertised_list_load_and_close_capabilities` — an inline
  agent that answers initialize with no session capabilities, plus a new
  session.
- `simultaneous_permissions_keep_their_supplied_option_ids` — the main agent
  starts two subagents and waits. Each subagent route runs one shell command and
  then answers. Option indexes follow the Ox server's permission options.
- `cancellation_answers_pending_and_late_permissions_before_next_prompt`,
  renamed `cancellation_answers_pending_permissions_before_next_prompt` — one
  pending shell permission. It no longer has a late request.
- `running_turn_accepts_cancel_without_a_permission_request` and
  `a_prompt_during_a_turn_cancels_it_and_is_sent_after_it_finishes` — a
  `Reply::Hang` whose prefix streams one text delta.
- `rejected_and_failed_turns_allow_another_prompt` — one model request that
  answers an error status, and one that streams text and then ends without a
  completion.
- `server_exit_releases_pending_work` — spawns `serve_connection` on one end of
  a duplex channel and aborts it while a shell permission is pending.

`crates/ox/src/tui.rs`: the model picker, favorites, effort, and mode tests use
the fixture catalog's models, names, prices, context limits, and efforts. The
approval tests use shell calls from the fixture.

`crates/ox/tests/tui.rs`: every tmux test passes its replies to `Tmux::new`.

- The resume picker shows the derived session title and today's date.
- The model picker shows fixture catalog rows, and the saved favorites are
  fixture model IDs.
- The rendering and mouse-wheel tests create `a.tally` and `b.tally`. They run
  in Auto mode and script a reasoning delta, `shell` with `ls`, `read_file`, a
  background `shell`, `apply_patch`, and one subagent's answer.
- The status line test gets its settings from the settings file and its usage
  from a `usage` chunk.
- The ghost-text test writes a `tally` skill under `~/.config/ox/skills`.
- The server-failure test kills `ox acp` and expects `EXIT_1`.
- `invalid_startup_does_not_launch_a_server_or_change_terminal_mode` is
  unchanged.

## Implementation plan

1. `crates/ox-server/Cargo.toml`: add `[features] test-support = ["tokio/net"]`.
2. `crates/ox-server/src/openrouter.rs`:
   - Add `endpoint()`, which reads `OX_OPENROUTER_ENDPOINT` and falls back to
     `ENDPOINT`. Use it in `fetch_catalog` and `Client::new`.
   - Change `fixture` to `#[cfg(any(test, feature = "test-support"))] pub mod`,
     and give the catalog line in `catalog` the same condition.
   - Add `echo_reply` and `catalog_reply` to `fixture`.
3. `crates/ox-server/src/sessions.rs`: give `SessionStore::in_memory` the same
   condition and make it `pub`.
4. `crates/ox-server/src/acp.rs`:
   - Add `#[cfg(any(test, feature = "test-support"))] pub mod fixture` with
     `serve_connection`.
   - Move `test_settings` and `no_home` there, and have the ACP tests import
     them.
5. `crates/ox-server/src/lib.rs`: add
   `#[cfg(any(test, feature = "test-support"))] pub mod fixture`, which
   re-exports `openrouter::fixture::*` and `acp::fixture::serve_connection`.
6. `crates/ox/Cargo.toml`: replace the `ox-fake-server` dev-dependency with
   `ox-server = { workspace = true, features = ["test-support"] }`.
7. `crates/ox/src/acp.rs` tests:
   - `with_session` takes the replies. It starts a fixture `Server`, creates a
     temporary workspace, spawns `serve_connection` on one end of a duplex
     channel, and runs the client on the other.
   - Rewrite the tests as the test plan describes.
8. `crates/ox/src/tui.rs` tests: pass replies to `with_session` and use the
   fixture catalog's values.
9. `crates/ox/tests/tui.rs`:
   - `Tmux::new(replies)` owns a Tokio runtime and starts the fixture server
     with `catalog_reply()` first. It writes the settings file and the
     workspace, and launches `ox --dir <workspace>` with `HOME`,
     `XDG_STATE_HOME`, `OX_DATA_DIR`, `OPENROUTER_API_KEY`, and
     `OX_OPENROUTER_ENDPOINT`.
   - Add `Tmux::kill_server`.
   - Rewrite the tests as the test plan describes.
10. Delete `crates/ox-fake-server/`. Remove it from the workspace `members` and
    `[workspace.dependencies]` in `Cargo.toml`, and update `Cargo.lock`.
11. `Makefile`: `e2e` drops the `cargo build --workspace` line, since
    `cargo test` builds the `ox` binary.
12. Report the production-line change from `rsloc crates` before and after.

## Documentation updates

- `AGENTS.md`: delete the `crates/ox-fake-server/` line.
- `agents/testing.md`: `make e2e` runs the tmux tests against the Ox server and
  the scripted OpenRouter test fixture, not the fake server.
