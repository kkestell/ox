# Full-repository multi-lens review

## Scope and coverage

The whole repository at `733ac2a`: `oxd` (`cmd/oxd`, `internal/*`), the relay (`cmd/ox`), the MCP forwarder (`cmd/ox-mcp`), the web client (`cmd/ox/web`), and the docs. It was checked against every requirement in `docs/functional-requirements.md` and the rules in `AGENTS.md`. There was no brief. Five GPT-6.2 Sol reviewers each took one lens: requirements conformance; state, lifecycle, and concurrency; design and simplicity; the web client and relay; tests, protocol, and docs. Each finding below was traced in the source before it was fixed. The reviewers had confirmed most of them with live Haiku probes, Chrome runs, injected faults, or code mutations. Two Sol sessions made the fixes, one for Go and docs and one for the web client.

Gaps:

- **Subscription billing** was checked only to the extent of credential stripping. No billing audit was possible.
- The plan-mode approval branch of **Claude can message other sessions** was not exercised live, because Haiku declined to call the tool.

Rejected findings:

- *A background Bash tool shows done at launch.* The tool call has finished. The background task it started is separate, and the session already holds off parking while it runs (**Parking**). This misreads **Tool calls**.
- *A stalled SSE reader keeps a dead daemon connection in the relay.* Only a raw-socket client that stops reading triggers it. Browsers read event streams continuously. Speculative.
- *The fake harness accepts `null` tool input.* The fake is a test harness, and its commands are written by tests. The fix costs more than the defect.
- *A timed-out control keeps an empty request ID that an ID-less reply would match.* Claude always echoes `request_id` in control responses. Speculative.

## Summary

| ID  | Severity | Category       | Fix   | Title |
| --- | -------- | -------------- | ----- | ----- |
| F1  | High     | Correctness    | Fixed | The resume token ignores `/clear` and a first turn without a result |
| F2  | High     | Concurrency    | Fixed | A refused second `oxd` writes into the live daemon's history |
| F3  | High     | Correctness    | Fixed | `/model` and `/effort` prompts change settings behind Ox |
| F4  | Medium   | Correctness    | Fixed | Waiting messages are invisible and dropped unseen |
| F5  | Medium   | Architecture   | Fixed | Settings kept in two places diverge during and after configuration |
| F6  | Medium   | Correctness    | Fixed | A pending settings change hides other pages' changes |
| F7  | Medium   | Correctness    | Fixed | A Stop missed while disconnected loses the composer text |
| F8  | Medium   | Concurrency    | Fixed | Stdout delivery coupled to reading deadlocks and drops output |
| F9  | Medium   | Resources      | Fixed | A second stop signal leaves Claude running |
| F10 | Medium   | Correctness    | Fixed | Deleting a sender replaces its messages' label with an ID |
| F11 | Medium   | Correctness    | Fixed | Replies that never stream are invisible |
| F12 | Medium   | Error handling | Fixed | An unexpected exit omits Claude's stated reason |
| F13 | Medium   | Correctness    | Fixed | The prompt size limit counts the JSON envelope |
| F14 | Medium   | Correctness    | Fixed | Reconnect applies updates to a stale transcript |
| F15 | Medium   | Correctness    | Fixed | A settled request reports into a later dialog |
| F16 | Medium   | Architecture   | Fixed | Sidebar membership waits on each session's snapshot |
| F17 | Medium   | Correctness    | Fixed | A retired effort cannot be repaired in the picker |
| F18 | Medium   | Correctness    | Fixed | A workspace path with `..` through a symlink names the wrong directory |
| F19 | Medium   | API design     | Fixed | Duplicate catalog IDs load |
| F20 | Medium   | Naming         | Fixed | Claude's permission mode has two identities |
| F21 | Medium   | Testing        | Fixed | **Send cut off** is untested |
| F22 | Medium   | Testing        | Fixed | The configuration-exit test races its own requests |
| F23 | Medium   | Testing        | Fixed | Repeat creation never checks settings survive |
| F24 | Medium   | Testing        | Fixed | A rejected `session/create` is untested |
| F25 | Medium   | Testing        | Fixed | Claude's working directory is untested |
| F26 | Medium   | Testing        | Fixed | **Following output** is untested |
| F27 | Medium   | Testing        | Fixed | Shift+Enter and IME Enter are untested |
| F28 | Medium   | Documentation  | Fixed | The configure contract misstates events and errors |
| F29 | Medium   | Documentation  | Fixed | The send contract promises steering Claude may not do |
| F30 | Medium   | Documentation  | Fixed | MCP docs conflate envelope errors with tool refusals |
| F31 | Low      | Concurrency    | Fixed | `list_sessions` reads labels outside the owner |
| F32 | Low      | Correctness    | Fixed | A small scroll up still follows output |
| F33 | Low      | Correctness    | Fixed | Markdown lists lose bullets and numbers |
| F34 | Low      | Correctness    | Fixed | A database path containing `?` opens another file |
| F35 | Low      | Readability    | Fixed | `ox-mcp` failure logs omit the session ID |
| F36 | Low      | API design     | Fixed | Client protocol types diverge from the wire |
| F37 | Low      | Documentation  | Fixed | The todo restates Claude's permission rules |

