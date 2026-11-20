### Behaviors

| ID | Behavior | Surface | Code | Test |
| --- | --- | --- | --- | --- |
| B1 | The daemon accepts newline-terminated JSON-RPC 2.0 requests over a Unix socket and sends responses and notifications on the same connection. | protocol | internal/server/server.go:80 | internal/server/methods_test.go:115 |
| B2 | Requests without an ID are ignored, while IDs that are strings, numbers, or null are echoed in responses. | protocol | internal/server/methods.go:38 | untested |
| B3 | Malformed JSON returns -32700, invalid envelopes or batches return -32600, and unknown methods return -32601. | protocol | internal/jsonrpc/jsonrpc.go:47; internal/server/methods.go:66 | untested |
| B4 | Parameter validation errors return -32602, missing workspaces or sessions return -32001, busy sessions return -32002, and nonpending approvals return -32003. | protocol | internal/server/methods.go:71 | internal/server/methods_test.go:34 |
| B5 | Other operation failures return -32000 with the underlying error message. | protocol | internal/server/methods.go:77 | internal/server/methods_test.go:200 |
| B6 | Every connected client receives workspace, session, and update notifications for all sessions without subscribing. | protocol | internal/server/server.go:132 | internal/server/methods_test.go:169 |
| B7 | Independent requests can complete in a different order from their arrival, and a slow session does not prevent another session from answering. | protocol | internal/server/server.go:101 | internal/server/live_test.go:163 |
| B8 | A connection is closed when its 1024-message outgoing queue fills or an individual write cannot complete within five seconds. | protocol | internal/server/conn.go:19 | untested |
| B9 | Disconnecting a client leaves its sessions and any pending approvals running. | protocol | internal/server/server.go:85 | untested |
| B10 | The socket defaults to ox/oxd.sock under XDG_RUNTIME_DIR or the state directory, and -socket overrides it. | protocol | internal/paths/paths.go:22; cmd/oxd/main.go:27 | untested |
| B11 | The daemon replaces a refused stale socket but refuses to overwrite a non-socket file or a socket served by another daemon. | protocol | cmd/oxd/main.go:109 | cmd/oxd/main_test.go:20 |
| B12 | The daemon stores its database at ox/ox.db under XDG_STATE_HOME or ~/.local/state unless -db overrides it. | ui | internal/paths/paths.go:15; cmd/oxd/main.go:28 | untested |
| B13 | The daemon reads ox/config.json under XDG_CONFIG_HOME or ~/.config unless -config overrides it, and uses its built-in configuration if the file is missing. | ui | internal/paths/paths.go:10; internal/config/config.go:46 | internal/config/config_test.go:11 |
| B14 | Invalid JSON, unknown configuration fields, trailing content, invalid defaults, unknown harnesses, and unsupported Claude effort levels prevent daemon startup. | ui | internal/config/config.go:53; cmd/oxd/main.go:73 | internal/config/config_test.go:11; cmd/oxd/main_test.go:54 |
| B15 | Each configured harness must have models and permission modes with nonempty IDs, and a model without efforts accepts an empty effort. | ui | internal/config/config.go:76 | internal/config/config_test.go:51 |
| B16 | The built-in catalog offers Claude Opus, Sonnet, and Haiku 5.5 with low through max efforts and manual, auto, acceptEdits, and plan permission modes. | ui | internal/config/default.json:3 | internal/config/config_test.go:11 |
| B17 | The built-in defaults are Claude Haiku 5.5, low effort, and acceptEdits permission mode. | ui | internal/config/default.json:47 | internal/config/config_test.go:11 |
| B18 | A protocol client can retrieve the complete offered harness catalog and defaults with harness/list. | protocol | internal/server/methods.go:50 | untested |
| B19 | workspace/create accepts an absolute path to an existing directory, cleans it, stores it, returns an ID, and announces the workspace. | ui | internal/server/methods.go:106 | internal/server/methods_test.go:128 |
| B20 | Registering an already registered cleaned path returns its existing workspace ID without creating another workspace. | ui | internal/server/methods.go:129 | untested |
| B21 | workspace/list returns workspaces and their sessions with IDs, paths or harness names, and creation timestamps in creation order. | ui | internal/server/methods.go:149; internal/store/store.go:88 | internal/server/methods_test.go:192 |
| B22 | Removing an empty workspace deletes its registration and broadcasts workspace/removed without deleting the directory. | ui | internal/server/methods.go:176 | internal/server/methods_test.go:212 |
| B23 | Removing a workspace with sessions fails with workspace has sessions. | ui | internal/server/methods.go:196 | internal/server/methods_test.go:199 |
| B24 | session/create requires a workspace and configured harness, model, effort, and permission-mode combination, and creates a parked session without starting its process. | ui | internal/server/methods.go:211 | internal/server/methods_test.go:34 |
| B25 | A client may specify a 32-hex-digit session ID or omit it to receive a generated ID. | protocol | internal/server/methods.go:222 | internal/server/methods_test.go:91 |
| B26 | Repeating creation with an existing ID in the same workspace returns existing:true without changing its settings, while using that ID in another workspace fails. | protocol | internal/server/methods.go:240 | internal/server/methods_test.go:71 |
| B27 | Creating a session broadcasts session/created with its session ID, workspace ID, harness, and creation time. | ui | internal/server/methods.go:273 | internal/server/live_test.go:177 |
| B28 | Workspaces, sessions, their settings, and recorded conversation events are loaded from SQLite after daemon restart, with sessions initially parked. | ui | internal/server/server.go:44; internal/store/store.go:88 | internal/server/methods_test.go:232 |
| B29 | session/get returns current settings, execution state, active prompt ID or null, pending approvals in arrival order, and ordered conversation history. | ui | internal/server/methods.go:285; internal/claude/agent.go:190 | internal/claude/prompt_test.go:136 |
| B30 | A session snapshot follows updates already queued before its response and precedes updates not reflected in it. | protocol | internal/server/session.go:13; internal/server/methods.go:296 | untested |
| B31 | session/send requires a session ID and nonblank text, preserves the submitted text, starts a parked harness if necessary, and returns a message ID after the write succeeds. | ui | internal/server/methods.go:314 | internal/server/methods_test.go:142 |
| B32 | A running, action-required, queued, or configuring Claude session refuses another prompt as busy. | ui | internal/claude/prompt.go:16; internal/claude/agent.go:186 | internal/claude/prompt_test.go:87; internal/claude/configure_test.go:40 |
| B33 | A successfully written prompt produces a prompt update and an immediate running state update bearing its message ID. | ui | internal/claude/prompt.go:20 | internal/claude/prompt_test.go:87 |
| B34 | Claude receiving an idle report before it takes a queued prompt does not release that prompt or allow another send. | ui | internal/claude/prompt.go:53 | internal/claude/prompt_test.go:104 |
| B35 | A prompt remains reserved after its result until Claude reports idle after taking it. | ui | internal/claude/prompt.go:46; internal/claude/prompt.go:59 | internal/claude/prompt_test.go:238 |
| B36 | Text deltas, top-level tool calls and tool results, final results, and state changes are broadcast as timestamped session updates. | ui | internal/claude/agent.go:115; internal/server/session.go:144 | internal/claude/prompt_test.go:169 |
| B37 | Nested subagent tool calls and results are omitted from the top-level update stream. | ui | internal/claude/agent.go:128 | internal/claude/prompt_test.go:169 |
| B38 | A tool result with multiple text blocks joins them with newlines and omits non-text blocks. | ui | internal/claude/messages.go:84 | internal/claude/prompt_test.go:174 |
| B39 | Updates from a prompt's own turn carry its message ID through the result, while later self-started turns omit that ID and the final idle update identifies the released prompt. | protocol | internal/claude/prompt.go:88; internal/claude/agent.go:166 | internal/claude/prompt_test.go:238 |
| B40 | A failed Claude result reports its result text, otherwise joined errors, otherwise its subtype, and an aborted result reports Interrupted. | ui | internal/claude/agent.go:149; internal/claude/messages.go:35 | internal/server/live_test.go:139 |
| B41 | Empty successful bookkeeping results do not produce result updates. | protocol | internal/claude/agent.go:157 | internal/claude/prompt_test.go:149 |
| B42 | Conversation history records prompts, text, top-level tools and results, final results, and exits but not state or approval updates. | ui | internal/server/history.go:18 | internal/server/history_test.go:11 |
| B43 | Consecutive text fragments for the same message are combined in history with the first fragment's timestamp, including in an in-progress snapshot. | ui | internal/server/history.go:18 | internal/server/history_test.go:20 |
| B44 | A history write failure is logged and the live update is still broadcast. | ui | internal/server/history.go:42; internal/server/session.go:144 | untested |
| B45 | Claude runs in the workspace directory with the selected model, effort, and permission mode, and without inherited ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN. | ui | internal/claude/claude.go:47 | internal/claude/claude_test.go:132 |
| B46 | Claude receives an appended instruction to lead with the big idea and use crisp, deadpan, military-efficient delivery and dry wit. | ui | internal/claude/claude.go:28 | untested |
| B47 | Claude startup waits at most one minute for initialization and reports startup errors, including the retained tail of stderr when necessary. | ui | internal/claude/claude.go:103 | internal/claude/claude_test.go:17; internal/claude/claude_test.go:87 |
| B48 | A new Claude conversation becomes resumable after its first nonempty or error result, and later process starts use its saved conversation ID. | ui | internal/claude/agent.go:149; internal/claude/agent.go:50 | internal/server/methods_test.go:327 |
| B49 | If Claude reports that a saved conversation no longer exists, the discovering send fails and records an exited notice, and the next send starts without the old context. | ui | internal/server/session.go:185 | internal/server/methods_test.go:327 |
| B50 | An unexpected Claude process exit produces an exited update and then a parked update, including when there is no active prompt. | ui | internal/claude/agent.go:198 | internal/claude/prompt_test.go:189 |
| B51 | The daemon stops a parkable process after five minutes by default, allows -park-after to change the period, and disables timed parking for a nonpositive period. | ui | cmd/oxd/main.go:30; internal/server/session.go:89 | internal/server/methods_test.go:115 |
| B52 | Claude is not parked while it has a pending prompt, configuration, approval, background task, or possible scheduled work. | ui | internal/claude/park.go:8 | internal/claude/park_test.go:8 |
| B53 | A resumed Claude session or a scheduling tool call stays unparkable until a Stop hook supplies an empty schedule list. | ui | internal/claude/agent.go:71; internal/claude/agent.go:128; internal/claude/park.go:13 | internal/claude/park_test.go:41; internal/server/live_test.go:244 |
| B54 | Stopping Claude sends SIGTERM and kills it if it has not exited after ten seconds. | ui | internal/claude/claude.go:255 | internal/claude/prompt_test.go:206 |
| B55 | SIGINT or SIGTERM shuts down the daemon and its sessions, and a second signal terminates it without waiting for shutdown. | ui | cmd/oxd/main.go:54 | cmd/oxd/main_test.go:82 |
| B56 | session/interrupt asks a non-idle Claude process to stop and succeeds without sending anything for an idle or parked session. | ui | internal/claude/prompt.go:36; internal/server/methods.go:364 | internal/server/live_test.go:139 |
| B57 | Claude tool permission requests are broadcast intact and remain available in the session snapshot until answered or withdrawn. | ui | internal/claude/approval.go:12; internal/claude/agent.go:190 | internal/claude/approval_test.go:12 |
| B58 | Multiple approval requests may be pending simultaneously and are identified by their request IDs. | ui | internal/claude/approval.go:29 | untested |
| B59 | An approval answer must have allow or deny behavior, an object updatedInput if present, and an array updatedPermissions if present, and its other fields are forwarded unchanged. | ui | internal/claude/approval.go:33; internal/claude/approval.go:74 | internal/claude/approval_test.go:12 |
| B60 | A successful approval answer removes the pending request and broadcasts approval_answered, while duplicate or late answers fail. | ui | internal/claude/approval.go:33 | internal/claude/approval_test.go:12 |
| B61 | A failed approval write leaves the request pending until a later cancellation or process exit. | ui | internal/claude/approval.go:40 | internal/claude/approval_test.go:112 |
| B62 | Claude cancellation, idle, or exit withdraws pending approvals and broadcasts approval_cancelled, while a result alone does not withdraw them. | ui | internal/claude/approval.go:50; internal/claude/prompt.go:53; internal/claude/agent.go:198 | internal/claude/approval_test.go:45; internal/claude/approval_test.go:68 |
| B63 | session/configure changes only supplied model, effort, or permission-mode fields and validates a changed model and effort together against the configured choices. | ui | internal/server/methods.go:383 | internal/server/methods_test.go:34 |
| B64 | Configuring a parked session stores and broadcasts its new settings for the next process. | ui | internal/server/methods.go:420 | untested |
| B65 | Configuring a running Claude process requires it to be idle and blocks other sends and configurations until the request completes. | ui | internal/claude/configure.go:28 | internal/claude/configure_test.go:40 |
| B66 | Claude applies changed model, effort, and permission mode in that order, waiting for each acknowledgement and retaining earlier accepted changes if a later one fails. | ui | internal/claude/configure.go:33; internal/claude/configure.go:71 | internal/claude/configure_test.go:40; internal/claude/configure_test.go:148 |
| B67 | An unanswered configuration control request stops Claude after one minute, and process exit causes the requested settings to be saved for the next process. | ui | internal/claude/configure.go:61; internal/server/methods.go:434 | internal/claude/configure_test.go:161; internal/server/methods_test.go:284 |
| B68 | If storing settings already applied to a running process fails, the configuration returns an error but clients still receive the settings the process uses. | ui | internal/server/methods.go:439 | internal/server/methods_test.go:243 |
| B69 | When Claude reports its own permission-mode change, the daemon adopts and broadcasts it, translating Claude's default mode to manual. | ui | internal/claude/agent.go:172; internal/server/session.go:168 | internal/server/methods_test.go:388 |
| B70 | Destroying a session stops its process, deletes its stored session and history, broadcasts session/destroyed, and leaves Claude's own transcript untouched. | ui | internal/server/methods.go:455; internal/store/store.go:44 | internal/server/methods_test.go:206 |
| B71 | If configured, the fake harness echoes a prompt as You said: followed by the input, streaming one space-delimited fragment every 50 milliseconds. | protocol | internal/fake/fake.go:58; internal/fake/fake.go:112 | internal/server/methods_test.go:115 |
| B72 | Interrupting the fake harness ends its current prompt with an error result containing Interrupted and an idle state. | protocol | internal/fake/fake.go:130 | untested |

