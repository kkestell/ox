### Behaviors

| ID | Behavior | Surface | Code | Test |
| --- | --- | --- | --- | --- |
| B1 | The web server serves its embedded web files at `/` and defaults to `localhost:8080`, with `-addr` and `-socket` overrides. | ui | cmd/ox/main.go:24 | untested |
| B2 | Each `/events` stream opens a separate daemon socket and first receives a randomly generated connection identifier. | ui | cmd/ox/main.go:81 | untested |
| B3 | Each complete newline-terminated daemon message reaches the page as a server-sent event, and closing the event request closes its daemon connection. | ui | cmd/ox/main.go:97 | untested |
| B4 | A `/rpc` POST forwards its body and a newline to the daemon connection named by its `connection` query parameter and returns HTTP 204 on success. | ui | cmd/ox/main.go:119 | cmd/ox/main_test.go:42 |
| B5 | An RPC using an unknown connection receives HTTP 404, and a socket write failure receives HTTP 502. | ui | cmd/ox/main.go:123 | untested |
| B6 | An RPC body larger than 1 MiB receives HTTP 413 without being forwarded. | ui | cmd/ox/main.go:127 | cmd/ox/main_test.go:66 |
| B7 | If the daemon socket cannot be opened, `/events` responds with HTTP 502 and the connection error. | ui | cmd/ox/main.go:82 | untested |
| B8 | The relay rejects event and RPC requests whose Host is neither a loopback name nor its configured host, and rejects cross-origin RPC posts. | ui | cmd/ox/main.go:55 | cmd/ox/main_test.go:15 |
| B9 | A page opens an event stream and sends numbered JSON-RPC calls through its associated relay connection. | ui | cmd/ox/web/src/connection.ts:22 | cmd/ox/web/e2e/session.spec.ts:25 |
| B10 | A failed relay POST or JSON-RPC response displays the returned error for the operation. | ui | cmd/ox/web/src/connection.ts:55 | cmd/ox/web/e2e/session.spec.ts:82 |
| B11 | On event-stream failure, pending requests fail and the page reports either that it is reconnecting or that a page reload is needed. | ui | cmd/ox/web/src/connection.ts:36 | cmd/ox/web/e2e/session.spec.ts:150 |
| B12 | On connection or reconnection, the page reloads the harness catalog, workspace list, and each session's settings, history, state and pending approvals. | ui | cmd/ox/web/src/ox.tsx:329 | cmd/ox/web/e2e/session.spec.ts:341 |
| B13 | During disconnection, existing session send and settings controls remain unavailable until that session's snapshot has reloaded. | ui | cmd/ox/web/src/ox.tsx:182 | cmd/ox/web/e2e/session.spec.ts:341 |
| B14 | The page retains unsent session text across navigation and connection loss without automatically repeating the request. | ui | cmd/ox/web/src/ox.tsx:163 | cmd/ox/web/e2e/session.spec.ts:150 |
| B15 | Workspace and session creation, removal, configuration, state and conversation notifications update the page as they arrive from any connected client. | ui | cmd/ox/web/src/ox.tsx:356 | cmd/ox/web/e2e/session.spec.ts:341 |
| B16 | A user can register a workspace by entering a path in the Add workspace dialog, and a failed request is shown in the dialog. | ui | cmd/ox/web/src/Sidebar.tsx:16 | cmd/ox/web/e2e/workspace.spec.ts:6 |
| B17 | The sidebar shows each workspace's final path component, with its full path available as a tooltip. | ui | cmd/ox/web/src/Sidebar.tsx:110 | cmd/ox/web/e2e/workspace.spec.ts:15 |
| B18 | The sidebar orders a workspace's sessions by creation time and shows each session's state and first prompt's trimmed first line, or the first eight ID characters before a prompt exists. | ui | cmd/ox/web/src/Sidebar.tsx:27 | cmd/ox/web/e2e/session.spec.ts:101 |
| B19 | A user can select a session from the sidebar, and its ID in the URL fragment restores that selection on reload. | ui | cmd/ox/web/src/App.tsx:9 | cmd/ox/web/e2e/session.spec.ts:150 |
| B20 | A malformed encoded session fragment is ignored so the rest of the page remains usable. | ui | cmd/ox/web/src/App.tsx:10 | cmd/ox/web/e2e/workspace.spec.ts:6 |
| B21 | The sidebar offers session deletion through a confirmation dialog that identifies the session and says its history is deleted. | ui | cmd/ox/web/src/Sidebar.tsx:80 | cmd/ox/web/e2e/workspace.spec.ts:47 |
| B22 | A workspace with no listed sessions offers removal through a confirmation dialog explaining that its directory stays on disk. | ui | cmd/ox/web/src/Sidebar.tsx:95 | cmd/ox/web/e2e/workspace.spec.ts:36 |
| B23 | Failed workspace removal leaves the dialog open and displays the error, allowing another attempt. | ui | cmd/ox/web/src/Sidebar.tsx:52 | cmd/ox/web/e2e/workspace.spec.ts:59 |
| B24 | Choosing New session opens one reusable draft per workspace, and removing the workspace removes its draft from view. | ui | cmd/ox/web/src/App.tsx:32 | cmd/ox/web/e2e/workspace.spec.ts:52 |
| B25 | A new session draft starts with the configured defaults and permits selecting a harness, model, effort and permission mode before sending. | ui | cmd/ox/web/src/NewSessionView.tsx:11 | cmd/ox/web/e2e/workspace.spec.ts:20 |
| B26 | When changing harness, a draft keeps its permission mode if the new harness offers it, otherwise using that harness's default or first mode. | ui | cmd/ox/web/src/NewSessionView.tsx:22 | untested |
| B27 | The model picker searches model and harness names case-insensitively, supports arrow-key selection and Enter, and displays a no-match message. | ui | cmd/ox/web/src/SettingsFields.tsx:25 | untested |
| B28 | Changing model retains the selected effort if offered by the new model, otherwise choosing its first effort or an empty effort. | ui | cmd/ox/web/src/SettingsFields.tsx:38 | untested |
| B29 | The effort slider is disabled when the model offers fewer than two effort choices. | ui | cmd/ox/web/src/SettingsFields.tsx:139 | untested |
| B30 | A saved permission mode absent from the current catalog remains displayed by its ID. | ui | cmd/ox/web/src/SettingsFields.tsx:188 | untested |
| B31 | A prompt is submitted with Enter or the Send button, while Shift+Enter inserts a newline and Enter during input-method composition does not submit. | ui | cmd/ox/web/src/SessionView.tsx:10 | cmd/ox/web/e2e/session.spec.ts:25 |
| B32 | Blank or whitespace-only prompts are not sent. | ui | cmd/ox/web/src/ox.tsx:218 | untested |
| B33 | Sending a new draft creates a session with a page-generated ID and selected settings, then sends the text captured at submission. | ui | cmd/ox/web/src/ox.tsx:218 | cmd/ox/web/e2e/session.spec.ts:25 |
| B34 | If the user navigates away while a draft is being created, its eventual creation does not change the current selection. | ui | cmd/ox/web/src/App.tsx:54 | cmd/ox/web/e2e/session.spec.ts:101 |
| B35 | A successful send clears the composer only if its text still matches the submitted text, preserving later edits. | ui | cmd/ox/web/src/ox.tsx:190 | cmd/ox/web/e2e/session.spec.ts:25 |
| B36 | A failed send shows an error and retains the prompt for a user-initiated retry. | ui | cmd/ox/web/src/ox.tsx:190 | cmd/ox/web/e2e/session.spec.ts:82 |
| B37 | A prompt whose serialized request exceeds 1 MiB fails visibly and stays in the composer. | ui | cmd/ox/web/src/connection.ts:75 | cmd/ox/web/e2e/session.spec.ts:162 |
| B38 | If a lost creation reply is recovered as an existing session, the page retains the draft text, displays a recovery notice, and waits for the user to review settings and send. | ui | cmd/ox/web/src/App.tsx:59 | cmd/ox/web/e2e/session.spec.ts:304 |
| B39 | A draft whose creation fails while another view is selected shows an error with a Reopen draft button. | ui | cmd/ox/web/src/App.tsx:79 | cmd/ox/web/e2e/session.spec.ts:304 |
| B40 | A session shows its workspace path, harness and session ID above its conversation. | ui | cmd/ox/web/src/SessionView.tsx:62 | untested |
| B41 | The conversation displays prompts as plain text and assistant text as streaming Markdown. | ui | cmd/ox/web/src/SessionView.tsx:111 | cmd/ox/web/e2e/session.spec.ts:44 |
| B42 | Markdown escapes raw HTML, does not load images, linkifies URLs, and opens links in a new tab with `noopener noreferrer`. | ui | cmd/ox/web/src/Markdown.tsx:3 | cmd/ox/web/e2e/session.spec.ts:44 |
| B43 | The conversation displays failed turn results and process exit errors but does not repeat successful result text. | ui | cmd/ox/web/src/SessionView.tsx:121 | cmd/ox/web/e2e/session.spec.ts:173 |
| B44 | A tool call shows its name, the first line of its first string input truncated to 120 characters, and a running, done, failed or stopped status. | ui | cmd/ox/web/src/SessionView.tsx:132 | untested |
| B45 | Expanding a tool call shows its formatted input, its result when present, and an ExitPlanMode plan rendered as Markdown. | ui | cmd/ox/web/src/SessionView.tsx:139 | cmd/ox/web/e2e/approval.spec.ts:47 |
| B46 | The conversation follows new content at the bottom unless the user scrolls at least 32 pixels away from it. | ui | cmd/ox/web/src/SessionView.tsx:20 | untested |
| B47 | While a session is running or requires action, Stop replaces Send and requests an interrupt. | ui | cmd/ox/web/src/SessionView.tsx:90 | cmd/ox/web/e2e/session.spec.ts:173 |
| B48 | While a send is pending before the running state arrives, Send is disabled and Stop is not yet shown. | ui | cmd/ox/web/src/ox.tsx:185 | cmd/ox/web/e2e/session.spec.ts:184 |
| B49 | A pending send or configuration in one session does not prevent work in another session. | ui | cmd/ox/web/src/ox.tsx:271 | cmd/ox/web/e2e/session.spec.ts:199 |
| B50 | Session settings can be changed only while the session is idle or parked and its page control has no send or configuration pending. | ui | cmd/ox/web/src/ox.tsx:182 | cmd/ox/web/e2e/session.spec.ts:117 |
| B51 | A settings change sends only changed fields, shows requested settings and a spinner for changed controls, and disables Send until the request settles. | ui | cmd/ox/web/src/ox.tsx:236 | cmd/ox/web/e2e/session.spec.ts:117 |
| B52 | A failed settings change displays its error and returns the settings display to the session's reported settings. | ui | cmd/ox/web/src/SessionView.tsx:160 | cmd/ox/web/e2e/session.spec.ts:137 |
| B53 | A session's model picker offers only the models of its existing harness. | ui | cmd/ox/web/src/SessionView.tsx:163 | untested |
| B54 | Pending approvals appear together above the composer and disappear when answered or cancelled by any client. | ui | cmd/ox/web/src/SessionView.tsx:71 | cmd/ox/web/e2e/approval.spec.ts:20 |
| B55 | An ordinary approval shows the tool name, an input summary and expandable full JSON input, and renders an ExitPlanMode plan as Markdown. | ui | cmd/ox/web/src/ApprovalRequest.tsx:62 | cmd/ox/web/e2e/approval.spec.ts:47 |
| B56 | An ordinary approval permits Allow, Deny, or a nonblank custom denial message, and requires a choice before submission. | ui | cmd/ox/web/src/ApprovalRequest.tsx:26 | cmd/ox/web/e2e/approval.spec.ts:20 |
| B57 | Allow forwards the original tool input as updatedInput, retaining numeric tokens that would otherwise lose precision. | ui | cmd/ox/web/src/connection.ts:46 | cmd/ox/web/e2e/session.spec.ts:238 |
| B58 | Deny sends `Denied by user`, while a custom denial sends the trimmed typed message. | ui | cmd/ox/web/src/ApprovalRequest.tsx:40 | cmd/ox/web/e2e/approval.spec.ts:35 |
| B59 | An approval's controls are disabled while its response is being sent, and a failure is shown as a session error. | ui | cmd/ox/web/src/ApprovalRequest.tsx:40 | untested |
| B60 | A well-formed AskUserQuestion approval shows each question, optional header and option descriptions with single-select or multi-select controls and an Other field. | ui | cmd/ox/web/src/ApprovalRequest.tsx:154 | cmd/ox/web/e2e/session.spec.ts:254 |
| B61 | The question form enables Submit only when every question has a nonempty answer and sends answers keyed by question text in the original input. | ui | cmd/ox/web/src/ApprovalRequest.tsx:123 | cmd/ox/web/e2e/session.spec.ts:254 |
| B62 | Multi-select answers contain selected labels in option order followed by trimmed Other text, joined with comma-space. | ui | cmd/ox/web/src/ApprovalRequest.tsx:113 | cmd/ox/web/e2e/session.spec.ts:269 |
| B63 | Declining a question form sends a denial with the message `User declined to answer`. | ui | cmd/ox/web/src/ApprovalRequest.tsx:224 | cmd/ox/web/e2e/session.spec.ts:290 |
| B64 | An AskUserQuestion input that does not have the recognized question and option shape falls back to the ordinary permission card. | ui | cmd/ox/web/src/ox.tsx:60 | cmd/ox/web/e2e/session.spec.ts:300 |
| B65 | Starting another request in a control clears its displayed error, and any request failure displays its own error even if a later request has started. | ui | cmd/ox/web/src/ox.tsx:146 | cmd/ox/web/e2e/session.spec.ts:82 |
| B66 | The page displays pending or completed session changes from a second page when it receives the corresponding daemon notifications. | ui | cmd/ox/web/src/ox.tsx:356 | cmd/ox/web/e2e/session.spec.ts:341 |