## F1. The resume token ignores `/clear` and a first turn without a result

High · Correctness · Fixed · `internal/claude/agent.go:102`

Ox saved the UUID it launched, once, on the first nonempty result. After `/clear`, Claude moves to a new conversation ID and returns an empty success result, so parking or restarting resumed the conversation the user had cleared. A first turn that never reached a result, such as one waiting on an approval when `oxd` restarted, saved no token at all. The next send then started a new conversation, even though Claude's transcript already held the prompt (**Resuming the conversation**, **Surviving restarts**).

### Evidence

Live Haiku probes: after `/clear` and a restart, the next prompt went back to the old transcript. After a restart during a first-turn approval, the session lost its context, while `claude --resume` of the original ID still recalled it.

### Fix

The agent tracks the `session_id` Claude reports on each line. It reports the ID through `OnResumable` whenever it changes, once model output, a tool result, or a result shows the transcript is saved. `docs/claude-code.md` records the observed timing. `TestLive` covers both cases.

## F2. A refused second `oxd` writes into the live daemon's history

High · Concurrency · Fixed · `cmd/oxd/main.go:59`

`server.New` records `parked` for every session whose history does not end with it. It ran before `oxd` claimed the socket, so a second `oxd` that then exited with "another oxd is serving" had already appended `parked` to the running daemon's sessions. Shutdown also closed the listener, freeing the socket, before the sessions had stopped.

### Evidence

Reproduced: starting a second `oxd` on the same paths changed a running session's snapshot from `[prompt]` to `[prompt, parked]`.

### Fix

`oxd` claims the socket before it opens the database, and closes the server before the listener. `TestDaemonOwnsSocketUntilShutdown` covers both.

## F3. `/model` and `/effort` prompts change settings behind Ox

High · Correctness · Fixed · `internal/claude/prompt.go:12`

A prompt of `/effort high` or `/model sonnet` changed the running Claude process's setting, while `session/get` and the page still showed the old one (**Settings shown are settings used**). Claude reports the change only in reply text.

### Evidence

Live: `/effort high` returned "Set effort level to high (this session only)", and `session/get` still reported `low`.

### Fix

The user decided to refuse these prompts. The Claude harness provides a prompt check, `agent.Harness.CheckPrompt`. It refuses a prompt whose first word is `/model` or `/effort` with "change the model and effort in the session's settings". `session/send` runs the check before starting a parked session. Other slash commands are unaffected. Covered by `TestClaudeRefusesSettingCommandsBeforeStarting`.

## F4. Waiting messages are invisible and dropped unseen

Medium · Correctness · Fixed · `internal/server/session.go:98`

A message queued behind an input Claude had not taken lived only in the session's inbox. It had no ID, update, or snapshot entry, so no page showed it, and Stop dropped it without a trace (**Messages in the transcript**). `session/send` and inbox delivery each started a parked session in their own way. A failed resume through delivery recorded two `exited` notices.

