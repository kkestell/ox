# One owner for a session's label

## Goal

Audit [2026-11-20-003 F2](../audits/2026-11-20-003-since-first-audit.md#f2-a-sessions-label-is-derived-on-both-sides-of-the-protocol): a session's label is derived twice. `oxd` derives it from the stored first user prompt, with a store query per session on every `workspace/list`, `list_sessions`, `send_message`, and `create_session`. The page derives its own from the transcript it has loaded. The two disagree in three reproduced cases (deslop F12): a reconnecting page keeps its old snapshot's label over the listed one, Go and JavaScript trim U+0085 differently, and a prompt whose write failed labels the session on the page but not in `oxd`.

When done, the session owns its label. `oxd` holds it in memory, sets it once when the session takes its first user prompt, lists it in `workspace/list` and `session/created`, and announces the change with a new `session/labeled` event. The page shows only what `oxd` sends.

## Related code

- `internal/server/mcp.go:641-660` — `Server.label`: child title, else `store.FirstPrompt` parsed and cut to its first line, else `id[:8]`. Called from `listSessions` (`:261`), `sendMessage` (`:305`), `createSession` (`:390`), and `listWorkspaces`.
- `internal/server/mcp.go:231-280` — `listSessions` posts to each goroutine, and its `readErrors` exist only for label errors.
- `internal/server/methods.go:165-197` — `listWorkspaces` runs a store query per session while holding `s.mu`.
- `internal/server/methods.go:374-387` — `addSession` broadcasts `session/created` without a label.
- `internal/server/session.go:20-41`, `:52-67` — the `session` struct (its `archived` flag is the existing pattern for a field guarded by `Server.mu`) and `newSession`.
- `internal/server/session.go` `update` — runs on the session goroutine for every update the harness publishes, including the `prompt` update when the harness takes an input.
- `internal/server/server.go:60-68` — `New` loads every stored session.
- `internal/store/store.go:275-286` — `FirstPrompt` returns the raw event data.
- `cmd/ox/web/src/ox.tsx:48-50`, `:72-81` — `Session.label` and `sessionLabel`, the page's derivation.
- `cmd/ox/web/src/ox.tsx:392-395`, `:425-437` — reload copies the listed label; `session/created` builds an ID label.
- `cmd/ox/web/src/Sidebar.tsx:86`, `:190`; `cmd/ox/web/src/SessionView.tsx:38`, `:130-133` — the label's readers.
- `cmd/ox/web/src/protocol.ts:57-68` — the `Notification` union.

## Decisions

- **Requirements:**
  - **Session labels.** Unchanged in text. A child's label is its title. Any other session's label is `id[:8]` until its goroutine sees the first `prompt` update without `from`, then that prompt's first line, for good. This is the same moment the page's derivation used (a `prompt` entry in the transcript), so the visible timing does not change.
  - **Every page in sync.** `session/labeled` goes to every connection, and the page applies it whatever the session's load state, as it does `session/archived`. A page whose snapshot is held or reloading still shows the new label.
  - **Lost connection.** On reconnect the page takes each label from `workspace/list` and has no other source, so the reconnect divergence cannot occur. The label is set and announced under `s.mu`, and `workspace/list` reads and replies under `s.mu`, so a page sees either the new label in the listing or the event after it.
  - **Database failures.** The label follows the prompt the session took, whether or not its write succeeded, on every page and in every listing, until `oxd` restarts. After a restart it comes from the stored history, as every other unsaved fact does. `workspace/list` no longer touches the store, so it no longer fails while the database cannot be read.
  - **Messages in the transcript**, **Claude can message other sessions**, **Claude can start child sessions.** `from_label`, `list_sessions`, and the message wrapper read the same in-memory label. The page's fallback for a recorded message without `from_label` reads the sender's `label`.
- **Construction:**
  - One source per label: a child's `child.Title`, fixed at creation; a top-level session's `firstLine`, guarded by `Server.mu`, empty until set. `labelLocked` derives the label from them, so no reader branches on child or emptiness.
  - `firstLine` is written only by the session's goroutine, once: the write checks that it is still empty, under `s.mu`. Prompts are nonblank, so a set `firstLine` is never empty.
  - The first-line rule (`strings.TrimSpace`, then cut at the first `\n`) lives in one Go function, `firstLine(text)`, used at load and on the live update. The page has no copy.
- **Ownership:**
  - `session.firstLine` (new field). Owner: the session's goroutine writes it, guarded by `Server.mu` because `workspace/list`, `list_sessions`, `send_message`, and `create_session` read it under that lock alongside membership. User-visible need: the label in the sidebar, the header, the listing tools, and message headers. Race: a listing concurrent with the first prompt sees the old or new label, and an old one is followed by `session/labeled`; the lock orders both. Cleanup: none; it lives as long as the session and is rebuilt from the store at load.
  - `session/labeled` (new event): no state of its own.
- **Protocol:** `session/created` gains `label`: the child's title, or `id[:8]`. New event `session/labeled` with `session_id` and `label`, broadcast once per top-level session, when it takes the first prompt the user sent it. A notification fits better than a field on `session/update`: the label is a property of the session, like its archived flag and settings, which also have their own events, and it is not part of the history.
- **Store:** `FirstPrompt` returns the prompt's text (`json_extract(data, '$.text')`) as a string, `""` when there is none, so the server no longer decodes an `agent.Update` for it. It is called only from `New`.

## Check your assumptions

1. **A label from a prompt that could not be saved.** Kept (settled without the user, as instructed).
   - **Plan:** When Ox cannot write its database, a session is still labeled with the first prompt it took, on every page and in Claude's session list, until `oxd` restarts. This matches **Database failures**, where unsaved state lasts until a restart.
   - **Alternative:** Label it with its short identifier until the prompt is saved. Every page would then show the identifier while the transcript shows the prompt, which reads as a bug.

## Test plan

- **Label set once, from the first user prompt, through a restart and a failed write** — server, new `TestSessionLabel` in `internal/server/methods_test.go`. No existing test covers `session/created`'s label, a store failure during labeling, or a restart's label. Fake harness, two top-level sessions:
  - `session/created` for each carries `label` equal to `id[:8]`.
  - Session A is sent `"\u0085\nActual title\nmore"`; `session/labeled` arrives with `Actual title`. Once A is idle, a second prompt `"second"`; after it is idle, `workspace/list` still lists `Actual title` (fails if the once-only check is removed).
  - Close the server and start a new `Server` over the same store; `workspace/list` lists A as `Actual title` (guards the load path).
  - `st.Close()`, then send B `"Unsaved title"`: `session/labeled` arrives with `Unsaved title`, and `workspace/list` succeeds and lists it (fails before the change: the listing errors on the closed store).
- **A message does not label its recipient** — server, existing `TestSendMessage` already asserts B is listed as `approve T {}`, its first user prompt after a message. It guards the `from` check in the new code; no change.
- **A child's label is its title in `session/created`** — server, existing `TestCreateSession` block in `internal/server/mcp_test.go` (`createdChild`): also decode `label` from `session/created` and require `Worker`.
- **`TestListSessionsCapturesLabelWithOwnerRead`** — keeps passing; the label is now read under `s.mu` with membership, so the case it guarded (a store query after the session's deletion) no longer exists. Rename it `TestListSessionsReadsEachSessionFromItsOwner` and drop the label wording from its failure message.
- **The page shows the label before the transcript loads** — e2e, existing `"session list membership and ordering don't wait for transcripts"` in `cmd/ox/web/e2e/session.spec.ts`: while the first session's `session/get` is held, the sidebar shows `["older session", "younger session"]`, replacing the `id.slice(0, 8)` expectation. Fails before the change, when the page waited for the snapshot.

## Implementation plan

1. `internal/store/store.go`: change `FirstPrompt` to return the first user prompt's text as a string, `""` when there is none.
2. `internal/server/session.go`:
   - Add `firstLine string` beside `archived`, under the `Guarded by Server.mu` comment: the first line of the first prompt the user sent a top-level session, empty before it has one.
   - Add `func firstLine(text string) string` (trim, cut at `\n`).
   - Add `labelLocked() string`: `child.Title`, else `firstLine`, else `id[:8]`. The caller holds `s.mu`.
   - `newSession` takes the stored first line and sets the field.
   - In `update`, after recording and broadcasting: for a top-level session and a `prompt` update with no `From`, lock `s.mu`; if `firstLine` is empty, set it from the update's text and broadcast `session/labeled` with `session_id` and `labelLocked()`.
3. `internal/server/server.go`: in `New`, for each top-level record, call `st.FirstPrompt` and pass `firstLine(text)` to `newSession`; a store error fails `New`. `addSession` passes `""`.
4. `internal/server/methods.go`: `listWorkspaces` uses `sess.labelLocked()` and loses its error path. `addSession` adds `Label string \`json:"label"\`` to `session/created`, from `labelLocked()`.
5. `internal/server/mcp.go`:
   - Delete `Server.label`.
   - `listSessions`: read each label with `labelLocked()` in the loop that already holds `s.mu`, and carry it into the entry the goroutine fills; delete `readErrors`.
   - `sendMessage`: read `caller.labelLocked()` in the locked block that looks up the recipient.
   - `createSession`: read `caller.labelLocked()` inside the block that already holds `s.mu`, and drop the error return.
6. `internal/server/methods_test.go`, `internal/server/mcp_test.go`: the tests above.
7. `cmd/ox/web/src/protocol.ts`: add `label: string` to `session/created`'s params, and `{ method: "session/labeled"; params: { session_id: string; label: string } }` to `Notification`.
8. `cmd/ox/web/src/ox.tsx`:
   - `Session.label`'s comment becomes "the session's label, as oxd lists and announces it".
   - Delete `sessionLabel`.
   - `session/created` takes `params.label`.
   - Handle `session/labeled` by setting `session.label` on the matching session, whatever its load state.
   - Reload keeps copying the listed label.
9. `cmd/ox/web/src/Sidebar.tsx`, `cmd/ox/web/src/SessionView.tsx`: read `session.label` where they called `sessionLabel`, including `senderLabel`'s fallback.
10. `cmd/ox/web/e2e/session.spec.ts`: the e2e change above.
11. `docs/protocol.md`: the updates below.

## Requirement changes

None.

## Documentation updates

- `docs/protocol.md`: `session/created` lists `label` (a child's title, or the first 8 characters of its ID) in the events table and its example; add a `session/labeled` row: `session_id`, `label` — a top-level session took the first prompt the user sent it, and `label` is its first line; sent once per session. `workspace/list`'s label sentence stays as it is.
