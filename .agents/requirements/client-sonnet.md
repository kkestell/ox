# Requirements extraction: web UI server and web client

Paths are relative to `cmd/ox/` unless they start with `docs/`. `S` = `web/e2e/session.spec.ts`, `W` = `web/e2e/workspace.spec.ts`, `A` = `web/e2e/approval.spec.ts` (live Haiku, `OX_LIVE=1` only), `M` = `main_test.go`.

### Behaviors

| ID | Behavior | Surface | Code | Test |
| --- | --- | --- | --- | --- |
| B1 | `ox` serves the embedded web client for GET (and HEAD) requests on `-addr`, default `localhost:8080`. | ui | main.go:21,25,57 | untested (S/W load pages through it) |
| B2 | `ox` fails at startup if `-addr` is not host:port, or if the oxd socket path cannot be resolved; `-socket` overrides the default socket path. | ui | main.go:26-33,39-42 | untested |
| B3 | `ox` starts even when oxd is not running; the failure appears when a page opens `/events`. | ui | main.go:43-44,82-86 | untested |
| B4 | `/events` and `/rpc` answer 403 "unknown host" unless the request's Host names `localhost`, `127.0.0.1`, `::1` or the host part of `-addr`. | protocol | main.go:66-79 | M:15 (foreign host), M:42 |
| B5 | The static page itself is served for any Host (only `/events` and `/rpc` check Host). | protocol | main.go:57-59 | untested |
| B6 | `/rpc` refuses (403) a POST whose origin is cross-origin; a POST through Vite's proxy (relay's own Host, page's Origin, same-origin) is accepted. | protocol | main.go:59 | M:15 (other origin), M:42 |
| B7 | Each `GET /events` opens its own connection to oxd's Unix socket and streams every line oxd sends as a Server-Sent Event (`data: <line>`), flushing each one. | protocol | main.go:81-114 | S (every spec) |
| B8 | The first event on a stream is `event: connection` whose data is a random connection ID the page uses for its requests. | protocol | main.go:88,104 | S (every spec) |
| B9 | When oxd cannot be reached, `/events` answers 502 `connect to oxd: <error>`. | protocol | main.go:82-86 | untested |
| B10 | Closing the page's stream closes its oxd connection and forgets its connection ID; when oxd closes the connection the stream ends. | protocol | main.go:87-100,107-110 | S:150 (relay restart) |
| B11 | `POST /rpc?connection=<id>` writes the request body plus a newline to that connection's oxd socket and answers 204 with no body; the JSON-RPC reply arrives on the event stream. | protocol | main.go:119-140 | M:42 |
| B12 | `/rpc` with an unknown or closed connection ID answers 404 "unknown connection". | protocol | main.go:121-126 | untested |
| B13 | `/rpc` refuses a request body over 1 MiB (1<<20 bytes) with 413 "request is larger than 1 MiB" and forwards nothing to oxd. | protocol | main.go:127-131 | M:66, S:162 |
| B14 | `/rpc` answers 400 with the read error for an unreadable body and 502 with the error when the write to oxd fails. | protocol | main.go:132-139 | untested |
| B15 | The page shows an error when a request fails at the relay, displaying the relay's response text (trimmed) as the message. | ui | web/src/connection.ts:76 | S:82, S:162 |
| B16 | A page opens one event stream at load; on its `connection` event it reloads the harness catalog and defaults, the workspace list, and every listed session. | ui | web/src/connection.ts:32-35; web/src/ox.tsx:331-354 | S:150 |
| B17 | Requests made while there is no live stream connection fail at once with "Not connected to ox". | ui | web/src/connection.ts:64 | untested |
| B18 | When the stream errors, every in-flight request fails with "Lost the connection to ox. Reconnecting." (or, if the browser closed the stream for good, "Lost the connection to ox. Reload the page to reconnect."), and the page shows that message in an alert at the top of the main area. | ui | web/src/connection.ts:36-45; web/src/ox.tsx:395-400; web/src/App.tsx:78 | S:150 (alert clears after reconnect) |
| B19 | While disconnected the Send button (and the draft's model/mode controls) are disabled, and each session is marked unsynced until its own snapshot arrives. | ui | web/src/ox.tsx:182,185,396-399; web/src/NewSessionView.tsx:60,69,76 | S:341, W:6 |
| B20 | After a reconnect the connection error clears, and the page reloads catalog, workspaces, and sessions: sessions deleted while disconnected vanish (with their composer text), loaded sessions update in place (view, scroll and typed text stay), new sessions appear. | ui | web/src/ox.tsx:331-353,292-327 | S:150, S:341 |
| B21 | After a reconnect each session becomes usable as soon as its own `session/get` returns, independent of other sessions. | ui | web/src/ox.tsx:182,315-326 | S:341 |
| B22 | A reload failure (other than "not found" on one session) shows its message as an alert. | ui | web/src/ox.tsx:297-298,392,387 | untested |
| B23 | JSON numbers in notifications and snapshots keep their exact source text (e.g. 9007199254740993, 1.00000000000000001) so an approved tool input is sent back unchanged. | ui | web/src/connection.ts:47-53,73; web/src/ox.tsx:77 | S:238 |
| B24 | The URL hash holds the selected session's ID; selecting a session rewrites the hash (no history entry), selecting a draft clears it, and loading the page selects the session in the hash. | ui | web/src/App.tsx:9-18,30,38-39 | S:25 (reads hash), W:6 |
| B25 | A malformed hash (e.g. `#%`) is ignored and the app starts with nothing selected. | ui | web/src/App.tsx:10-18 | W:6 |
| B26 | The main area is empty when the selection is a session that does not exist (unknown hash, or the session was deleted/never loaded) — no message is shown. | ui | web/src/App.tsx:41-46,90-97 | untested |
| B27 | The sidebar lists workspaces, each named by its path's last segment (full path, or "/" for root, as the name; full path as tooltip), in the order oxd lists them, with newly created ones appended. | ui | web/src/Sidebar.tsx:110-114; web/src/ox.tsx:340,364-367 | W:6, W:36 |
| B28 | Under each workspace its sessions appear oldest first (by created time). | ui | web/src/Sidebar.tsx:27-30,133 | S:101 |
| B29 | A session's sidebar label is the first line (trimmed) of its first prompt, or the first 8 characters of its ID if it has no prompt. | ui | web/src/Sidebar.tsx:168-171 | S (sessionButton by title) |
| B30 | A session's sidebar row shows a status dot named after its state (`parked`, `running`, `requires_action`, `idle`) as title and accessible label. | ui | web/src/Sidebar.tsx:142-147; web/src/protocol.ts:12 | untested |
| B31 | The selected session is highlighted and marked `aria-current="page"`. | ui | web/src/Sidebar.tsx:32,135,139 | untested |
| B32 | The "Add workspace" button opens a modal dialog with an absolute-path field; Add sends `workspace/create`, and on success closes and clears the dialog, and the new workspace appears. | ui | web/src/Sidebar.tsx:16-25,60-79 | W:6, e2e `open` helper |
| B33 | If adding a workspace fails, the error shows in the dialog, the dialog stays open and keeps the typed path; reopening it clears the error. | ui | web/src/Sidebar.tsx:65-68,78 | untested |
| B34 | Cancel closes the Add dialog without clearing its field. | ui | web/src/Sidebar.tsx:73 | untested |
| B35 | Each workspace has a "New session" (+) button that selects a draft for that workspace. | ui | web/src/Sidebar.tsx:124-130; web/src/App.tsx:32-40 | S, W |
| B36 | A workspace shows a "Remove workspace" (trash) button only while it has no sessions; it opens a confirmation dialog stating the path is removed from Ox and the directory stays on disk. | ui | web/src/Sidebar.tsx:115-123,46-56,95-109 | W:36 |
| B37 | Confirming Remove sends `workspace/remove`; failure shows the error inside the dialog and leaves it open and the workspace in place; success closes the dialog and removes the workspace. | ui | web/src/Sidebar.tsx:52-56,108 | W:36 |
| B38 | Removing a workspace deletes its draft (including unsent text); if that draft was open the main area becomes empty. | ui | web/src/App.tsx:47-51; web/src/Sidebar.tsx | W:36 |
| B39 | Each session row has a "Delete session" (trash) button opening a confirmation dialog naming the session's title and stating its history is deleted; Delete sends `session/destroy`, closing on success, showing failure in the dialog. | ui | web/src/Sidebar.tsx:34-44,80-94,150-156 | W:36 |
| B40 | A session deleted from any page (or elsewhere) disappears from the sidebar and drops its composer text; if it was open, the main area becomes empty. | ui | web/src/ox.tsx:379-383; web/src/App.tsx:41-46 | W:36 (own page) |
| B41 | A workspace created, a workspace removed, or a session created in another page or client appears/disappears live without reload. | ui | web/src/ox.tsx:364-378 | S:341 (second page, settings only) |
| B42 | A session created elsewhere is loaded with its history; updates for a not-yet-loaded session are discarded because the snapshot includes them. | ui | web/src/ox.tsx:292-327,358-363 | untested |
| B43 | A session destroyed before its snapshot returns is not added. | ui | web/src/ox.tsx:295-299 | untested |
| B44 | The page keeps one draft per workspace, with its own composer text and settings; switching to another session or workspace and back restores the draft's text and chosen model/effort/mode until the page is reloaded. | ui | web/src/App.tsx:30-40; web/src/NewSessionView.tsx:9-14 | S:304 |
| B45 | A draft view shows "<workspace path> · new session", an empty log, a prompt textarea (autofocus), a Send button, and the model picker and permission-mode select. | ui | web/src/NewSessionView.tsx:38-81 | S:25, W:6 |
| B46 | The draft's model picker and mode select start at the daemon's defaults and are hidden until the catalog has loaded; Send is disabled until the page is connected and loaded. | ui | web/src/NewSessionView.tsx:12,60,63; web/src/ox.tsx:182 | W:6 |
| B47 | Changing the draft's model to one of a different harness keeps the current permission mode if the new harness offers it, otherwise uses that harness's default mode (the daemon's default mode if the default harness matches, else the harness's first mode). | ui | web/src/NewSessionView.tsx:22-30; web/src/SettingsFields.tsx:218-228 | untested |
| B48 | Submitting a draft with empty or whitespace-only text sends nothing (the empty textarea is blocked by the browser's `required`; whitespace-only is silently ignored). | ui | web/src/NewSessionView.tsx:51; web/src/ox.tsx:220 | untested |
| B49 | Submitting a draft creates the session (with the draft's workspace, harness, model, effort, permission mode, and a page-chosen 32-hex session ID) and then sends the composer text as its first prompt exactly once; the draft's controls are disabled meanwhile. | ui | web/src/ox.tsx:218-231; web/src/NewSessionView.tsx:31-37,60-76 | S:25 |
| B50 | The first send starts as soon as creation succeeds, before the new session's snapshot arrives; text typed after submit stays in the composer and is not part of the first prompt. | ui | web/src/ox.tsx:186-197,223-227 | S:25 |
| B51 | When creation succeeds and the draft is still open, the view switches to the new session; if the user moved elsewhere in the meantime the selection is left alone and the session just appears in the sidebar. | ui | web/src/App.tsx:54-57 | S:101 |
| B52 | If session creation fails, the error shows in the draft (alert), the text and settings stay, and a later submit retries. If the user is in another view, an alert with a "Reopen draft" button shows the error. | ui | web/src/NewSessionView.tsx:42; web/src/App.tsx:79-88 | S:304 (reopen) |
| B53 | If the first prompt's send fails after the session was created, the page shows the session with its error alert, the text stays in the composer, and the user can retry with Send in the session. | ui | web/src/ox.tsx:191-197 | S:82 |
| B54 | If the creation reply is lost but the session exists (create returned `existing`, or the session shows up through a notification/reconnect snapshot while the draft has an error), the page reattaches the draft to the session without sending, keeps the composer text, and shows the status "Session recovered. Review its settings and send your prompt." | ui | web/src/ox.tsx:200-203,224-226; web/src/App.tsx:59-72 | S:304 |
| B55 | A session view shows "<workspace path> · <harness> · <session id>", the conversation log, any request error (alert) and notice (status), pending approvals, the prompt composer, and the settings row. | ui | web/src/SessionView.tsx:62-106 | S (many) |
| B56 | Prompts render as plain text; Claude's replies render as Markdown (links autolinked, bold, lists, code); raw HTML in replies shows as literal text; images are not rendered (an image shows as a link); unsafe-scheme links (e.g. `javascript:`) are not links; links open in a new tab with `noopener noreferrer`. | ui | web/src/Markdown.tsx:5-18; web/src/SessionView.tsx:116-119 | S:44 |
| B57 | Streaming reply text appends to the current reply; a tool call or a new turn starts a new reply block. | ui | web/src/ox.tsx:106-111 | S (reply assertions) |
| B58 | A tool call shows as a collapsed row with a status marker (running, done, failed, stopped), the tool name, and its first string input value (first line, truncated to 120 characters); expanding shows the input JSON (pretty, 2-space) and the result text. | ui | web/src/SessionView.tsx:132-153; web/src/ox.tsx:41-44,77 | A:47 (partial) |
| B59 | A tool without a result shows "stopped" once the session is parked or its process exits; otherwise "running". | ui | web/src/ox.tsx:97-99,126-129,139-142,314 | untested |
| B60 | An ExitPlanMode tool call, expanded, shows its `plan` input rendered as Markdown above the JSON. | ui | web/src/SessionView.tsx:145-147; web/src/ox.tsx:47-48 | A:47 |
| B61 | A turn that ended in an error shows its error text in the log; a successful result is not shown (it repeats the reply). | ui | web/src/SessionView.tsx:121-124 | S:173 ("Interrupted") |
| B62 | When Claude's process exits, its error text appears in the log. | ui | web/src/SessionView.tsx:125; web/src/ox.tsx:139-142 | untested |
| B63 | The log scrolls to the bottom on load and follows new content (including when the composer grows) until the user scrolls up more than 32px from the bottom; scrolling back to within 32px re-pins it. | ui | web/src/SessionView.tsx:17-42,53 | untested |
| B64 | Pressing Enter in a prompt textarea submits; Shift+Enter inserts a newline; Enter during IME composition or a held (repeating) Enter does not submit. | ui | web/src/SessionView.tsx:10-15 | S (press("Enter")) |
| B65 | Send is enabled only when the page is connected, the session is synced, the session is idle or parked, and no send, creation or settings change of this page is in flight. | ui | web/src/ox.tsx:182-185; web/src/SessionView.tsx:93 | S:117, S:184, S:341 |
| B66 | After a send succeeds, the composer clears unless the user typed something different meanwhile; after a failure the text stays and an alert shows the error. | ui | web/src/ox.tsx:191-197 | S:25, S:82 |
| B67 | A send (or a send of only whitespace) from a session that is not available is silently ignored. | ui | web/src/ox.tsx:233-235 | S:117 (Enter while pending) |
| B68 | Composer text is kept per session across switching views and reconnects, and is lost on page reload. | ui | web/src/ox.tsx:176,267-285 | S:150, S:199 |
| B69 | While a prompt is running (session state `running` or `requires_action`) the Send button becomes Stop; Stop sends `session/interrupt`. Before oxd reports the prompt running, Send is disabled and no Stop shows. | ui | web/src/SessionView.tsx:48-51,90-103; web/src/ox.tsx:184,247 | S:173, S:184 |
| B70 | An interrupted turn leaves an "Interrupted" error line in the log and Send returns. | ui | web/src/SessionView.tsx:121-124 | S:173 |
| B71 | One session's held or failing request leaves other sessions usable. | ui | web/src/ox.tsx:267-285 | S:199 |
| B72 | A request error stays until the next request of that session starts (or a recovery); a notice clears when the next request starts. | ui | web/src/ox.tsx:146-161,186-189 | S:82 |
| B73 | The settings row shows a model picker (current model name, effort name, harness icon) and a permission-mode select for the session's harness only. | ui | web/src/SessionView.tsx:158-187 | S:117 |
| B74 | Changing a session's model, effort, or mode sends `session/configure` with only the changed fields; while pending the changed control shows a spinner and `aria-busy`, the other controls and Send are disabled, and on success the confirmed value shows; on failure the previous value returns and an alert shows the error. | ui | web/src/ox.tsx:236-246; web/src/SessionView.tsx:158-186 | S:117 |
| B75 | Settings changes made elsewhere (another page, Claude changing mode) update the session's shown settings live. | ui | web/src/ox.tsx:374-378 | S:341 |
| B76 | Settings controls and the picker are disabled while the session is running, requires action, unsynced, or has a send/configure in flight. | ui | web/src/ox.tsx:185; web/src/SessionView.tsx:171,178 | S:117 |
| B77 | The model picker is a popover button; opening it clears the search, focuses the search field, and lists models (all harnesses in a draft, one harness in a session), filtered by case-insensitive substring of "model name + harness name"; no match shows "No models match." | ui | web/src/SettingsFields.tsx:20-31,79-127 | untested |
| B78 | In the picker, ArrowUp/ArrowDown move the active option (wrapping, scrolling into view), Enter chooses it, hovering sets the active option, clicking chooses; the current model has a check mark; choosing the current model does nothing. The popover stays open after choosing. | ui | web/src/SettingsFields.tsx:38-60,111-125 | untested |
| B79 | Choosing a model keeps the current effort if the new model offers it, otherwise selects the model's first effort. | ui | web/src/SettingsFields.tsx:38-43 | untested |
| B80 | The picker has an Effort slider with tick labels for the model's efforts; it is disabled when the model has fewer than two efforts; dragging previews the position and releasing applies the effort if it changed. | ui | web/src/SettingsFields.tsx:139-176 | untested |
| B81 | A saved model or mode no longer in the catalog still shows (by its raw ID) so the control reflects the real setting. | ui | web/src/SettingsFields.tsx:35-36,73,188-191 | untested |
| B82 | The harness icon is the Claude mark for `claude`, a dashed-circle mark for `fake`, and nothing (empty icon labeled with the harness name) for others; display names are "Claude" and "Fake", else the raw ID. | ui | web/src/HarnessIcon.tsx:3-36 | untested |
| B83 | An approval request appears in a "Waiting on you" section above the composer, one card per pending approval, and disappears when answered or cancelled (from any page). | ui | web/src/SessionView.tsx:71-79; web/src/ox.tsx:130-138 | A:20, S:238 |
| B84 | A permission card reads "Allow <tool>?", shows the input's first string as a summary (or "Input" if none) that expands to the full input JSON, and offers Allow, Deny, and an "Other" text reply. | ui | web/src/ApprovalRequest.tsx:62-101 | A:20, S:238 |
| B85 | Submit stays disabled until Allow, Deny, or a non-blank Other reply is chosen; typing in Other selects it; choosing Allow or Deny clears the reply; Enter in the Other field submits. | ui | web/src/ApprovalRequest.tsx:33-48,92-98 | A:20 (partial) |
| B86 | Allow answers `allow` with the request's original input; Deny answers `deny` with message "Denied by user"; Other answers `deny` with the trimmed reply as the message. | ui | web/src/ApprovalRequest.tsx:40-48 | S:238, A:20 |
| B87 | While an approval answer is in flight the card's controls are disabled; a failed answer shows the session error alert and re-enables the card. | ui | web/src/ApprovalRequest.tsx:44-48; web/src/ox.tsx:248-249 | untested |
| B88 | An ExitPlanMode approval shows the plan rendered as Markdown with the input collapsed under "Input". | ui | web/src/ApprovalRequest.tsx:65-70 | A:47 |
| B89 | An AskUserQuestion approval shows a form: each question with its header and options (with descriptions), radios for single-select, checkboxes for multi-select, plus an "Other" option with a text field; typing in Other selects it (and deselects a single-select question's options); choosing an option in a single-select question unchecks Other but keeps its text. | ui | web/src/ApprovalRequest.tsx:123-215 | S:254, A:69 |
| B90 | The question form's Submit is enabled only when every question has an answer; it answers `allow` with the original input plus `answers` keyed by question text; multi-select answers join the chosen labels (in option order) and a non-blank Other text with ", ". | ui | web/src/ApprovalRequest.tsx:111-142 | S:254, A:69 |
| B91 | The question form's Decline answers `deny` with message "User declined to answer". | ui | web/src/ApprovalRequest.tsx:225-231 | S:254 |
| B92 | An AskUserQuestion whose input lacks the expected shape falls back to the ordinary permission card. | ui | web/src/ox.tsx:60-74; web/src/ApprovalRequest.tsx:14-16 | S:254 |
| B93 | The question form's Input details expands to the full input JSON. | ui | web/src/ApprovalRequest.tsx:217-223 | untested |
| B94 | Pending approvals present in a session's snapshot (e.g. after reload or reconnect) show again. | ui | web/src/ox.tsx:312,325 | untested |
| B95 | Dependency: e2e harness runs oxd and the relay per spec file on a temporary socket, database and port, restarts the relay on demand, and fails the run if oxd exits during the tests. (Test infrastructure, not user-facing.) | ui | web/e2e/services.ts:53-101 | n/a |

### Suspected defects

1. **A send whose reply is lost can be sent twice.** `web/src/connection.ts:36-43` rejects every in-flight request on a stream error, and `web/src/ox.tsx:193-197` then keeps the text and shows an error. The request may already have reached oxd; pressing Send again sends the prompt a second time. Only creation is idempotent (client-chosen session ID, `existing`). Docs (architecture.md:138-139) say reconnect "repeats no request", which holds for automatic retries but not for the user's retry.
2. **Deleted or unknown session leaves a blank main area with no message and a stale hash.** `web/src/App.tsx:41-46,90-97`. Deleting the open session (or opening `#<unknown id>`) shows nothing; the sidebar highlights nothing and the hash stays. Likely unintended.
3. **Page never recovers from an oxd restart without a manual reload.** `main.go:82-86` answers 502 when oxd is down; EventSource treats a non-200 reply as final (`web/src/connection.ts:39-41` shows "Reload the page to reconnect."). Documented in architecture.md:133-134, but a user restarting oxd must reload every page; the relay-restart case recovers by itself, the oxd-restart case does not.
4. **Whitespace-only submit gives no feedback.** `web/src/NewSessionView.tsx:51` / `web/src/ox.tsx:220,234`: `required` passes for "  ", then the submit is silently dropped. Same for Send in a session. Looks like a user confusion trap.
5. **Stop and approval buttons stay enabled while disconnected.** `web/src/SessionView.tsx:96-102`, `web/src/ApprovalRequest.tsx:103,232` do not check `ready`; clicking fails with "Not connected to ox" while Send/settings are disabled. Inconsistent with the disabled-while-disconnected rule (B19).
6. **Model picker stays open after choosing.** `web/src/SettingsFields.tsx:38-43,119` never hides the popover (light-dismiss only), so a choice made by click or Enter leaves it open. Possibly intended so the effort slider is reachable, but no test or doc says so.
7. **No request timeout.** `web/src/connection.ts:62-80`: a request that oxd never answers (and no stream error) stays pending forever; its session's `sending`/`creating`/`requested` flags then keep Send and settings disabled indefinitely.
8. **Non-loopback `-addr` accepts only the literal `-addr` host.** `main.go:39-43,72-77`: with `-addr 0.0.0.0:8080` or `:8080`, the host is `0.0.0.0`/empty, so a page reached by LAN IP or machine name gets 403 "unknown host" on `/events`. Probably intended safety, but the page itself loads and then fails with an opaque stream error ("Reload the page to reconnect.") since the 403 is not shown.
9. **Failure of `/events` shows only a generic message.** `web/src/connection.ts:36-45` discards the 502/403 body text, so the user is never told that oxd is unreachable or the host was refused.
10. **A reload failure after the catalog loaded leaves the page half-loaded.** `web/src/ox.tsx:331-353`: `setReady(true)` runs before sessions load, so Send in a draft is enabled while some session loads may later fail; failures only show as one alert and the failed sessions never appear until the next reconnect.
11. **Session `title()` can be empty.** `web/src/Sidebar.tsx:168-171`: a first prompt sent through the protocol with only whitespace gives an empty sidebar label (the page itself blocks such prompts). Low confidence.
12. **Add-workspace dialog keeps a failed path but Cancel does not reset it** (`web/src/Sidebar.tsx:73`): a stale path from an earlier cancelled attempt appears next time. Minor.

### Doc conflicts

- `docs/architecture.md:138-139` says the control "repeats no request" on reconnect; the code fails in-flight requests (B18), after which the user's Send can repeat one (defect 1). Not strictly a code/doc contradiction, flagged for the user.
- `docs/architecture.md:122-124` says `ox` answers `/events` and `/rpc` "only when the request's Host names" an allowed host and refuses `/rpc` from another origin: matches `main.go:66-79,59`. It does not mention that the static page is served for any Host (B5), `/events` has no Origin check, or the 1 MiB `/rpc` limit and 413 (`main.go:127-131`), which `docs/protocol.md` also omits (protocol.md:13 only says one JSON object per line).
- `docs/architecture.md:148-149` says "If the reply is lost, the page can recognize the session after reconnect"; the code also recovers from a session/created notification without a reconnect (`web/src/App.tsx:61-72`) and via `existing` in the create reply (`web/src/ox.tsx:224-226`). Wider than documented, not a contradiction.
- `docs/architecture.md:140-144` says the form shows "the requested values, each changed one with a spinner"; code matches (`web/src/SessionView.tsx:160-161,172`). No conflict found.

### Coverage

Read in full:
- cmd/ox/main.go
- cmd/ox/main_test.go
- cmd/ox/web/src/index.tsx
- cmd/ox/web/src/App.tsx
- cmd/ox/web/src/NewSessionView.tsx
- cmd/ox/web/src/SettingsFields.tsx
- cmd/ox/web/src/ox.tsx
- cmd/ox/web/src/Markdown.tsx
- cmd/ox/web/src/HarnessIcon.tsx
- cmd/ox/web/src/connection.ts
- cmd/ox/web/src/protocol.ts
- cmd/ox/web/src/Sidebar.tsx
- cmd/ox/web/src/ApprovalRequest.tsx
- cmd/ox/web/src/SessionView.tsx
- cmd/ox/web/e2e/session.spec.ts
- cmd/ox/web/e2e/workspace.spec.ts
- cmd/ox/web/e2e/approval.spec.ts
- cmd/ox/web/e2e/services.ts
- cmd/ox/web/e2e/ui.ts
- cmd/ox/web/AGENTS.md (context, via hook), docs/architecture.md lines 108-150; docs/protocol.md and docs/claude-code.md only grepped, not read in full.