### Evidence

Chrome showed only the untaken prompt while the MCP reply confirmed the message was queued.

### Fix

The session owns every pending input from acceptance until Claude takes it or it is dropped. An accepted message gets an ID and a `prompt_queued` update. Every drop publishes `prompt_cancelled`. `session/get` returns `queued` as an ordered array. One method, `startForInput`, starts a parked session for both input paths and records a single notice on failure. The client shows every pending input. Covered by `TestSendMessage`, `TestExitDropsWaitingMessages`, `TestMessageToUnresumableConversationLeavesOneNotice`, and the e2e test "all waiting inputs show in order with their send-time labels".

## F5. Settings kept in two places diverge during and after configuration

Medium · Architecture · Fixed · `internal/server/methods.go:394`

The session and the agent each held the settings. During a configuration, `session/get` reported the old model after Claude had switched. When the process exited mid-configuration, the session applied a full settings snapshot taken at request time, which overwrote a mode Claude had changed in the meantime. A failed database write after Claude applied a setting became the request's error, and the parked path did the same, although **Database failures** says the write is logged and the page does not show it.

### Evidence

An injected stand-in showed an effort-only change restoring `plan` after Claude had left plan mode. `TestConfigurationTakenButNotStored` showed the write error reaching the client.

### Fix

The agent reports each settings change through `OnConfigured` as it happens, whether Claude acknowledged it or made it on its own. The session adopts, stores (logging failures), and broadcasts every change through `adopt`. `Configure` takes an `agent.SettingsPatch` and reports only the outcome. After an exit, the session applies just the request's fields to its current settings. Covered by `TestConfigureWaitsForAcknowledgements`, `TestConfigurationTakenButNotStored`, and `TestConfigurationOutlivesItsProcess`.

## F6. A pending settings change hides other pages' changes

Medium · Correctness · Fixed · `cmd/ox/web/src/SessionView.tsx:179`

While a settings request was in flight, the page displayed a full copy of the settings taken before the request. If another page changed a setting this request did not touch, this page showed the stale value and marked it pending.

### Evidence

Chrome: with page A's mode change held, changing effort on page B left page A showing the old effort, marked pending.

### Fix

The client keeps only the in-flight patch and overlays it on the latest settings from `oxd`. Only the patch's fields show as pending. Covered by e2e.

## F7. A Stop missed while disconnected loses the composer text

Medium · Correctness · Fixed · `cmd/ox/web/src/ox.tsx:170`

The page returned a dropped prompt's text to its composer only on a live `prompt_cancelled` (**Stop**, **Unsent text**). If another page pressed Stop while this page was disconnected, the text was lost.

### Evidence

Reproduced in Chrome.

### Fix

The page keeps each prompt it sent until it learns the outcome. After reloading the snapshot, it returns the text to the composer, once and ahead of anything typed since, if the prompt is neither queued nor in the history. Covered by the e2e tests "a reconnect reconciles a prompt taken/dropped while the page was away".

## F8. Stdout delivery coupled to reading deadlocks and drops output

Medium · Concurrency · Fixed · `internal/claude/claude.go:314`

The stdout reader handed each line to the session before reading the next. A prompt larger than the 64 KiB pipe buffer left the session blocked writing stdin while Claude was blocked writing stdout, and `SIGTERM` could not stop `oxd`. Separately, `Wait` set a 1 s read deadline even while the reader was blocked on delivery. Output Claude had written before exiting was lost if the session was busy for more than a second.

### Evidence

Stand-in reproductions of both, by the state reviewer.

### Fix

The reader drains the pipe into an in-memory queue, and a separate goroutine delivers it in order. The exit event follows all captured output. The 1 s cutoff now applies only to reading the pipe. Covered by `TestOutputDrainsBeforeSlowDelivery`.

## F9. A second stop signal leaves Claude running

Medium · Resources · Fixed · `internal/claude/claude.go:29`

