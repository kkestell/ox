# Store errors, MCP checks, and conversation recovery

## Goal

Three fixes, landed together:

1. `claude-code-acp` reports session registry failures. A request that cannot
   lock, read, or write `acp-sessions.json` fails with an internal error
   instead of answering as if the change was saved.
2. `claude-code-acp` accepts any stdio MCP server whose `command` is an
   absolute path, with any arguments. The executable name allowlist and the
   shell metacharacter checks go, with `CLAUDE_ACP_MCP_ALLOWED_COMMANDS`.
3. The app's conversation follows the workspace watch's connection to `oxd`.
   After the user answers **Retry** in the "Cannot reach oxd" dialog and the
   watch reconnects, the viewed conversation is subscribed again instead of
   staying blank until the user picks another session.

Runner and Updates turn handling, stream blocks, idle handling, and the
composer are unchanged.

## Working tree

The plan starts from `a98dd07` ("Title tool calls with Claude's tool names").
The untracked `workflow-design-notes.txt` is the user's and stays untouched.
Nothing in this plan reverts, stashes, or checks out files; demonstrating that
a test fails without its fix is done with a temporary edit to the fixed code,
undone afterward.

## 1. Session registry errors

### Store (`crates/claude-code-acp/src/store.rs`)

`Store`'s public methods return `io::Result`:

```rust
pub fn get(&self, id: &str) -> io::Result<Option<Session>>
pub fn list(&self, cwd: Option<&Path>) -> io::Result<Vec<(String, Session)>>
pub fn create(&self, id: String, session: &Session) -> io::Result<()>
pub fn update(&self, id: &str, update: impl FnOnce(&mut Session)) -> io::Result<()>
```

`transaction` returns `io::Result<T>` and stops at the first failure:

```rust
fn transaction<T>(
    &self,
    operation: impl FnOnce(&mut Map<String, Value>) -> (T, bool),
) -> io::Result<T> {
    let _lock = self.lock()?;
    let mut entries = self.read()?;
    let (value, changed) = operation(&mut entries);
    if changed {
        self.write(&entries)?;
    }
    Ok(value)
}
```

The `readable` flag and the three `eprintln!` calls in `transaction` go.

- `create` saves the entry first and records the session's MCP servers in
  `mcp_servers` only once the save succeeded.
- `update` keeps its current meaning for a missing or unparsable entry: it
  changes nothing and returns `Ok(())`. It records the servers only after the
  save succeeded, as now.
- `read` keeps moving a registry that is not a JSON object aside to
  `acp-sessions.json.corrupt-<time>` and continuing with no entries; a failed
  rename is already returned as an error. `parse` keeps skipping entries it
  cannot read, and writes keep them. The tests `a_corrupt_store_is_set_aside`
  and `unreadable_store_entries_survive_updates` pass unchanged.
- The `Store` doc comment says each operation reads and rewrites the registry
  under an exclusive lock and fails if it cannot lock, read, or write it.

No retries, fsync, or recovery are added.

### Bridge (`crates/claude-code-acp/src/bridge.rs`)

A helper beside `invalid`:

```rust
fn store_error(error: io::Error) -> Error {
    Error::new(-32603, format!("session registry: {error}"))
}
```

`-32603` matches the bridge's other internal errors. Call sites:

| Call site | Change |
| --- | --- |
| `new_session` (`store.create`) | `.map_err(store_error)?` before the session is marked active, so a failed save returns the error and no ID. |
| `open` (`store.get`, `store.update` for MCP servers, `store.create` for a Claude history session) | Each `.map_err(store_error)?`. A registry that cannot be read is reported as such, not as "session not found". |
| `list_sessions` (`store.list`) | `.map_err(store_error)?`. |
| `require` | `self.store.get(id).map_err(store_error)?`, then the existing active filter and `session_not_found`. This covers `resume_session`, `send_config`, `save_config`, and `prompt`'s first check. |
| `save_config` (`store.update`) | `.map_err(store_error)?`. When Claude already accepted the change, the running Claude keeps it and the next Claude starts with the saved value; the request reports that the save failed. No rollback. |
| `prompt` title (`store.update`) | The prompt is already with Claude and its responder is gone. On error, `eprintln!("failed to save session {id}: {error}")` and send no `session_info_update`; on success, notify as now. |
| `turn_recorder` (`store.update`, `store.get`) | Runs after a turn with no request to answer. A failed update logs the same line; a failed get logs `failed to read session {id}: {error}` and sends no `session_info_update`. |

Logging to stderr is how the bridge already reports failures it cannot return
(`updates::notify`).