### Suspected defects

- A daemon crash can lose the most recent streamed text fragments because history keeps them only in memory until another update arrives (`internal/server/history.go:18`); this can leave a reloaded transcript missing text already shown to the user.
- A failed history write only logs the error and discards the update from durable history while still broadcasting it (`internal/server/history.go:42`, `internal/server/session.go:144`); users can see conversation content disappear after reload or restart.
- During initialization, Claude output preceding the successful handshake is consumed without being delivered to the session (`internal/claude/claude.go:142`); a resumed conversation that starts a turn during startup can lose its early state or output.
- A queued prompt can receive a non-idle state from an unrelated automatic turn and then an idle report that is ignored (`internal/claude/prompt.go:53`); the snapshot can remain requires_action or another stale state while the queued prompt is still pending.

### Doc conflicts

- `docs/protocol.md:224` says recorded text survives daemon restarts, but the latest contiguous text remains only in memory until another update flushes it (`internal/server/history.go:18`), so an abrupt restart can lose it.
- `docs/claude-code.md:169` says undeclared hook callbacks receive an error, but every hook_callback is answered successfully without checking its callback ID (`internal/claude/approval.go:19`).

### Coverage

- `cmd/oxd/main.go`
- `cmd/oxd/main_test.go`
- `internal/agent/agent.go`
- `internal/claude/agent.go`
- `internal/claude/approval.go`
- `internal/claude/approval_test.go`
- `internal/claude/claude.go`
- `internal/claude/claude_test.go`
- `internal/claude/configure.go`
- `internal/claude/configure_test.go`
- `internal/claude/messages.go`
- `internal/claude/park.go`
- `internal/claude/park_test.go`
- `internal/claude/prompt.go`
- `internal/claude/prompt_test.go`
- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/config/default.json`
- `internal/fake/fake.go`
- `internal/jsonrpc/jsonrpc.go`
- `internal/paths/paths.go`
- `internal/server/client_test.go`
- `internal/server/conn.go`
- `internal/server/history.go`
- `internal/server/history_test.go`
- `internal/server/live_test.go`
- `internal/server/methods.go`
- `internal/server/methods_test.go`
- `internal/server/server.go`
- `internal/server/session.go`
- `internal/store/store.go`
- `docs/protocol.md`
- `docs/architecture.md`
- `docs/claude-code.md`
- `.agents/requirements/BRIEF.md`