The second signal ended `oxd`, and with it the kill timers that `oxd` had armed. Claude processes, including any still initializing, kept running, against **Stopping oxd**.

### Evidence

A stand-in that ignores `SIGTERM` was still alive after the second signal.

### Fix

Each Claude process is registered when it is spawned. On a second signal, `oxd` calls `claude.KillAll` and then exits. `TestDaemonOwnsSocketUntilShutdown` covers both running and initializing processes.

## F10. Deleting a sender replaces its messages' label with an ID

Medium · Correctness · Fixed · `internal/server/mcp.go:271`

The page took a message's sender label from the current sidebar. After the sender was deleted, every page, including after a reload, showed the sender's ID instead.

### Fix

Message prompt updates and `queued` entries carry `from_label`, the sender's label when the message was sent. It is recorded in history. The client heads each message with it. Covered by `TestSendMessage` and e2e.

## F11. Replies that never stream are invisible

Medium · Correctness · Fixed · `internal/claude/agent.go:175`

Local commands such as `/cost` return their text only in a successful `result`, with no `text_delta`. Clients hide successful result text as a duplicate of the streamed reply, so this output never showed.

### Fix

When a successful result has text and the turn streamed none, the agent publishes the text as a `text_delta` before the result. Verified live with `/cost` in `TestLive`.

## F12. An unexpected exit omits Claude's stated reason

Medium · Error handling · Fixed · `internal/claude/agent.go:218`

The transcript recorded `Claude exited: exit status 1`, while Claude's stderr explanation reached only the log (**Unexpected exits**).

### Fix

The exit event carries the tail of Claude's stderr, and the transcript entry includes it. Covered by `TestExitDropsWaitingMessages`.

## F13. The prompt size limit counts the JSON envelope

Medium · Correctness · Fixed · `internal/server/methods.go:329`

The relay capped the whole RPC body at 1 MiB, so JSON escaping and the envelope cut the prompt allowance below 1 MiB, while `oxd` enforced nothing (**Prompt size limit**).

### Fix

`oxd` refuses a prompt whose decoded text exceeds 1 MiB with `-32602`. The relay allows bodies up to 8 MiB. Covered by `TestSendPromptSizeCountsDecodedBytes`, `TestRPCRelaysOneMiBOfEscapedText`, and `TestRPCRefusesOversizedBody`.

## F14. Reconnect applies updates to a stale transcript

Medium · Correctness · Fixed · `cmd/ox/web/src/ox.tsx:361`

After a reconnect, the page applied live updates that arrived before the `session/get` response to the transcript it had before the disconnect. The protocol says to ignore updates until the snapshot arrives, so a turn could show its newest prompt without the one before it.

### Fix

Each session is either reloading, which keeps showing its previous snapshot, or live. Updates apply only to a live session. Covered by e2e.

## F15. A settled request reports into a later dialog

Medium · Correctness · Fixed · `cmd/ox/web/src/Sidebar.tsx:7`

One dialog instance served every invocation. A Delete request for session A that settled after the user had opened Delete for session B reported its error into B's dialog, or closed it. Add and Remove workspace had the same flaw.

### Fix

Each dialog invocation owns its target, request, and result. Covered by the e2e tests "a cancelled deletion's reply cannot close the next confirmation" and "a cancelled add or remove cannot settle the next dialog".

## F16. Sidebar membership waits on each session's snapshot

Medium · Architecture · Fixed · `cmd/ox/web/src/ox.tsx:334`

A session appeared in the sidebar only once its `session/get` resolved. A session slow to load left its workspace looking empty, and removable. Sessions created in the same millisecond sorted by when their snapshots arrived.

### Fix

Membership and order come from `workspace/list` and the session created and destroyed events. Each session's snapshot load state is kept separately. Covered by e2e.

## F17. A retired effort cannot be repaired in the picker

Medium · Correctness · Fixed · `cmd/ox/web/src/SettingsFields.tsx:37`