### Suspected defects

- A catalog model with omitted `efforts` can make the model picker call `.find` or `.some` on undefined (`cmd/ox/web/src/SettingsFields.tsx:35`, `cmd/ox/web/src/SettingsFields.tsx:40`), although the protocol permits omitted efforts (`docs/protocol.md:73`), potentially breaking the picker.
- Two AskUserQuestion entries with identical question text overwrite one another in the submitted answer object (`cmd/ox/web/src/ApprovalRequest.tsx:138`), so a visibly answered question can lose its answer.

### Doc conflicts

- `docs/claude-code.md:275` says to echo approval input byte-for-byte, while `cmd/ox/web/src/ApprovalRequest.tsx:44` and `cmd/ox/web/src/connection.ts:71` serialize the parsed input again, preserving significant numeric tokens but not necessarily the original whitespace or spelling of other JSON tokens.

### Coverage

- `.agents/requirements/BRIEF.md`
- `cmd/ox/main.go`
- `cmd/ox/main_test.go`
- `cmd/ox/web/AGENTS.md`
- `cmd/ox/web/src/SessionView.tsx`
- `cmd/ox/web/src/ApprovalRequest.tsx`
- `cmd/ox/web/src/Sidebar.tsx`
- `cmd/ox/web/src/protocol.ts`
- `cmd/ox/web/src/connection.ts`
- `cmd/ox/web/src/HarnessIcon.tsx`
- `cmd/ox/web/src/Markdown.tsx`
- `cmd/ox/web/src/ox.tsx`
- `cmd/ox/web/src/SettingsFields.tsx`
- `cmd/ox/web/src/NewSessionView.tsx`
- `cmd/ox/web/src/App.tsx`
- `cmd/ox/web/src/index.tsx`
- `cmd/ox/web/e2e/session.spec.ts`
- `cmd/ox/web/e2e/approval.spec.ts`
- `cmd/ox/web/e2e/workspace.spec.ts`
- `cmd/ox/web/e2e/services.ts`
- `cmd/ox/web/e2e/ui.ts`
- `docs/protocol.md`
- `docs/architecture.md`
- `docs/claude-code.md`