### README (`crates/claude-code-acp/README.md`)

In the Sessions paragraph, after "each change is written under a lock file
beside it.", add: "A request that cannot lock, read, or write the registry
fails with an internal error (`-32603`)."

### Tests (`crates/claude-code-acp/tests/protocol.rs`)

- `an_unusable_store_fails_session_requests`: `CLAUDE_ACP_STORE_DIR` is a
  regular file. A Claude transcript for `HISTORY_ID` is written under
  `empty-claude/projects/sample` with `cwd` set to the project, as in
  `lists_and_resumes_claude_history`. Then `session/new`, `session/list`, and
  `session/resume` of `HISTORY_ID` each answer with error code `-32603`.
  Without the fix, `session/new` returns an ID, `session/list` lists the
  transcript, and `session/resume` answers "not found".
- `failed_registry_reads_and_writes_fail_session_new`: two bridges on two
  stores. In the first, `acp-sessions.json` is a directory, so reading fails.
  In the second, the store holds `acp-sessions.lock` and `acp-sessions.json`
  containing `{}` and is made read-only (`0o555`), so locking and reading
  succeed and the write fails; the test restores `0o755` before the temporary
  directory is removed. `session/new` answers `-32603` in both. Without the
  fix, both return an ID.

Proof: temporarily restore the old `transaction` body (log each failure,
continue with no lock or an empty registry, return the value) behind the new
signature, run both tests, see them fail, and undo the edit.

## 2. MCP server checks

`runner.rs` passes MCP servers to Claude as JSON in `--mcp-config`, with
arguments as an array; no shell parses the command or its arguments. The
executable name allowlist checks only the file name, so it constrains nothing
a client could not route around. ACP v2 types `command` as `AbsolutePath`,
which the schema does not check on deserialization, so the bridge keeps the
absolute path check.

### Code

- `crates/claude-code-acp/src/validation.rs`: `mcp_command` checks only that
  the path is absolute, with the error `MCP server command must be an
  absolute path: <path>`. Its doc comment: "An MCP server command is an
  absolute path, as ACP v2 types it." `mcp_arg`, `has_metacharacters`, and the
  `env` import go. `cwd` is unchanged.
- `crates/claude-code-acp/src/bridge.rs` `parse_mcp`: the loop over
  `server.args` calling `mcp_arg` goes. The stdio-only check and
  `mcp_command` stay.

### Docs

- `crates/claude-code-acp/README.md`: the `CLAUDE_ACP_MCP_ALLOWED_COMMANDS`
  row leaves the Configuration table, and the table's columns are realigned.
  The MCP sentence becomes "The bridge forwards stdio MCP server definitions
  to Claude. An MCP server's `command` is an absolute path."
- `docs/acp-v2.md`, `session.mcp.stdio` row: the caveat becomes "The command
  is an absolute path."

### Tests

- `mcp_commands_are_allowed_absolute_paths` becomes
  `mcp_commands_are_absolute_paths`, checking that the relative command
  `node` is refused with `-32602`. The `/usr/bin/curl` and
  `/usr/bin/node;rm` cases go.
- `passes_stdio_mcp_servers_to_claude` uses the command
  `/opt/tools/mcp-filesystem` and the arguments
  `["--url", "https://example.com/mcp?a=1&b=2", "{\"root\":\"/tmp\"}"]`. The
  fake Claude writes the value after `--mcp-config` to `mcp-config.json` in
  its cwd, as other tests capture `$message`, and the test parses that file
  and asserts it equals
  `{"mcpServers":{"filesystem":{"command":"/opt/tools/mcp-filesystem","args":[...],"env":{"TOKEN":"test"}}}}`.
  This replaces the shell `case` pattern checks. Without the fix,
  `session/new` is refused, for the command name and for the arguments.

Proof: temporarily restore the `mcp_arg` loop and see the test fail; then
temporarily restore the allowlist check alone and see it fail again; undo
both.

## 3. Conversation recovery

### Current flow

- `useWorkspaces` (`ui/src/useWorkspaces.ts`) starts a watch. `Disconnected`
  restarts it at once. A watch that cannot start sends `Error`, and the user
  chooses **Retry** in the native dialog to start another.
- `useConversation` (`ui/src/useConversation.ts`) views the session and
  views it again on `ViewMessage::Disconnected`.
- `view_session` (`crates/ox/src/lib.rs`) returns without a message when
  `app.client()` or `client.subscribe` fails.