If the session's effort was no longer offered and its model offered only one effort, the slider was disabled and reselecting the model did nothing. The user had no way to reach a valid pair.

### Fix

Choosing a model resolves the model and effort pair before checking whether anything changed. Covered by e2e.

## F18. A workspace path with `..` through a symlink names the wrong directory

Medium · Correctness · Fixed · `internal/server/methods.go:114`

`filepath.Clean` removed `..` before the filesystem resolved symlinks. If `/a/link` points to `/b/inner`, the path `/a/link/..` names `/b`, but Ox stored `/a`, so Claude would work in `/a` (**Working directory**).

### Fix

The path is stored as given and resolved by the filesystem. Covered by `TestSameDirectoryIsOneWorkspace`.

## F19. Duplicate catalog IDs load

Medium · API design · Fixed · `internal/config/config.go:76`

A configuration could list the same model, effort, or permission-mode ID twice. The picker then offered choices that `oxd` could not tell apart, and validation used only the first.

### Fix

Configuration loading rejects duplicate IDs within each list. Covered by `TestChoiceIDs`.

## F20. Claude's permission mode has two identities

Medium · Naming · Fixed · `cmd/oxd/main.go:123`

A catalog could offer `default`. Claude reports that mode as `default`, the agent maps it to `manual`, and the session ended up with a mode the catalog did not offer.

### Fix

`oxd` refuses `default` for Claude and directs the configuration to use `manual`, which keeps the agent's mapping as the only translation. Covered by `TestConfigRefusesClaudesDefaultPermissionModeAlias`.

## F21. **Send cut off** is untested

Medium · Testing · Fixed · `cmd/ox/web/e2e/session.spec.ts:203`

When sends were left unsettled on disconnect, every e2e test still passed.

### Fix

Added the e2e test "a send cut off after acceptance shows an error without resending".

## F22. The configuration-exit test races its own requests

Medium · Testing · Fixed · `internal/server/methods_test.go`

The test depended on a 10 ms sleep between two requests that `oxd` handles concurrently. It could fail on a valid ordering, or pass without reaching the exit branch.

### Fix

`TestConfigurationOutlivesItsProcess` holds the acknowledgement, triggers the exit, and checks the result through `session/get` and the next start.

## F23. Repeat creation never checks settings survive

Medium · Testing · Fixed · `internal/server/methods_test.go`

When the repeat path adopted the retried request's settings, `TestRepeatSessionCreation` still passed.

### Fix

The test now changes a setting, retries the creation, and asserts the change survived.

## F24. A rejected `session/create` is untested

Medium · Testing · Fixed · `cmd/ox/web/e2e/session.spec.ts:125`

When the link back to the draft was hidden after a failed creation, every e2e test still passed.

### Fix

Added the e2e test "a rejected creation keeps the draft and its settings after switching views".

## F25. Claude's working directory is untested

Medium · Testing · Fixed · `internal/claude/claude_test.go`

Removing `cmd.Dir = workspace` left the suite green.

### Fix

The stand-in in `TestClaudeRunsAsAnOxSession` reports its working directory on new and resumed launches.

## F26. **Following output** is untested

Medium · Testing · Fixed · `cmd/ox/web/e2e/session.spec.ts:244`

Disabling the scroll-follow code left the e2e suite green.

### Fix

Added the e2e test "overflowing output follows only while the reader is at the bottom". It first caught a regression in the F32 fix, which F32 describes.

## F27. Shift+Enter and IME Enter are untested

Medium · Testing · Fixed · `cmd/ox/web/e2e/session.spec.ts:224`

Removing both guards in the Enter handler left the e2e suite green.

### Fix

Added an e2e test for both.

## F28. The configure contract misstates events and errors

Medium · Documentation · Fixed · `docs/protocol.md:257`

The configure section did not say when `session/configured` is sent or which errors the request returns.

### Fix

The section now describes the F5 contract: one `session/configured` per change, and failed database writes logged, not returned.

## F29. The send contract promises steering Claude may not do

Medium · Documentation · Fixed · `docs/protocol.md:202`

