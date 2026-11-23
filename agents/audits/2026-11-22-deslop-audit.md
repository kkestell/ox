# Deslop audit

## Scope and evidence

The whole repository at `394daa4` on `main`, with a clean working tree. The baseline is `32db923`, the revision of the last deslop audit (`2026-11-20-002-deslop.md`). Read for earlier dispositions: every audit in `agents/audits/`, the reviews since the baseline, and the `bug` issues, open (#5, #6, #9, #15) and closed.

Ten GPT-6.2 Sol reviewers at high effort, run through the Codex companion, each worked in its own detached worktree at `394daa4` and covered all six lenses. Areas, one reviewer each, with the files and workflows each covered:

1. Claude harness: `internal/claude`, `docs/harnesses/claude-code.md`; an approval end to end, and start, resume, and parking.
2. Sessions and lifecycle: `session.go`, `server.go`, `history.go`, `conn.go`, `internal/agent`, `internal/fake`, `live_test.go`; park and wake, and archive and unarchive.
3. RPC methods and protocol: `methods.go`, `internal/jsonrpc`, `docs/protocol.md`; a prompt from the composer to the transcript, and every method against the protocol.
4. MCP: `mcp.go`, `cmd/ox-mcp`; child sessions and messaging.
5. Daemon, configuration, and store: `cmd/oxd`, `internal/config`, `internal/store`, `internal/paths`, `docs/architecture.md`; a settings change, and `oxd` startup and shutdown.
6. Ur: `internal/ur`; an Ur turn with shell approval, and an Ur conversation across a restart.
7. Relay and tooling: `cmd/ox`, the dev stack, the e2e services, `Makefile`, `scripts`, `.githooks`, `.github`, `AGENTS.md`, `.claude/skills`; the relay, and `make dev` start, restart, and shutdown.
8. Web state: `ox.tsx`, `connection.ts`, `protocol.ts`, `poll.ts`, `App.tsx`, `Sidebar.tsx`, `Panels.tsx`, `e2e/ui.ts`; reconnect, and the sidebar across two pages.
9. Web session view: `SessionView.tsx`, `ApprovalRequest.tsx`, `NewSessionView.tsx`, `SettingsFields.tsx`, `Markdown.tsx`, `toolInput.ts`, `style.css`, `session.spec.ts`, `approval.spec.ts`, `cmd/ox/web/AGENTS.md`; a new session, and questions and tool calls in the transcript.
10. Workspace files: `files.go`, `FileTree.tsx`, `FileView.tsx`, `highlight.ts`, `workspace.spec.ts`, and every requirement in `docs/functional-requirements.md`; opening a file, and path handling.

The reviewers confirmed behavior with stand-in `claude` scripts, in-memory JSON-RPC probes against the real server and SQLite, real Bash, the production page modules under a simulated transport, and mutations in temporary copies. The orchestrator confirmed every finding below against the source at `394daa4`, and its origin against `32db923` where the finding says so.

Gaps:

- The reviewers' sandbox refused TCP and Unix socket binds and Chrome would not start in it, so no browser e2e spec, `cmd/oxd` socket test, or socket-based Ur test ran under the reviewers. Findings about rendering (F2, F12, F16) are traced in the source and the stylesheet, not observed in a browser.
- No live Claude Code or OpenRouter check ran.
- The Ur reviewer's first turn was stopped by OpenAI's content filter partway through its shell probes. It was resumed in the same thread and finished its pass.

## Summary

Nothing was fixed.

| ID  | Severity | Lens              | Origin              | Action   | Title                                                                    |
| --- | -------- | ----------------- | ------------------- | -------- | ------------------------------------------------------------------------ |
| F1  | High     | Broken guarantees | Introduced          | Plan     | Ur can run a command its approval card does not show                     |
| F2  | Medium   | Broken guarantees | Introduced          | Plan     | On a narrow screen an open file hides the page's alerts                  |
| F3  | Medium   | Broken guarantees | Unknown             | Plan     | One failed snapshot after a reconnect leaves a session out of sync       |
| F4  | Medium   | Broken guarantees | Present at baseline | Plan     | One Ctrl-C under `make dev` can still signal `oxd` twice                 |
| F5  | Medium   | Hollow tests      | Mixed               | Plan     | Tests that pass when the behavior they name breaks                       |
| F6  | Low      | Broken guarantees | Present at baseline | Plan     | A model change overwrites another page's effort                          |
| F7  | Low      | Broken guarantees | Mixed               | Plan     | Two kinds of failed request show no error                                |
| F8  | Low      | Broken guarantees | Present at baseline | Record   | A Claude process that stops reading stdin blocks its session and others  |
| F9  | Low      | Broken guarantees | Introduced          | Plan     | A second stop signal leaves Ur's commands running                        |
| F10 | Low      | Broken guarantees | Present at baseline | Plan     | `CronDelete` keeps a session from parking                                |
| F11 | Low      | Broken guarantees | Present at baseline | Plan     | Claude's plain-text failure after `initialize` is dropped                |
| F12 | Low      | Broken guarantees | Introduced          | Plan     | An interrupted Ur command shows as failed, not stopped                   |
| F13 | Low      | Broken guarantees | Introduced          | Plan     | Two Ur command outcomes are misreported                                  |
| F14 | Low      | Broken guarantees | Present at baseline | Plan     | Configuration accepts Claude permission modes Claude refuses             |
| F15 | Low      | Broken guarantees | Present at baseline | Rejected | Live session order differs from creation order under concurrent creation |
| F16 | Low      | Broken guarantees | Introduced          | Plan     | The side panels can squeeze the transcript to nothing                    |
| F17 | Low      | Broken guarantees | Introduced          | Plan     | Highlighting doubles a lone carriage return at the end of a file         |
| F18 | Low      | Broken guarantees | Present at baseline | Plan     | Three small input and display bugs in the web client                     |
| F19 | Low      | Over-engineering  | Mixed               | Rejected | Text fragments are joined by repeated concatenation                      |
| F20 | Low      | Comments and docs | Mixed               | Plan     | Documents and comments that contradict the code                          |
| F21 | Low      | Dead code         | Mixed               | Plan     | Small dead code and needless work                                        |
| F22 | Low      | Comments and docs | Present at baseline | Plan     | Three functional requirements disagree with the code                     |
| F23 | Low      | Broken guarantees | Present at baseline | Record   | Stop does nothing while a resumed Claude runs a startup turn             |
| F24 | Low      | Broken guarantees | Present at baseline | Rejected | `list_sessions` during an archive lists the family as parked             |
| F25 | Low      | Broken guarantees | Present at baseline | Rejected | Stop during first startup leaves no interruption in the transcript       |
| F26 | Low      | Broken guarantees | Mixed               | Rejected | Paths and files outside normal use                                       |
| F27 | Low      | Broken guarantees | Mixed               | Rejected | Double faults and injected orderings with no normal trigger              |
| F28 | Low      | Broken guarantees | Introduced          | Rejected | Ur cannot contain processes that leave their group                       |
| F29 | Low      | Broken guarantees | Present at baseline | Rejected | Typed denials are trimmed                                                |
| F30 | Low      | Hollow tests      | Mixed               | Rejected | Tooling findings cheaper to keep                                         |

## Changes

The `Plan` findings go through the project workflow as six changes, one `bug` issue each. `F8` and `F23` are recorded as `bug` issues of their own.

| Change         | Findings                         | Owner                                                           |
| -------------- | -------------------------------- | --------------------------------------------------------------- |
| Ur shell       | F1, F9, F12, F13                 | `internal/ur`, and the forced exit in `cmd/oxd`                 |
| Page errors    | F2, F3, F7                       | `ox.tsx`, `poll.ts`, `connection.ts`, `App.tsx`                 |
| Dev shutdown   | F4                               | `cmd/ox/web/stack.mjs`                                          |
| Claude harness | F10, F11, F14                    | `internal/claude`                                               |
| Hollow tests   | F5                               | the tests it names                                              |
| Cleanup        | F6, F16, F17, F18, F20, F21, F22 | docs, comments, dead code, small web bugs, and requirement text |

Ur shell goes first, for F1.

## F1. Ur can run a command its approval card does not show

High · Broken guarantees, Duplication · Introduced (`c31a116`) · Plan

Ur parses a shell call's arguments twice, with two parsers that disagree. The approval card's input is checked with `json.Unmarshal`, which rejects a JSON object followed by trailing text and publishes `{}` instead. The command that runs is parsed with a `json.Decoder`, which accepts the first JSON value and ignores the rest. When the model emits a valid command object followed by trailing text, the page asks the user to approve `{}`, and Allow runs the command the card never showed. In auto mode the transcript records `{}` as the tool's input. This breaks **Allow and Deny**, **Tool calls**, and **Ur's shell commands**.

### Places

- `internal/ur/ur.go:324` — checks the arguments for display and substitutes `{}`.
- `internal/ur/shell.go:51` — `parseShell` decodes the first JSON value only.
- `internal/ur/ur.go:343` — the `asking` step keeps the parsed command beside the substituted input.

### Evidence

Reproduced by two reviewers through the real `session/send` and `session/approve` handlers with a canned provider response: approving `{}` ran a benign command, and a fresh connection received the same `{}` approval from `session/get`. The orchestrator confirmed both parsers in the source. `TestShellArguments` and `TestShellInvalidCall` use inputs both parsers agree on.

### Action

Check the arguments once, at the boundary: `parseShell` refuses trailing data, and the published input and the approval request are built from the one parsed value, so the card and the command cannot differ. Keep a Ur test whose provider returns an object with trailing text and that asserts the call is answered as invalid and nothing runs.

## F2. On a narrow screen an open file hides the page's alerts

Medium · Broken guarantees · Introduced (`0484a03`) · Plan

The page's connection alert and a failed creation's error with its **Reopen draft** link render inside `<main>`. On a narrow screen `<main>` is hidden while the file viewer is open. A phone user reading a file sees neither a lost connection nor a failed creation, and cannot reach the failed draft until they close the file. This breaks **Lost connection** and **Failed creation**.

### Places

- `cmd/ox/web/src/App.tsx:151` — the alerts sit inside `<main>`.
- `cmd/ox/web/src/style.css:450` — `.narrow main:has(~ .viewer) { display: none; }`.

### Evidence

Traced in the source and stylesheet by three reviewers and the orchestrator. The phone spec at `workspace.spec.ts:768` checks that `<main>` is hidden; the connection specs check the alert with no file open.

### Action

Render page-level alerts outside `<main>`, where every layout shows them. Keep a phone-viewport e2e test that opens a file, cuts the connection, and expects the alert.

## F3. One failed snapshot after a reconnect leaves a session out of sync

Medium · Broken guarantees · Unknown · Plan

After the event stream reconnects, the page loads each session's snapshot once with `session/get`. When one of those requests fails, the page shows the error and leaves that session reloading, and `receive` drops every later update for a session that is not live. The session keeps its old transcript and a disabled Send until the next reconnect or reload, while the connection itself is healthy. This breaks **Every page in sync** and **One transcript**.

### Places

- `cmd/ox/web/src/ox.tsx:362` — `load` rethrows any error but not-found.
- `cmd/ox/web/src/ox.tsx:427` — loads every listed session once.
- `cmd/ox/web/src/ox.tsx:433` — updates for a session that is not live are dropped.

### Evidence

Reproduced with the production page modules under a simulated transport: one failed POST, a second session loaded, and a prompt delivered over the healthy stream left the first session with its old transcript and Send disabled. The per-session reconnect spec (`session.spec.ts:1020`) holds and then releases the snapshot successfully.

### Action

A failed snapshot load is retried by the session's owner until it succeeds or the stream drops, or the session shows its own error with a retry. The plan picks one. Keep an e2e test that fails one session's `session/get` after a reconnect and expects that session to catch up.

## F4. One Ctrl-C under `make dev` can still signal `oxd` twice

Medium · Broken guarantees · Present at baseline (`c3b56a9`) · Plan

A terminal Ctrl-C sends SIGINT to every service in the terminal's process group, and the supervisor's own SIGINT handler stops the stack without signalling them again. When a service's `exit` event reaches the supervisor before its SIGINT handler does, the exit handler calls `stop(code ?? 1)`, which sends SIGTERM to every running service. `oxd` takes that second signal as a forced exit: it kills Claude and exits 1, skipping graceful shutdown. This is the outcome the earlier audit's F2 fixed for the direct path. It breaks **Stopping oxd** under `make dev`.

### Places

- `cmd/ox/web/stack.mjs:95` — an unexpected exit stops the stack with SIGTERM.
- `cmd/ox/web/stack.mjs:151` — the SIGINT handler that should win.
- `cmd/oxd/main.go:104` — a second signal forces the exit.

### Evidence

Reproduced with stand-in services by injecting the callback order: the stand-in daemon received SIGINT then SIGTERM. The orchestrator confirmed the path in the source. The Ctrl-C tests in `stack.test.mjs:156` and `:171` deliver the supervisor's signal while the stand-ins are still alive.

### Action

The supervisor decides once that a terminal Ctrl-C is under way, before it judges any service's exit, for example by treating an exit on SIGINT as the start of the Ctrl-C shutdown. Keep a stack test that delivers a service's SIGINT exit before the supervisor's SIGINT.

## F5. Tests that pass when the behavior they name breaks

Medium · Hollow tests · Mixed · Plan

Each test below passes when the behavior it names breaks. Reviewers confirmed the ones marked "mutation" by breaking the code in a temporary copy; the orchestrator confirmed the rest in the source.

### Places

- `internal/server/live_test.go:936` — `TestLiveUr` waits for `tool_use`, published before the command starts, and checks a stopped result Ur synthesizes. It passes if the command never starts or survives the interrupt. Three reviewers found it.
- `internal/claude/configure_test.go:248` — `TestConfigureTimeoutStopsProcess` observes the process only after both the stale and current timers fire. It passed with the request-ID check removed (mutation).
- `internal/claude/park_test.go:22` and `internal/server/live_test.go:830` — removing the Stop hook registration from `initialize` left the non-live suites green (mutation); the live check starts with `mayHaveSchedules` already set.
- `internal/server/history_test.go:65` — `TestUnrecordedEndIsRecordedAtLoad` inspects history only after both servers close. It passed with the repair moved from load to shutdown (mutation).
- `cmd/ox/web/e2e/session.spec.ts:1490` — "archived on every page" opens page B after the archive, so B reads the flag from `workspace/list`, not the notification. Two reviewers found it.
- `internal/server/methods_test.go:1083` — `TestApprovalAnsweredOnce` claims every client hears the answer but uses one connection.
- `internal/server/methods_test.go:75` — `TestInvalidLines` uses `ping` without an ID. It passed when ID-less requests were dispatched with their replies suppressed (mutation).
- `internal/server/methods_test.go:1225` — `TestSameDirectoryIsOneWorkspace` passed when a duplicate add broadcast `workspace/created` again (mutation), which the page would show as a second workspace.
- `internal/server/mcp_test.go:970` — returning `null` for an empty `list_sessions` passed every MCP test (mutation).
- `internal/server/mcp_test.go:105` — `TestReadSession` builds its expected timestamp with the production `timeLayout`, so a changed format passes (mutation).
- `cmd/ox/web/e2e/session.spec.ts:259` — the pending-settings spec checks the new mode before waiting for the pending indicator to clear, so the optimistic value satisfies it.
- `cmd/ox/web/e2e/session.spec.ts:773` — the Other-answer spec no longer selects a label before checking an empty Other; removing the radio handler's reset passed (mutation).
- `cmd/ox/web/highlight.test.mjs:16` — the highlighting tests passed with Shiki bypassed and the source wrapped in one span (mutation).
- `cmd/ox/web/e2e/session.spec.ts:1321` — the wide-table spec checks the table's outer box only; the `reply()` helper's wait can pass on the first fragment.

### Evidence

As listed under Places.

### Action

Make each test observe the outcome it names: wait for the shell child in `TestLiveUr`; observe the process between the stale and current timers; send a real Stop callback through `initialize`; inspect history before shutdown; keep page B open during the archive; add a second connection; use a method with a side effect; assert no second `workspace/created`; assert `[]` and a literal timestamp; wait for the pending indicator; restore the label-then-Other sequence; assert distinct token colors; assert scroll reach inside the table.

## F6. A model change overwrites another page's effort

Low · Broken guarantees · Present at baseline · Plan

The model picker computes the new model's fallback effort from the page's snapshot and sends it as an explicit effort change. When another page changes the effort first, the delayed model change still writes the stale fallback, and a model-only change can be refused because of the other page's effort. `AGENTS.md` requires that a request carry only what the user changed, with `oxd` applying it to the current state. This breaks **Effort across models** across two pages.

### Places

- `cmd/ox/web/src/SettingsFields.tsx:41` — computes the fallback effort on the page.
- `internal/server/methods.go:495` and `internal/config/config.go:196` — where `oxd` could choose it against current state.

### Evidence

Reproduced over two RPC connections, both the overwrite and the refusal. The two-page e2e test changes independent fields.

### Action

The page sends only the model; `oxd` keeps the session's effort when the new model offers it and otherwise takes the model's fallback. Keep a Go test with two connections.

## F7. Two kinds of failed request show no error

Low · Broken guarantees · Mixed · Plan

A failed `fetch` of `/rpc` rejects as `Disconnected`, and the file tree and file viewer pollers discard `Disconnected`, because their comment assumes the page reports the lost connection. The page reports only event-stream errors, so with the stream healthy a first listing stays blank, or a stale one stays, with no error. Separately, when Vite answers a relay restart with an empty `502`, the page rejects the request with `Error("")`, and the views hide empty errors, so a failed send or creation loses its error and its **Reopen draft** link. Both break **Every request settles**.

### Places

- `cmd/ox/web/src/poll.ts:24` — drops `Disconnected` (introduced with `poll.ts`).
- `cmd/ox/web/src/connection.ts:84` — a failed `fetch` becomes `Disconnected`.
- `cmd/ox/web/src/connection.ts:80` — an empty body becomes an empty error (present at baseline).

### Evidence

Both reproduced with the production modules: an aborted POST under a healthy stream left the file pane blank, and an injected empty `502` produced `Error("")`. `workspace.spec.ts:380` asserts only that the old listing stays.

### Action

A failed POST is the request's error, not the connection's, and an empty body falls back to the status text. Keep e2e tests for both.

## F8. A Claude process that stops reading stdin blocks its session and others

Low · Broken guarantees · Present at baseline · Record

Prompts, messages, and approval answers are written to Claude's stdin synchronously on the session goroutine. When Claude stops reading and the pipe fills, that goroutine blocks, and with it the request, `session/get`, a peer's `list_sessions`, and `Server.Close`, so the first stop signal cannot shut `oxd` down. This breaks **Every request settles**, **Independent sessions**, and **Stopping oxd** under the fault. No real Claude process has been seen to stop reading stdin, which keeps this Low.

### Places

- `internal/claude/claude.go:356` — the synchronous write.
- `internal/claude/prompt.go:55` and `internal/claude/approval.go:43` — its callers on the session goroutine.
- `internal/server/server.go:153` — shutdown posts to every session.

### Evidence

Reproduced by three reviewers with a stand-in Claude that stopped reading stdin, through send, approval, and `list_sessions`; killing the stand-in released the write.

### Action

Open a `bug` issue. Worst cost: one hung Claude process blocks its session, peers' `list_sessions`, and graceful shutdown until it is killed. Found by three reviewers with a stand-in. Waits on a live observation of Claude Code stopping reading stdin; the fix moves stdin writes off the session goroutine, which `AGENTS.md`'s rule for harness goroutines would have to allow.

## F9. A second stop signal leaves Ur's commands running

Low · Broken guarantees · Introduced (`c31a116`) · Plan

On a second signal `oxd` kills registered Claude processes and exits at once. Ur's command groups are killed only on the graceful path, when the session cancels the turn and `ur.WaitAll` returns. A Ur command in its own process group survives a second signal that arrives first. This breaks **Stopping oxd**. F4 makes a second signal reachable from one Ctrl-C.

### Places

- `cmd/oxd/main.go:104` — the forced exit.
- `internal/ur/shell.go:75` — command groups are only waited for, not killed, on that path.

### Evidence

Reproduced with an injected delay before `Server.Close`: an actual Ur shell command survived the daemon's exit; in the single-signal control it was gone.

### Action

The forced exit kills Ur's command groups as it kills Claude. Keep a daemon test.

## F10. `CronDelete` keeps a session from parking

Low · Broken guarantees · Present at baseline · Plan

The harness marks a session as possibly holding schedules when Claude calls `CronDelete`, though a deletion cannot create one. If the turn then ends without a Stop callback, for example by failing, the idle session never parks. This breaks **Parking** for a fresh session, apart from the resumed case #5 records.

### Places

- `internal/claude/agent.go:221` — `CronDelete` sets `mayHaveSchedules`.

### Evidence

Reproduced with a stand-in: a fresh process, a `CronDelete`, and a failed turn with no Stop callback stayed unparked while a control parked.

### Action

Only `CronCreate` and `ScheduleWakeup` set the flag. Keep a park test with `CronDelete`.

## F11. Claude's plain-text failure after `initialize` is dropped

Low · Broken guarantees · Present at baseline · Plan

`docs/harnesses/claude-code.md` records failures Claude prints as plain text on stdout, such as `API Error: Connection refused`, and one of them appears only once the first prompt reaches the API. `Handle` keeps plain text only while starting. After `initialize`, the reason is dropped and the transcript shows only `Claude exited: exit status 1`. This breaks **Unexpected exits**.

### Places

- `internal/claude/agent.go:144` — plain text kept only in `starting`.
- `internal/claude/agent.go:338` — the exit reason after startup.

### Evidence

Reproduced with a stand-in that acknowledged `initialize`, read the prompt, printed the error, and exited 1. `TestUnexpectedExitIsReported` supplies its reason on stderr.

### Action

The last plain-text line is kept in every phase and reported with an unexpected exit. Keep a test with the reason on stdout after `initialize`.

## F12. An interrupted Ur command shows as failed, not stopped

Low · Broken guarantees · Introduced (`c31a116`) · Plan

On an interrupt Ur publishes the running call's result as an error with its stopped text, and the transcript marks any error result `failed`. The user who pressed Stop sees the command as failed, live and after a reload. **Tool calls** distinguishes failed from stopped.

### Places

- `internal/ur/ur.go:478` — the stopped call's result is an error.
- `cmd/ox/web/src/SessionView.tsx:166` — any error result is `failed`.

### Evidence

Reproduced at the wire by the Ur reviewer and traced through rendering; the orchestrator confirmed both in the source.

### Action

An interrupted call is shown as stopped. The plan decides whether Ur publishes no result, as for a call cut off by a restart, or a result the page reads as stopped.

## F13. Two Ur command outcomes are misreported

Low · Broken guarantees · Introduced (`c31a116`) · Plan

A shell that exits 0 within a second of its timeout, leaving a background child holding the output pipe, is reported as timed out: `Wait` spends the one-second `WaitDelay` on the pipe, and `runShell` checks the deadline before the exit status. Separately, when Ur cannot save a finished command's outcome, it ends the turn with an error and publishes no tool result, so the transcript shows the command running until parking and then stopped, and after a restart the model is told the user stopped it. Both break **Ur's shell commands**.

### Places

- `internal/ur/shell.go:107` — the deadline is checked before the status.
- `internal/ur/ur.go:366` — a failed save ends the turn without the result.

### Evidence

The timeout case was reproduced in four runs with a one-second timeout; the save case with a denied JSONL write and a store reopen, with a marker file proving the command finished.

### Action

Report the exit status of a shell that exited on its own, and publish the outcome before reporting that it could not be saved. Keep a shell test and a save-failure test.

## F14. Configuration accepts Claude permission modes Claude refuses

Low · Broken guarantees · Present at baseline · Plan

Claude's configuration check refuses only `default`. A typo such as `acceptEdit` or `manul` passes configuration loading and `oxd`'s startup check, is offered on the page, and fails when Claude starts with it. This breaks **Configuration checks**.

### Places

- `internal/claude/claude.go:126` — `checkPermissionMode`.
- `docs/harnesses/claude-code.md:563` — Claude's allowed modes, as observed.

### Evidence

Reproduced by two reviewers with Go probes; the CLI's refusal is the observed behavior in the harness notes.

### Action

Check permission modes against the modes Claude accepts. Keep a startup test with a misspelled mode.

## F15. Live session order differs from creation order under concurrent creation

Low · Broken guarantees · Present at baseline · Rejected

A child's `created_at` is set before its launch, but it joins the workspace's session list only when the launch completes. A session created in the same workspace during the launch is listed first. After a restart the store loads by `created_at`, and the order flips. `workspace/list` and `list_sessions` promise oldest first. The sidebar groups children under their parents, so the page shows no difference.

### Places

- `internal/server/mcp.go:476` — `CreatedAt` is set before launch.
- `internal/server/methods.go:374` — publication appends.
- `internal/store/store.go:172` — the restart order.

### Evidence

Reproduced by three reviewers through `workspace/list` and `list_sessions`, before and after a restart.

### Action

Rejected: cheaper to keep. The page groups children under their parents and shows no difference, and only a protocol client listing during a slow child launch sees the order flip.

## F16. The side panels can squeeze the transcript to nothing

Low · Broken guarantees · Introduced (`64d1e65`) · Plan

Each sash limits its own panel without reserving the center. At 960 px with both side panels open, the session list can grow to 640 px and the file tree to 320 px, leaving the transcript and composer a zero-width column. This breaks **Resizing panels**.

### Places

- `cmd/ox/web/src/Panels.tsx:97` — the per-panel limits.
- `cmd/ox/web/src/style.css:224` — the center's minimum width is zero.

### Evidence

The drag limits were reproduced with the production module; the rendering is traced, not observed. The earlier audit's F3 (`2026-11-22-001`) fixed the file split, not the two side panels together.

### Action

Each panel's limit leaves the center its minimum width. Keep an e2e test at a narrow desktop width.

## F17. Highlighting doubles a lone carriage return at the end of a file

Low · Broken guarantees, Duplication · Introduced (`27efb42`) · Plan

`highlight` splits the source a second time to restore the carriage returns it assumes Shiki dropped. Shiki keeps a final `\r` that no newline follows, so a file ending in a lone `\r` renders two. The viewer shows that HTML as the file's text. This breaks **File viewer**.

### Places

- `cmd/ox/web/src/highlight.ts:152` — the second split and its comment.

### Evidence

`highlight("a.go", "last\r")` emitted two `&#13;`. The fixtures cover CRLF and embedded carriage returns only.

### Action

Restore only the carriage returns Shiki dropped. Add the case to `highlight.test.mjs`.

## F18. Three small input and display bugs in the web client

Low · Broken guarantees · Present at baseline · Plan

- Enter in the model picker's search field selects a model even while an input method is composing, so confirming a composition can change a session's model (`SettingsFields.tsx:56`). The prompt field already skips composing Enter.
- A `SendMessage` whose text starts with a newline shows a blank transcript summary, since the summary takes the first line (`SessionView.tsx:166`, `toolInput.ts:22`). This breaks **Tool calls**.
- A workspace added with a trailing slash shows its full path as its name, and the e2e locator repeats the same expression, so the tests would accept it (`ox.tsx:69`, `e2e/ui.ts:25`).

### Evidence

Each reproduced with the compiled component or function.

### Action

Skip Enter while composing; summarize the first nonblank line; take the name after trailing slashes, with a test that adds such a path.

## F19. Text fragments are joined by repeated concatenation

Low · Over-engineering · Mixed · Rejected

`history.entries` joins a reply's `text_delta` fragments with `+=`, recopying the growing reply on the session goroutine for every `session/get` and `read_session`, including a paged read that returns a small part. Ur's end-of-turn handler rebuilds the whole conversation, with its tool-call repair, to read the final message, joining fragments the same way.

### Places

- `internal/server/history.go:69` — present at baseline.
- `internal/ur/ur.go:252` — introduced with Ur.

### Evidence

Measured: one replay of a 128 KiB reply in 16-byte fragments allocated 583 MB under the race detector; Ur's rebuild of 8,192 fragments allocated 289 MB.

### Action

Rejected: an unmeasured cost in use. The allocations come from a synthetic benchmark with tiny fragments; no slow `session/get` or Ur turn has been seen.

## F20. Documents and comments that contradict the code

Low · Comments and docs · Mixed · Plan

### Places

- `docs/architecture.md:110` — says no session goroutine posts to another; archive posts holds from ancestors to descendants and waits (three reviewers).
- `docs/architecture.md:52` — says Ur appends every shell outcome to its JSONL; an interrupted call's outcome is reconstructed, not appended.
- `docs/protocol.md:378` — says an interrupt with no running turn ends with `idle`; idle Ur sends nothing.
- `docs/protocol.md:180` — promises a workspace-relative path in folder-listing errors; they name the absolute directory.
- `internal/server/methods.go:387` — `getSession` says earlier updates are in the history; queue and approval changes are in the snapshot's fields, and a cancellation can leave no entry.
- `internal/agent/agent.go:146` and `internal/server/session.go:365` — say a parked session runs no process; `stop` publishes `parked` while the process may still be exiting, as `docs/protocol.md:483` records.
- `internal/claude/approval.go:41` — says a broken pipe means the process exited; a process can close stdin and keep running.
- `docs/harnesses/claude-code.md:332` — says to reject undeclared hook callbacks; Ox acknowledges any.
- `cmd/ox/web/AGENTS.md:23` — names `highlight.test.mjs` as the Node suite; `make test` also runs `stack.test.mjs` and `toolInput.test.mjs`.
- `cmd/ox/web/e2e/ui.ts:170` — says `hold` matches requests whose params include the keys; it also requires equal values, which a reconnect spec relies on.
- `cmd/ox/web/src/poll.ts:9` — says the page reports a failure to reach `ox`; see F7.
- `AGENTS.md:120` — says a harness's goroutines only read its process and fire timers; Ur's make HTTP requests and run commands.
- `AGENTS.md:207` and `.claude/skills/ox-work/SKILL.md:13` — a child that finds uncommitted work outside its brief stops, while `/ox-work` runs children concurrently in one checkout, where each sees its siblings' edits.

### Evidence

Each confirmed in the source by the orchestrator.

### Action

Correct each passage to state the code's behavior, or, for `AGENTS.md:207`, exempt sibling children's files from the stop rule.

## F21. Small dead code and needless work

Low · Dead code, Over-engineering, Duplication · Mixed · Plan

### Places

- `internal/claude/claude_test.go:47` — `failingStart` returns an exit channel its one caller discards.
- `internal/claude/claude.go:139` — an error branch for marshalling fixed string maps, which cannot fail.
- `internal/agent/agent.go:193` — `Approval.ToolName` repeats the name inside the raw request; the page reads one, `read_session` the other.
- `internal/server/session.go:91` — `newSession` runs startup history repair for freshly inserted sessions, which have no history, holding `Server.mu` during the query on top-level creation.
- `internal/store/store.go:41` and `internal/server/session.go:49` — child metadata has two identical shapes and a field-by-field copy.
- `internal/fake/fake.go:136` and `:168` — the fake's two input paths disagree on its special commands.
- `cmd/ox/web/src/Panels.tsx:8` — `Drawer` is exported with no importer.
- `cmd/ox/web/src/Markdown.tsx:9` — delegates to a `link_open` rule markdown-it never has; the `reply` class has no consumer.
- `cmd/ox/web/src/SettingsFields.tsx:195` — `Select`'s optional change handler; both callers pass one.
- `cmd/ox/web/src/style.css:419` — the file tab's hover background repeats one that already wins.
- `cmd/ox/web/stack.mjs:15` and `:57` — `exited`'s export and default argument are unused; the stack keeps every clean retired exit.
- `cmd/ox/web/e2e/services.ts:95` — missing-relay branches unreachable once `startServices` returns.
- `cmd/ox/web/e2e/ui.ts:213` — `cutReply` forwards every RPC result and keeps generic waiters for one `reply("existing")` caller.

### Evidence

Each confirmed in the source against its callers.

### Action

Remove each, and give the fake one input path.

## F22. Three functional requirements disagree with the code

Low · Comments and docs · Present at baseline · Plan

The user owns `docs/functional-requirements.md`. The user decided each mismatch during the audit: the requirement text changes in all three, and the code stays.

### Places

- **Claude can start child sessions** (`functional-requirements.md:163`) lets Claude pick a child's permission mode among others Ox offers; `create_session` has no permission mode argument and always takes the parent's (`mcp.go:472`).
- **Database failures** (`:188`) says the page does not show a failed write; workspace creation and removal show their storage failures in the dialog (`methods.go:142`, `:209`).
- **Ox's own files** (`:354`) omits the configuration `oxd` writes on first start (`config.go`). It also omits the database's lock file, which the user considers an implementation detail the requirement need not name.

### Evidence

Confirmed in the source.

### Action

Apply this text, approved by the user, in the Cleanup change:

- **Claude can start child sessions:** "The child takes its parent's permission mode, and its parent's harness, model, effort, and directory unless Claude picks others Ox offers, and can start children of its own up to the configured depth."
- **Database failures:** "`oxd` logs a failed write of a transcript or a setting, and the page does not show it." The failure of adding or removing a workspace shows in its dialog.
- **Ox's own files:** "Ox's own bookkeeping writes only its database, its socket, the configuration it writes on first start, and Ur's saved conversations."

## F23. Stop does nothing while a resumed Claude runs a startup turn

Low · Broken guarantees · Present at baseline · Record

A resumed conversation can start a turn during startup, as the harness notes record. While Claude is starting, `Interrupt` drops the queued prompt and writes nothing, yet the page offers Stop once that turn reports `running` or `requires_action`. A stand-in that asked for approval before answering `initialize` kept its turn running after Stop. This breaks **Stop** if real Claude reports state or asks for approval before answering `initialize`.

### Places

- `internal/claude/prompt.go:104` — `Interrupt` writes nothing while starting.

### Evidence

Reproduced with a stand-in and an injected ordering; not reproduced with real Claude.

### Action

Open a `bug` issue. Worst cost: Stop is ignored for a startup turn, which runs to its end. Found by the Claude harness reviewer with a stand-in. Waits on a live reproduction of a resumed Claude starting a turn before it answers `initialize`.

## F24. `list_sessions` during an archive lists the family as parked

Low · Broken guarantees · Present at baseline · Rejected

`list_sessions` picks unarchived sessions under `Server.mu` and then reads each one's state on its goroutine, so a list that overlaps an archive can show the family as `parked`, and a send to them is refused as archived. A list that finished just before the archive would show them idle with the same refused send. Every outcome is one the two independent requests allow.

## F25. Stop during first startup leaves no interruption in the transcript

Low · Broken guarantees · Present at baseline · Rejected

Stopping the first prompt while Claude starts restores the prompt with no interruption entry. `docs/protocol.md` specifies this outcome before a turn starts; the finding misreads the intended behavior.

## F26. Paths and files outside normal use

Low · Broken guarantees · Mixed · Rejected

Unusual paths and files, each with a cost of handling beyond its benefit, as the earlier rejection of per-site path cleaning (`2026-11-20-003` F5) holds: a `-db` path through a symlink to a live database takes a different lock; `-config` or `-db` through `link/..` creates the lexical parent; listing a folder that became a named pipe blocks until a writer opens it; a top-level directory named `__proto__` cannot expand; an emptied Ur conversation file loads as an empty conversation; an empty CSS file crashes `make metrics`.

## F27. Double faults and injected orderings with no normal trigger

Low · Broken guarantees · Mixed · Rejected

Speculative: a child launch failure whose compensating delete also fails leaves the child after a restart; a draft's retry after another page deletes its new session during a lost reply loses the draft's edits; a reply withheld while the event stream stays healthy, which the relay cannot produce, stalls a poller; a `null` question input from the fake harness throws, with no evidence real Claude sends it; a lost send reply combined with another page's Stop loses the cancelled prompt; the tracker keeps a drained input's text until the next append.

## F28. Ur cannot contain processes that leave their group

Low · Broken guarantees · Introduced (`c31a116`) · Rejected

A command that starts a job in a new process group, such as with `set -m`, survives Stop. Containing processes that leave the group needs operating-system containment Ox does not have; the cost exceeds the benefit.

## F29. Typed denials are trimmed

Low · Broken guarantees · Present at baseline · Rejected

The approval card trims the denial text it sends. Surrounding whitespace carries nothing Claude uses; cheaper to keep.

## F30. Tooling findings cheaper to keep

Low · Hollow tests, Duplication · Mixed · Rejected

Each costs more to change than to keep: Make reports its own SIGINT status for both a clean and a failed `make dev` stop; `make -j2 dev` builds `ox` before the web bundle; a targeted SIGTERM during a rebuild leaves the build running; the e2e fixture's readiness check passes before `oxd` serves, which queued requests absorb; a fixture that fails to start leaves its temporary directories; the pre-commit hook checks working files, not the staged ones, which CI catches; `dev.mjs` and Vite share their port defaults through `OX_RELAY_PORT`; the relay tests leave the Tailscale listener's wiring untested, as the earlier rejection of the Tailscale code (`2026-11-22-002` F15) holds; `TestClaudeStartsWithoutAnAPIKey` rejects any `ANTHROPIC_` variable; `ApprovalRequest` rescans earlier pages on Submit.

## Checks run

- Race-enabled `go test` passed for `internal/claude`, `internal/agent`, `internal/server`, `internal/store`, `internal/config`, and `cmd/ox`; Ur's socket-free tests passed, and its full suite passed with an in-process transport.
- The web build, web lint and type checks, `go mod tidy -diff`, `dprint check`, and all 19 Node tests passed; Playwright listed 128 tests across four shards.
- Probes: stand-in `claude` scripts for F8, F10, F11, and F23; in-memory JSON-RPC against the real server and SQLite for F1, F6, F13, and F15; production page modules under a simulated transport for F3 and F7; mutations in temporary copies for F5.
- Blocked by the reviewers' sandbox: browser e2e, `cmd/oxd` and `cmd/ox-mcp` socket tests, and socket-based Ur tests. Not run: `make live`.