The watch and the view share one `Client`, so a dropped connection ends both.
When `oxd` cannot be started again, the watch shows the dialog while the
view's own retry fails silently. **Retry** reconnects only the watch, and the
conversation stays blank. Sending `Disconnected` from the view's failure paths
would make the view retry immediately and repeatedly, starting `oxd` each
time, alongside the dialog.

### Decision

The watch owns the connection: it already reports failure and offers
**Retry**. The view follows it. Each time a watch delivers its first listing,
the app is connected, and the conversation views its session again. The view
keeps no retry of its own.

- `view_session` failing to connect or subscribe sends nothing. A connection
  failure ends the watch too, which reports it and reconnects; a session
  closed meanwhile has nothing to show.
- A watch whose restart fails stops at the dialog until the user answers, so
  nothing loops, and nothing in the view adds timers.

### Code

`crates/ox/src/lib.rs`:

- `ViewMessage` goes. `view_session` takes `channel: Channel<Item>` and sends
  each item with `channel.send(item)`.
- The loop reads `while let Ok(event) = events.next().await`; both ends of the
  stream end the view.
- The doc comment on `view_session`: sends a session's conversation from its
  first event, replacing the previous view; with no session, ends the
  previous view. The view ends when the session closes or the connection
  drops, and a view that cannot connect or subscribe sends nothing; the
  workspace watch reports the connection, and the webview views the session
  again once the watch reconnects.

`ui/src/protocol.ts`: the `ViewMessage` type goes.

`ui/src/useWorkspaces.ts`:

- A `connection` state, starting at 0, counts the watches that have
  delivered a listing. In the effect, a local `listed` flag; the first `list`
  message on the channel sets it and calls `setConnection((c) => c + 1)`.
- The hook returns `{ workspaces, sessions, connection }`. Its doc comment
  says `connection` changes each time a watch connects.

`ui/src/useConversation.ts`:

- `useConversation(sessionId: string | null, connection: number)`. The
  `attempt` state goes. The effect's dependencies are `[sessionId,
  connection]`, and the `biome-ignore` comment says each connection views the
  session again.
- While `connection` is 0 the effect does nothing, so the first view waits
  for the first watch and the session is not viewed twice at startup.
- The channel is `Channel<Item>`; `onmessage` dispatches the item when
  `current`.
- The hook's doc comment: the items of a session's conversation, replayed
  from its first event and followed live, viewed again each time the app
  reconnects to `oxd`.

`ui/src/App.tsx`: `const { workspaces, sessions, connection } =
useWorkspaces();` and `useConversation(viewed, connection)`.

### Test

`ui/e2e/specs/connection.e2e.ts`, "views the session again after oxd
restarts":

1. `startSession()`, `openSession(cwd)`, `send("say 1 0")`, and wait for
   `chunk-1`.
2. Stop `oxd` with a new helper in `ui/e2e/helpers.ts`, `stopDaemon()`, which
   runs `pkill -TERM -f "oxd --socket <OX_SOCKET>$"`, the launch command
   `start_daemon` uses.
3. Wait for the session entry's `[aria-label="Exited"]`, which only the
   restarted `oxd`'s listing shows (restored sessions are exited), and for
   the conversation to have no agent text, which shows the view started
   again on the new connection.
4. Select the session, which loads it; the fake agent replays its log; wait
   for `chunk-1`.

Removing `connection` from `useConversation`'s dependencies makes this test
fail at step 3 or 4. The test passes on the current code too, because the
view's own retry reconnects when `oxd` restarts cleanly.

The **Retry** path has no automated test. Reaching it needs `oxd` to fail to
start and the user to answer the native dialog; the embedded WebDriver answers
only JavaScript dialogs, and Tauri defines `__TAURI_INTERNALS__.invoke` as
non-writable, so a test cannot answer `ask`. It is checked by hand with
`make dev`:

1. View a session with some conversation.
2. `chmod 555` the dev socket's directory, then stop the dev `oxd`. The
   restart cannot bind its socket and "Cannot reach oxd" appears.
3. `chmod 755` the directory and choose **Retry**.
4. The session list returns with the session exited and the conversation
   empty; selecting the session replays its conversation. Before the change,
   the conversation stays blank after selecting it.

The implementation report names the missing automated test for the
**Retry** path.

## Verification

After implementation, once:

- `make check-rust`: formatting, clippy, and workspace tests, including the
  new and changed `claude-code-acp` tests.
- `make check-ui`: Biome and `tsc` for the UI and e2e specs.
- `make e2e`: the existing specs and `connection.e2e.ts`.
- `make smoke-claude`: required by the `claude-code-acp` changes.
- The failure proofs above for each new or changed test, and the manual
  **Retry** check.