The contract said a prompt sent during a turn joins it. Claude may instead take the queued prompt after the turn's `result`, as a new turn.

### Fix

The contract now states both outcomes.

## F30. MCP docs conflate envelope errors with tool refusals

Medium · Documentation · Fixed · `docs/protocol.md:467`

The docs did not separate `-32602` errors for a bad `tools/call` request or an unknown tool from tool refusals, which return `isError` results. They also did not say that `initialize` requires `protocolVersion`.

### Fix

The section now distinguishes the two kinds of error and states the `initialize` requirement.

## F31. `list_sessions` reads labels outside the owner

Low · Concurrency · Fixed · `internal/server/mcp.go:210`

The label was looked up after each session's owner had answered. Destroying a session and recreating one with the same ID in between could pair one session's settings with the other's label.

### Fix

Each session's owner reads its label inside the same operation as the rest of its entry. Covered by `TestListSessionsCapturesLabelWithOwnerRead`.

## F32. A small scroll up still follows output

Low · Correctness · Fixed · `cmd/ox/web/src/style.css:155`

A 32 px threshold treated a 20 px scroll up as still pinned to the bottom. The first fix, a 1 px threshold, failed the new F26 test. When the composer shrank, Chrome clamped `scrollTop`, and the handler read that as the user scrolling.

### Fix

The transcript uses a bottom-anchored `column-reverse` layout and the browser's own scroll anchoring, with no JavaScript tracking whether it is pinned. Covered by the F26 test.

## F33. Markdown lists lose bullets and numbers

Low · Correctness · Fixed · `cmd/ox/web/src/style.css:130`

A global list reset removed list markers from replies.

### Fix

The reset now applies only to navigation. Covered by e2e.

## F34. A database path containing `?` opens another file

Low · Correctness · Fixed · `internal/store/store.go:70`

The DSN was built by appending to the raw path, so a `?` in the path started the query string.

### Fix

The store opens a SQLite file URI with an escaped path. Covered by `TestOpenUsesLiteralPath`.

## F35. `ox-mcp` failure logs omit the session ID

Low · Readability · Fixed · `cmd/ox-mcp/main.go:16`

### Fix

The logger prefix includes `OX_SESSION_ID`. Covered by `TestForwarderWithoutOxd`.

## F36. Client protocol types diverge from the wire

Low · API design · Fixed · `cmd/ox/web/src/protocol.ts`

### Fix

The types now match the wire: optional `message_id` on all updates, an optional creation `effort`, a non-empty configure patch, and the F4 and F10 fields.

## F37. The todo restates Claude's permission rules

Low · Documentation · Fixed · `docs/todo.md:7`

### Fix

The todo item keeps the open decision and links to `docs/claude-code.md` for Claude's permission rules.

## Checks run

- `make test`: lint, build, and `go test -race ./...` pass.
- `make e2e`: 34 passed, 3 live-only tests skipped. The first run failed the new following-output test, which led to the second F32 fix.
- `OX_LIVE=1 go test -race -count=1 -run TestLive -v ./internal/server` passed with Haiku in the Go fix session, including the `/clear`, first-turn restart, and `/cost` cases.
- A headless Chrome run through an isolated `make dev` stack passed the pending-message, settings, and reconnect paths.
- Base revision: the new Go tests that compile at `733ac2a` (`TestChoiceIDs`, `TestOpenUsesLiteralPath`, `TestRPCRelaysOneMiBOfEscapedText`) fail there. The others use interfaces this change adds. The current e2e specs run against the base client: every test of changed behavior fails. Four pass, as expected: the F21, F24, and F27 tests cover behavior that already worked but was untested, and the taken-prompt reconnect case is a renamed existing test.

## Verdict

Ready to commit. All 37 findings are fixed. F3 follows the user's decision to refuse `/model` and `/effort` prompts. The largest structural changes are the session-owned pending-input queue (F4, F10), the single settings path (F5), conversation tracking by Claude's reported ID (F1), and an output queue separate from the stdout reader (F8).
