# Ox user-observable behavior: consolidated candidate list

Merged from four independent extractions (`client-sonnet.md`, `client-terra.md`,
`daemon-sonnet.md`, `daemon-terra.md`). Rows are ordered by code area only, for
tracing; grouping comes later. `Found by`: CS/CT = client Sonnet/Terra, DS/DT =
daemon Sonnet/Terra. A row found by one extractor was checked against the source
before it went in. Surfaces: `ui` (visible in the web page), `protocol` (only a
client of `oxd`'s socket sees it), `operator` (the person running `oxd`/`ox`),
`fake` (the echo harness used in tests).

## Behaviors

### Relay (`cmd/ox`)

| ID | Behavior | Surface | Code | Test | Found by |
| --- | --- | --- | --- | --- | --- |
| R1 | `ox` serves the web page on `-addr` (default `localhost:8080`) and reaches `oxd` on `-socket` (default the XDG socket path); a malformed `-addr` stops it at startup. | operator | cmd/ox/main.go:21-44 | untested | CS CT |
| R2 | `ox` starts without `oxd` running; the page then shows a connection error. | ui | cmd/ox/main.go:43-44,82-86 | untested | CS |
| R3 | Each page gets its own `oxd` connection, opened by its event stream and closed when the page closes the stream. | protocol | cmd/ox/main.go:81-114 | S (every spec) | CS CT |
| R4 | `/events` and `/rpc` answer 403 unless the request's Host is `localhost`, `127.0.0.1`, `[::1]`, or the `-addr` host; `/rpc` also refuses a cross-origin POST. The static page is served for any Host. | protocol | cmd/ox/main.go:57-79 | main_test.go:15,42 | CS CT |
| R5 | A request body over 1 MiB is refused with 413 and never reaches `oxd`; the page shows the error and keeps the prompt in the composer. | ui | cmd/ox/main.go:127-131 | main_test.go:66; session.spec.ts:162 | CS CT |
| R6 | When `oxd` cannot be reached, `/events` answers 502 with the connection error; an unknown connection ID on `/rpc` answers 404; a failed socket write answers 502. | protocol | cmd/ox/main.go:82-86,121-139 | untested | CS CT |

### Web page (`cmd/ox/web`)

| ID | Behavior | Surface | Code | Test | Found by |
| --- | --- | --- | --- | --- | --- |
| P1 | On each connection the page reloads the catalog, the workspaces, and every session's settings, state, history, and pending approvals. | ui | web/src/ox.tsx:329-354 | session.spec.ts:150,341 | CS CT |
| P2 | When the event stream fails, every pending request fails, and the page shows "Lost the connection to ox. Reconnecting.", or asks for a reload when the browser has closed the stream for good (as after `ox` refuses it). | ui | web/src/connection.ts:36-45; ox.tsx:395-400 | session.spec.ts:150 | CS CT |
| P3 | A request made while disconnected fails at once with "Not connected to ox". | ui | web/src/connection.ts:64 | untested | CS |
| P4 | While disconnected, Send and the settings controls are disabled; after a reconnect each session enables them once its own snapshot arrives, independent of other sessions. | ui | web/src/ox.tsx:182-185,315-326 | session.spec.ts:341 | CS CT |
| P5 | After a reconnect, loaded sessions update in place (view, scroll, typed text stay), sessions deleted meanwhile disappear, and new ones appear. | ui | web/src/ox.tsx:292-353 | session.spec.ts:150,341 | CS |
| P6 | Workspace and session creation and removal, setting changes, state, and conversation updates from any page or client appear live. | ui | web/src/ox.tsx:356-383 | session.spec.ts:341 | CS CT |
| P7 | The URL fragment holds the selected session's ID; loading the page selects that session; a malformed fragment is ignored. | ui | web/src/App.tsx:9-39 | workspace.spec.ts:6; session.spec.ts:150 | CS CT |
| P8 | The sidebar lists workspaces by their path's last component (full path as tooltip), in the order `oxd` lists them. | ui | web/src/Sidebar.tsx:110-114 | workspace.spec.ts:6,15 | CS CT |
| P9 | Under each workspace, sessions appear oldest first, labeled with the trimmed first line of their first prompt, or the first 8 characters of their ID before one exists, and marked with their state (`parked`, `idle`, `running`, `requires_action`). | ui | web/src/Sidebar.tsx:27-30,133-171 | session.spec.ts:101 | CS CT |
| P10 | Add workspace takes a path in a dialog; a failure shows in the dialog, which stays open with the path. | ui | web/src/Sidebar.tsx:16-25,60-79 | workspace.spec.ts:6 | CS CT |
| P11 | A workspace with no sessions offers Remove, behind a confirmation that the directory stays on disk; a failure shows in the dialog and leaves the workspace in place. | ui | web/src/Sidebar.tsx:46-56,95-123 | workspace.spec.ts:36,59 | CS CT |
| P12 | Removing a workspace discards its draft, including unsent text. | ui | web/src/App.tsx:47-51 | workspace.spec.ts:36,52 | CS CT |
| P13 | Each session offers Delete, behind a confirmation that names it and says its history is deleted; a failure shows in the dialog. | ui | web/src/Sidebar.tsx:34-44,80-94 | workspace.spec.ts:47 | CS CT |
| P14 | A session deleted from any page disappears with its composer text. | ui | web/src/ox.tsx:379-383 | workspace.spec.ts:36 | CS |
| P15 | Each workspace has one draft for a new session, which keeps its text and chosen settings across navigation until the page reloads. | ui | web/src/App.tsx:30-40; NewSessionView.tsx:9-14 | session.spec.ts:304 | CS CT |
| P16 | A draft starts with the configured defaults and offers every harness's models; switching to another harness keeps the permission mode if offered, else uses that harness's default (or first) mode. | ui | web/src/NewSessionView.tsx:11-30 | workspace.spec.ts:20 | CS CT |
| P17 | Submitting a draft creates the session with a page-chosen ID and the draft's settings, then sends the submitted text once; text typed after submitting stays in the composer. | ui | web/src/ox.tsx:186-231 | session.spec.ts:25 | CS CT |
| P18 | The page opens the new session if the draft is still open; if the user has moved elsewhere, the selection is left alone. | ui | web/src/App.tsx:54-57 | session.spec.ts:101 | CS CT |
| P19 | A failed creation shows its error in the draft, keeps the text and settings, and allows another submit; from another view it shows with a Reopen draft button. | ui | web/src/App.tsx:79-88; NewSessionView.tsx:42 | session.spec.ts:304 | CS CT |
| P20 | A first send that fails after creation shows its error in the new session and keeps the text for the user to send. | ui | web/src/ox.tsx:191-197 | session.spec.ts:82 | CS |
| P21 | If a creation reply is lost but the session exists, the page attaches the draft to it without sending, keeps the text, and says "Session recovered. Review its settings and send your prompt." | ui | web/src/ox.tsx:200-226; App.tsx:59-72 | session.spec.ts:304 | CS CT |
| P22 | A blank or whitespace-only prompt is not sent, with no message. | ui | web/src/ox.tsx:220,234 | untested | CS CT |
| P23 | Enter sends; Shift+Enter adds a newline; Enter during IME composition or a held, repeating Enter does not send. | ui | web/src/SessionView.tsx:10-15 | session.spec.ts:25 | CS CT |
| P24 | Send is enabled only while connected, the session is synced and idle or parked, and none of the page's own requests for it are in flight. | ui | web/src/ox.tsx:182-185 | session.spec.ts:117,184,341 | CS CT |
| P25 | A successful send clears the composer unless its text changed meanwhile; a failed send keeps the text and shows the error. Nothing resends automatically. | ui | web/src/ox.tsx:190-197 | session.spec.ts:25,82,150 | CS CT |
| P26 | Each session keeps its composer text across navigation and reconnects; reloading the page discards it. | ui | web/src/ox.tsx:176,267-285 | session.spec.ts:150,199 | CS CT |
| P27 | One session's pending or failing request leaves other sessions usable. | ui | web/src/ox.tsx:267-285 | session.spec.ts:199 | CS CT |
| P28 | A request's error shows in the view that made it and stays until that view's next request starts; a failure shows even if a later request started first. | ui | web/src/ox.tsx:146-161 | session.spec.ts:82 | CS CT |
| P29 | A session view shows the workspace path, harness, and session ID above the conversation. | ui | web/src/SessionView.tsx:62 | untested | CS CT |
| P30 | Prompts show as plain text; replies stream in as Markdown, with raw HTML shown as text, images not loaded, unsafe-scheme links not linked, and links opening in a new tab with `noopener noreferrer`. | ui | web/src/Markdown.tsx:3-18; SessionView.tsx:111-119 | session.spec.ts:44 | CS CT |
| P31 | A tool call shows its name, the first line of its first string input (up to 120 characters), and running, done, failed, or stopped; expanded, it shows the input JSON, the result, and an ExitPlanMode plan as Markdown. | ui | web/src/SessionView.tsx:132-153 | approval.spec.ts:47 | CS CT |
| P32 | A tool call with no result shows stopped once the session parks or its process exits. | ui | web/src/ox.tsx:97-99,126-142 | untested | CS |
| P33 | A failed turn shows its error text (including "Interrupted"); a successful result is not repeated. A process exit shows its error text. | ui | web/src/SessionView.tsx:121-125 | session.spec.ts:173 | CS CT |
| P34 | The conversation follows new content until the user scrolls more than 32 px up, and follows again within 32 px of the bottom. | ui | web/src/SessionView.tsx:17-53 | untested | CS CT |
| P35 | While the session is running or requires action, Send becomes Stop, which interrupts it; before `oxd` reports the prompt running, Send is disabled and Stop is not shown. | ui | web/src/SessionView.tsx:48-51,90-103 | session.spec.ts:173,184 | CS CT |
| P36 | A session's settings row shows its model, effort, and harness, and a permission mode select; its model picker offers only that harness's models. | ui | web/src/SessionView.tsx:158-187 | session.spec.ts:117 | CS CT |
| P37 | Settings can change only while the session is idle or parked, synced, and has no send or change in flight. | ui | web/src/ox.tsx:182-185 | session.spec.ts:117 | CS CT |
| P38 | A settings change sends only the changed fields; meanwhile the changed control shows a spinner, and the others and Send are disabled; on success the confirmed value shows; on failure the session's settings return and the error shows. | ui | web/src/ox.tsx:236-246; SessionView.tsx:158-186 | session.spec.ts:117,137 | CS CT |
| P39 | The model picker filters by model and harness name (case-insensitive), moves with the arrow keys, chooses with Enter or a click, marks the current model, and says "No models match." when nothing matches. | ui | web/src/SettingsFields.tsx:20-60,79-127 | untested | CS CT |
| P40 | Choosing a model keeps the effort if the new model offers it, else takes its first effort, or none. | ui | web/src/SettingsFields.tsx:38-43 | untested | CS CT |
| P41 | The effort slider is disabled for a model with fewer than two efforts, and applies an effort when released. | ui | web/src/SettingsFields.tsx:139-176 | untested | CS CT |
| P42 | A stored model or permission mode the catalog no longer offers still shows, by its ID. | ui | web/src/SettingsFields.tsx:35-36,188-191 | untested | CS CT |
| P43 | Pending approvals show as cards above the composer, including after a reload or reconnect, and disappear when answered or withdrawn from any page. | ui | web/src/SessionView.tsx:71-79; ox.tsx:130-138,312 | approval.spec.ts:20; session.spec.ts:238 | CS CT |
| P44 | A permission card asks "Allow <tool>?", summarizes the input with the full JSON expandable, renders an ExitPlanMode plan as Markdown, and offers Allow, Deny, or a typed reply; Submit waits for a choice. | ui | web/src/ApprovalRequest.tsx:26-101 | approval.spec.ts:20,47 | CS CT |
| P45 | Allow sends the tool's input as received, keeping numbers exact; Deny sends "Denied by user"; a typed reply denies with that text. | ui | web/src/ApprovalRequest.tsx:40-48; connection.ts:46-53 | session.spec.ts:238; approval.spec.ts:20,35 | CS CT |
| P46 | AskUserQuestion shows a form: each question's header and options, radios or checkboxes, and an Other field. Submit needs every question answered and allows with the answers added to the input. Multi-select answers are joined with ", " in option order. Decline denies with "User declined to answer". An input of another shape gets the permission card. | ui | web/src/ApprovalRequest.tsx:111-231; ox.tsx:60-74 | session.spec.ts:254-300; approval.spec.ts:69 | CS CT |
| P47 | An approval's controls are disabled while its answer is in flight; a failed answer shows the error and re-enables the card. | ui | web/src/ApprovalRequest.tsx:40-48 | untested | CS CT |

### Daemon (`cmd/oxd`, `internal/server`, `internal/store`, `internal/config`)

| ID | Behavior | Surface | Code | Test | Found by |
| --- | --- | --- | --- | --- | --- |
| D1 | `oxd` reads its configuration from `$XDG_CONFIG_HOME/ox/config.json`, keeps its database at `$XDG_STATE_HOME/ox/ox.db`, and listens on `$XDG_RUNTIME_DIR/ox/oxd.sock` (or beside the database), each overridable by flag. | operator | internal/paths/paths.go:10-41; cmd/oxd/main.go:27-34 | untested | DS DT |
| D2 | Without a configuration file, `oxd` offers Claude Opus, Sonnet, and Haiku 5.5 at low to max effort, with manual, auto, acceptEdits, and plan modes, defaulting to Haiku 5.5, low, acceptEdits. | ui | internal/config/default.json | config_test.go:11 | DS DT |
| D3 | `oxd` refuses to start on an invalid configuration: unknown fields, trailing content, a harness other than `claude` or `fake`, a harness without models or modes, empty IDs, an effort Claude does not take, or defaults that are not offered. | operator | internal/config/config.go:53-103; cmd/oxd/main.go:73-90 | config_test.go:11-47; cmd/oxd/main_test.go:54 | DS DT |
| D4 | `oxd` replaces a stale socket, and refuses to start over a non-socket file or a socket another `oxd` serves. | operator | cmd/oxd/main.go:109-142 | cmd/oxd/main_test.go:20,41 | DS DT |
| D5 | Workspaces, sessions, settings, and history survive a daemon restart; every session starts parked. | ui | internal/server/server.go:44-63 | methods_test.go:232 | DS DT |
| D6 | On SIGINT or SIGTERM, `oxd` stops every session's Claude in parallel (an active prompt gets an `exited` update) and exits; a second signal exits at once. Requests during shutdown fail with "daemon is shutting down". | operator | cmd/oxd/main.go:54-70; server.go:105-120; methods.go:90-104 | cmd/oxd/main_test.go:82-157 | DS DT |
| D7 | Every connection receives every workspace, session, and update notification from the moment it connects, with no subscription and no replay. | protocol | internal/server/server.go:132-139 | methods_test.go:115 | DS DT |
| D8 | Closing a connection leaves its sessions, prompts, and approvals running. | protocol | internal/server/server.go:85 | untested | DT |
| D9 | A connection whose 1024-message queue overflows or whose write takes over 5 s is disconnected. | protocol | internal/server/conn.go:20-51 | untested | DS DT |
| D10 | Requests are handled concurrently, so independent replies can arrive out of order, and a slow session holds up only its own requests. | protocol | internal/server/server.go:101; session.go:49-76 | live_test.go:163 | DS DT |
| D11 | Every request with an ID gets a reply: -32700 for bad JSON, -32600 for a bad envelope or batch, -32601 for an unknown method, -32602 for bad params, -32001 not found, -32002 busy, -32003 approval not pending, -32000 for other failures with the underlying reason; a request without an ID gets none. | protocol | internal/jsonrpc/jsonrpc.go:47-79; methods.go:38-77 | methods_test.go:34,46 | DS DT |
| D12 | A reply is ordered after every update it reflects and before every update it does not, so a snapshot plus later updates reproduce the session. | protocol | internal/server/session.go:13-18; methods.go:285-312 | untested | DS DT |
| D13 | `harness/list` returns the catalog and defaults; a model without efforts lists an empty `efforts`. | protocol | internal/server/methods.go:50; config.go:80-83 | config_test.go:51 | DS DT |
| D14 | `workspace/create` takes an absolute path to an existing directory, cleans it, and returns its ID; the same path again returns the existing ID. | ui | internal/server/methods.go:106-147 | methods_test.go:88 | DS DT |
| D15 | `workspace/list` returns workspaces and their sessions, oldest first. | ui | internal/server/methods.go:149-174 | methods_test.go:192 | DS DT |
| D16 | `workspace/remove` removes a workspace only when it has no sessions, leaves its directory, and stays removed across restarts. | ui | internal/server/methods.go:176-209 | methods_test.go:200-240 | DS DT |
| D17 | `session/create` records a parked session without starting Claude; harness, model with effort, and mode must be offered; a model with efforts needs one. | ui | internal/server/methods.go:211-283 | methods_test.go:55-65 | DS DT |
| D18 | A client may choose a session's ID (32 hex digits); repeating it in the same workspace returns `existing: true` and changes nothing; reusing it in another workspace is refused. | protocol | internal/server/methods.go:222-250 | methods_test.go:73-113 | DS DT |
| D19 | `session/get` returns settings, state, the active prompt, pending approvals in arrival order, and history, including a reply still streaming. | ui | internal/server/methods.go:285-312; history.go:54-72 | methods_test.go:174; history_test.go:11 | DS DT |
| D20 | History keeps prompts, reply text (one entry per message), tool calls and results, results, and exits, including turns Claude starts itself, but not states or approvals. | ui | internal/server/history.go:18-33 | history_test.go:11-55 | DS DT |
| D21 | A failed history write is logged; the update still goes to every page but is missing after a reload. | ui | internal/server/history.go:42-52 | untested | DS DT |
| D22 | `session/configure` applies only the fields given, checks only the changed ones, and needs at least one. | ui | internal/server/methods.go:383-419 | methods_test.go:46-67 | DS DT |
| D23 | Configuring a parked session stores and announces the settings; the next process starts with them. | ui | internal/server/methods.go:420-428 | untested | DS DT |
| D24 | `session/destroy` stops Claude, withdraws approvals, ends an active prompt with `exited`, deletes the session and its history, announces it last, and leaves Claude's transcript and the workspace on disk. Later requests for it get -32001. | ui | internal/server/methods.go:455-487 | methods_test.go:206; live_test.go:177 | DS DT |
| D25 | A session parks after `-park-after` (default five minutes) of being parkable; the period restarts whenever it stops being parkable; zero or less disables parking. | ui | internal/server/session.go:89-100 | methods_test.go:140; live_test.go:233 | DS DT |
| D26 | A send to a parked session restarts Claude with the session's stored settings, resuming its conversation. | ui | internal/server/methods.go:324-329; claude/claude.go:56-61 | methods_test.go:199 | DS DT |

### Claude harness (`internal/claude`)

| ID | Behavior | Surface | Code | Test | Found by |
| --- | --- | --- | --- | --- | --- |
| C1 | Claude runs in the workspace directory with the session's model, effort, and permission mode, billed to the subscription (`ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` removed). | ui | internal/claude/claude.go:47-73 | claude_test.go:132 | DS DT |
| C2 | Claude gets an appended system prompt: lead with the big idea; crisp, deadpan, military-efficient delivery; dry wit; emotional neutrality. | ui | internal/claude/claude.go:28-30 | untested | DS DT |
| C3 | Claude's startup has one minute; a failed start fails the send with Claude's own reason (its error result, its stdout text, or the last 2 KiB of stderr) and leaves the session parked. | ui | internal/claude/claude.go:103-227 | claude_test.go:17-112 | DS DT |
| C4 | A send that finds Claude no longer has the conversation fails with a notice in the transcript, and the next send starts a new conversation without the earlier context. | ui | internal/server/session.go:185-201 | methods_test.go:327-384 | DS DT |
| C5 | A new conversation becomes resumable at its first non-empty or failed result. | ui | internal/claude/agent.go:158-165 | methods_test.go:380 | DS DT |
| C6 | A send fails as busy while a prompt is pending, the session is running or awaiting action, or a configuration is in flight; prompts are not queued. | ui | internal/claude/prompt.go:16-19 | prompt_test.go:87; configure_test.go:63 | DS DT |
| C7 | A send returns once the prompt is written, publishing the prompt and a `running` state at once. | ui | internal/claude/prompt.go:20-29 | prompt_test.go:98 | DS DT |
| C8 | A prompt is released only by Claude reporting `idle` after taking it; an earlier `idle` or a `result` alone does not release it. | protocol | internal/claude/prompt.go:46-68 | prompt_test.go:104,238 | DS DT |
| C9 | Reply text, top-level tool calls and results, and the result of a prompt's turn carry its `message_id`, as does the update that releases it; turns Claude starts itself carry none. | protocol | internal/claude/prompt.go:88-96; agent.go:166-168 | prompt_test.go:149-264 | DS DT |
| C10 | Subagent messages are not shown; a tool result's text blocks are joined with newlines. | ui | internal/claude/agent.go:128-148; messages.go:84-115 | prompt_test.go:169 | DS DT |
| C11 | A failed result shows its text, else its errors, else its subtype; an interrupted turn shows "Interrupted"; Claude's empty bookkeeping result is not shown. | ui | internal/claude/agent.go:149-165; messages.go:35-49 | prompt_test.go:149; live_test.go:139 | DS DT |
| C12 | `session/interrupt` stops a running turn, including a prompt Claude has not taken yet, ending it with "Interrupted" and `idle`; on an idle or parked session it succeeds and does nothing. | ui | internal/claude/prompt.go:36-44 | prompt_test.go:108; live_test.go:139 | DS DT |
| C13 | Each tool permission request is held as a pending approval and passed through intact; several can be pending, with or without a prompt. | ui | internal/claude/approval.go:12-31 | approval_test.go:12-43,91 | DS DT |
| C14 | An approval waits for an answer indefinitely, and the session does not park meanwhile. | ui | internal/claude/park.go:8-10 | approval_test.go:97 | DS |
| C15 | An answer must allow or deny, with an object `updatedInput` and an array `updatedPermissions` if present; it is forwarded and announced as answered; a second or late answer gets -32003, as does an answer to a parked session. | protocol | internal/claude/approval.go:33-88; methods.go:341-362 | approval_test.go:12 | DS DT |
| C16 | An answer that cannot be written leaves the approval pending. | protocol | internal/claude/approval.go:40-44 | approval_test.go:114 | DS DT |
| C17 | Approvals are withdrawn when Claude cancels them, reports `idle`, or exits, before that update; a result alone does not withdraw them. | ui | internal/claude/approval.go:50-72 | approval_test.go:45-89 | DS DT |
| C18 | Configuring a running session sends each changed setting to Claude in turn (model, effort, mode), waiting for each acknowledgement, and announces the result before replying; an unchanged request does not contact Claude. | ui | internal/claude/configure.go:28-87; methods.go:429-451 | configure_test.go:40-117 | DS DT |
| C19 | If Claude refuses a change or the write fails, the request fails with the reason, and the session keeps and announces what Claude did take, even when storing it fails. | ui | internal/claude/configure.go:71-79; methods.go:436-449 | configure_test.go:119-157; methods_test.go:245 | DS DT |
| C20 | A change Claude does not acknowledge within one minute stops the process; that change, or one cut off by the process exiting, succeeds, and the settings apply at the next start. | ui | internal/claude/configure.go:61-101; methods.go:434-438 | configure_test.go:161; methods_test.go:284 | DS DT |
| C21 | When Claude changes its own permission mode, as on leaving plan mode, the session adopts, stores, and announces it (Claude's `default` is `manual`). | ui | internal/claude/agent.go:115-118,172-184 | configure_test.go:209; methods_test.go:388 | DS DT |
| C22 | A session does not park while Claude reports a background task or a schedule; after a scheduling tool call or a resume, it stays active until a Stop report lists no schedules. | ui | internal/claude/park.go:8-20; agent.go:71-75,128-132 | park_test.go:8-70; live_test.go:209-264 | DS DT |
| C23 | Claude exiting on its own adds "Claude exited: <reason>" to the transcript, then parks; an exit Ox asked for parks silently unless a prompt was active. | ui | internal/claude/agent.go:198-219 | prompt_test.go:189-204 | DS DT |
| C24 | Stopping Claude sends SIGTERM and kills it after ten seconds. | ui | internal/claude/claude.go:255-266 | prompt_test.go:206 | DS DT |
| C25 | Claude's last output is read even without a final newline, and its exit is seen within a second even when a child holds its pipes. | ui | internal/claude/claude.go:232-253 | claude_test.go:49-82 | DS |
| C26 | `oxd` logs each line of Claude's stderr under the session's ID. | operator | internal/claude/claude.go:194-211 | untested | DS |
| C27 | Claude control requests other than tool permissions and the Stop hook are refused, so Claude does not wait. | protocol | internal/claude/approval.go:23-28 | untested | DS |

### Fake harness (`internal/fake`)

| ID | Behavior | Surface | Code | Test | Found by |
| --- | --- | --- | --- | --- | --- |
| F1 | When configured, `fake` echoes "You said: <text>" a word every 50 ms, supports interrupt and configuration, never asks for approval, and becomes resumable after its first result. | fake | internal/fake/fake.go:37-167 | methods_test.go:115-322 | DS DT |

## Defects confirmed from the source

Each needs your call: fix, record in `docs/known-issues.md`, or accept as the requirement.

1. **The park timer can stop Claude just as it starts a turn of its own.** `session.go:60-73` picks at random between a pending agent event and an expired park timer, and parks without checking `Parkable` again. If the timer fires while the goroutine is busy and an event arrives, a scheduled or background turn Claude was starting is killed. The window is narrow, but by `AGENTS.md` it is an ownership defect: the session parks while Claude has work. Found by DS.
2. **A failed write of the resume token silently loses the conversation.** `markResumable` (`session.go:177-183`) logs the store failure and returns, and the agent drops its callback after one call (`agent.go:161-164`). The next start begins a new conversation with no notice, unlike the C4 path. Found by DS.
3. **A deleted open session or an unknown `#id` leaves a blank main area.** There is no message, and the fragment is left as it was (`App.tsx:41-46,90-97`). Found by CS.
4. **Stop and the question form's Submit stay enabled while disconnected.** Clicking them fails with "Not connected to ox". Send, settings, and the permission card's Submit are disabled instead (`SessionView.tsx:96-102`, `ApprovalRequest.tsx:103` vs `:232`). Found by CS.

## Behaviors to confirm as intended

- **P2/R6:** after `oxd` restarts, each open page must be reloaded by hand. `/events` answers 502 while `oxd` is down, and the page then asks for a reload; `architecture.md` describes this. Only a relay restart recovers by itself. Found by CS.
- **P25:** a send whose reply was lost shows an error even if `oxd` received it, so a second Send can run the prompt twice. Guarantee 3 accepts this; the transcript shows whether it ran. Found by CS.
- **P22:** a whitespace-only prompt is silently dropped. Found by CS CT.
- **P39:** the model picker stays open after a choice (`SettingsFields.tsx:38-43`), which keeps the effort slider in reach. Found by CS.
- **D21:** a failed history write loses that update from history, with only a log line. Found by DS DT.

## Rejected candidates

| Finding | From | Reason |
| --- | --- | --- |
| A model with omitted `efforts` breaks the picker | CT | `config.go:81-83` turns a missing list into `[]`. |
| Duplicate AskUserQuestion question text overwrites an answer | CT | Claude keys answers by question text; Ox follows its format. |
| A final request line without `\n` is dropped | DS | The protocol defines requests as lines. |
| Any `hook_callback` is treated as the Stop hook | DT | Ox declares only the Stop hook, so Claude sends no other callback. |
| The in-progress reply's text is lost on a crash | DT | A clean shutdown flushes it through `parked`. Crash durability is not a goal (`AGENTS.md`). |
| A queued prompt can show a stale state | DT | Speculative; no sequence was shown that reaches it. |
| Claude-reported modes outside the catalog are adopted | DS | Speculative; Claude returns to the mode the session had. |
| No timeout on page requests | CS | `oxd` answers every request, and a lost stream fails them (P2). |
| A send to a parked session holds that session's other requests for up to a minute | DS | Intended: a request waits behind its session's operation (guarantee 4). |
| A parked no-op configure still announces | DS | Harmless. |
| History is unbounded | DS | An unmeasured scaling cost. |
| `/clear` and other local commands show nothing | DS | Unverified against live Claude. |
| Shutdown leaves "Claude exited: signal: terminated" in the transcript | DS | Accurate wording for what happened. |

## Doc conflicts

- `protocol.md` does not mention that `-park-after 0` disables parking (`session.go:92`), or the relay's 1 MiB request limit (`cmd/ox/main.go:127-131`).
- `protocol.md:162` marks `effort` "Required: No", but a model that has efforts requires one (`methods.go:256`). The Meaning cell implies this; the Required column says otherwise.
- `claude-code.md:275` says to echo approval input byte-for-byte without re-encoding. The page parses it and re-encodes it, keeping numbers exact but not whitespace. Claude parses the JSON, so nothing observable differs.

## Against the current `docs/guarantees.md`

Every clause of guarantees 1–9 matches one or more rows above. Two clauses have no row, because they are negative claims that extraction does not surface: "`oxd` writes only its database and socket", and "Claude writes in the workspace as its permission mode and approvals allow". In the other direction, the extraction found requirements that `guarantees.md` lacks. These include R4 (local-only access), R5, D5 (survives restart), D26 and C4 (conversation continuity), C12 (interrupt), D6 (shutdown), D3 (config validation), C1 (subscription billing), C2 (system prompt), and most of the page's interaction detail (P7–P47).
