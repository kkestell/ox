# Durable Ur conversations

## Goal

An Ur conversation lives only in its agent's memory, so parking is off for Ur
and every `oxd` restart or unarchive starts the next prompt without the
earlier context. Under `make dev`, which restarts `oxd` on every rebuild, that
wipes every Ur conversation many times a day. When this change is done, Ur
appends each message to a conversation file of its own, announces the file's
ID as the session's resume token, reads the file back when a session starts
with that token, and reports itself parkable when idle. An Ur session then
survives parking, `oxd` restarts, and archive and unarchive the way a Claude
session does.

## Related code

- `internal/ur/ur.go` — the agent. `begin` appends the user message to
  `history`; `finish` appends the reply when it is non-empty, for completed,
  failed, and interrupted turns alike; `Stop` discards the running turn's
  reply; `Parkable` returns false; `Harness(endpoint, providers)` builds the
  harness.
- `internal/ur/stream.go` — `request` marshals `history` as the request's
  `messages`. The stream goroutine reads only fields set at start.
- `internal/ur/ur_test.go` — `start` builds an agent against an `httptest`
  server; `TestInterrupt` and `TestStopDuringTurn` pin the turn-ending rules.
- `internal/paths/paths.go` — the single resolver of Ox's XDG locations.
- `cmd/oxd/main.go` — builds `ur.Harness`.
- `internal/server/session.go` — `start` passes `sess.resumeToken` as
  `Options.Resume`; `markResumable` stores the token `OnResumable` gives,
  including an empty one; `armPark` parks an agent that reports `Parkable`.
  `destroy` deletes only Ox's database rows.
- `internal/claude/agent.go` — prior art: `OnResumable` fires once Claude's
  transcript holds the prompt, and a resume Claude reports missing calls
  `OnResumable("")` and ends with `lostConversation`.
- `internal/server/live_test.go` — `TestLiveUr` checks that a second turn
  remembers the first.

## Decisions

- **The conversation file.** `paths.Ur()` is the directory
  `$XDG_STATE_HOME/ox/ur` (or `~/.local/state/ox/ur`). A new conversation's ID
  is `rand.Text()`; its file is `<dir>/<id>.jsonl`. Each line is one `entry`:
  the OpenAI chat `message` (`role`, `content`) embedded, plus `model`, set on
  assistant entries to the model that wrote them. The request sends only the
  embedded `message` of each entry, so `model` never reaches OpenRouter and
  the tool slice adds `tool_calls` and the `tool` role to `message` without a
  new format.
- **Memory is the file.** One method, `save(entry) error`, is the only way an
  entry enters `history`: it creates the directory, opens the file with
  `O_APPEND|O_CREATE|O_WRONLY` (mode `0600`, directory `0700`), writes the
  marshalled entry and its newline in one `Write`, closes it, and appends the
  entry to `history` only on success. `history` is therefore always exactly
  the file's entries, and no flag tracks what has been written.
- **When the token is announced.** `save` calls `OnResumable(id)` when it
  writes the first entry, which it knows because `history` was empty. That is
  the user message, written when its turn begins, matching Claude, whose
  transcript is resumable once it holds the prompt. A resumed conversation
  already has entries, so it is never announced again.
- **What a turn writes.** Unchanged rules, now on disk: the user message when
  its turn begins, before the request is posted; the reply, as far as it
  streamed, when the turn ends, if non-empty, whether the turn completed,
  failed, or was interrupted. `Stop` now keeps a running turn's partial reply
  the same way, through the same helper as `finish`, so an `oxd` shutdown
  mid-turn resumes with what the user saw. A failed turn with no text leaves
  two user messages in a row, which the chat format accepts.
- **Save failures.** If saving the user message fails, `begin` ends the turn
  at once with the error result `Ur could not save the prompt: <err>` and
  posts nothing, then begins the next queued input as `finish` always does.
  If saving the reply fails in `finish`, the turn's result becomes an error
  whose text is `Ur could not save the reply: <err>`, after the turn's own
  error text on its own line when the turn had already failed. In `Stop`, a
  failed save goes to `OnError`. Either way the model never sees a message
  the file lacks.
- **Resuming.** `Start` with `Options.Resume` reads `<dir>/<token>.jsonl`
  with a `json.Decoder` until EOF, so a line of any length loads. A missing
  file, a read error, or a malformed or truncated line is a lost conversation:
  `Start` sends `resume conversation: <err>` to `OnError`, calls
  `OnResumable("")`, and returns the error `Ur could not resume the earlier
  conversation; the next prompt starts a new one without its context`. The
  server's `startForInput` already returns a start error to the sender, so the
  send is refused with that text, the page keeps the prompt in the composer,
  and the cleared token makes the next send start a new conversation. A
  message from another session is refused the same way, and the sending
  Claude gets the reason, as **Message delivery** describes for a harness
  that cannot be launched. The key check stays first, so a missing key never
  clears the token.
- **Parkable.** `Parkable` returns `a.tracker.Idle()`: no turn runs and no
  input waits. Ur starts no background tasks, so idle and background are the
  same for it.
- **File lifetime.** No path deletes a conversation file. Deleting a session
  leaves it, as it leaves Claude's transcript; a lost conversation's
  unreadable file stays for inspection. Deleting it would need a harness
  method the server calls from `destroy`, which no requirement asks for yet.
- **Requirements.** **Parking**: an idle Ur session now parks; a running
  turn or a queued input keeps it unparkable, and Ur has no approvals,
  setting changes in flight, background tasks, or schedules. **Resuming the
  conversation** and **Surviving restarts**: the token goes through the
  existing store column, so parking, restarts, and unarchive all start Ur
  with it. **Database failures**: if storing the token fails, the session
  keeps it in memory until `oxd` restarts, as for Claude. **Setting changes
  keep the rest**: Configure still applies to the next request, and the
  conversation is untouched; the next reply's entry records the new model.
  **Stop**: an interrupted turn's partial reply stays in the conversation, as
  today. **Lost conversation** stays Claude's; Ur's own case is in **Ur
  sessions**. **Ox's own files**, **File locations**, and **Deleting a
  session** change as below.
- **Construction.** `history` equals the file's entries because `save` is
  the only writer of both and appends to memory only after the write
  succeeds. The token is announced exactly once per conversation because the
  announcement is derived from `history` being empty.
- **Ownership.** New state: the conversation `id` and `dir`, set at start and
  owned by the agent. The file is owned by the session goroutine, which makes
  every call that writes it; the stream goroutine never touches it. The
  user-visible behavior that needs it is resuming an Ur conversation after
  parking, restart, and unarchive. Races: none new; writes happen only on
  the session goroutine, and a token reaches the server through the same
  `OnResumable` call Claude uses. Cleanup: none; files outlive their
  sessions, as Claude's transcripts do.
- **Settling.** No new waits. Reading and appending a local file return on
  their own.
- **Trust boundaries.** The file path joins `dir` with a token that only Ur
  generates and only Ox's database stores; no client request sets it. Files
  are created `0600` in a `0700` directory under Ox's own state directory.

## Check your assumptions

1. **Deleting an Ur session leaves its saved conversation on disk.**
   - **Plan:** Deleting an Ur session removes it from Ox but leaves the
     conversation file in Ox's state directory, as deleting a Claude session
     leaves Claude's transcript. Old Ur conversations accumulate there until
     removed by hand.
   - **Alternative:** Deleting the session also deletes its conversation
     file. Nothing accumulates, at the cost of a new deletion step that every
     harness would have to answer.
   - Requirement **Deleting a session**, old: "…stops Claude, deletes Ox's
     history of it, leaves Claude's transcript and the workspace on disk, and
     removes it from every page." New: "…stops Claude, deletes Ox's history of
     it, leaves Claude's transcript, Ur's saved conversation, and the
     workspace on disk, and removes it from every page."
2. **A lost Ur conversation refuses the send instead of noting it in the
   transcript.**
   - **Plan:** If an Ur session's saved conversation is gone or unreadable,
     the send fails with an error under the composer saying the earlier
     conversation could not be resumed and the next prompt starts a new one;
     the prompt stays in the composer, and sending again starts fresh.
   - **Alternative:** Behave exactly as Claude does: accept the send, then
     drop it with a notice in the transcript and return its text to the
     composer. The notice would stay in the transcript, at the cost of an
     extra starting state in Ur for a case that needs the file deleted or
     damaged by hand.
3. **Ur sessions now park and keep their context.**
   - Requirement **Ur sessions**, old: "Ur chats through OpenRouter with the
     user's `OPENROUTER_API_KEY`, streaming text replies with no tools; a send
     to an Ur session without the key is refused with an error. An Ur session
     never parks while `oxd` runs, and after an `oxd` restart or unarchiving
     it keeps its transcript, but its next prompt starts a new conversation
     without the earlier context." New: see Requirement changes.
4. **Ox writes Ur's conversations beside its database.**
   - Requirement **Ox's own files**, old: "Ox's own bookkeeping writes only
     its database and socket." New: "Ox's own bookkeeping writes only its
     database, its socket, and Ur's saved conversations."
   - Requirement **File locations**, old: "Ox reads its configuration and
     keeps its database and socket in the user's XDG directories, and the
     user can choose other paths." New: "Ox reads its configuration and keeps
     its database, socket, and Ur's saved conversations in the user's XDG
     directories, and the user can choose other paths."

## Test plan

- Resume after a restart, Ur harness, new `TestResume` in
  `internal/ur/ur_test.go` (no existing test starts twice): turn one "hi" →
  "one" on model A; `OnResumable` has fired once, with a token, by the time
  the first request arrives; Configure to model B; turn two "again" → "two";
  `Stop`; a second `Start` with the same dir and the token; turn three's
  request carries `hi, one, again, two, third` with roles
  `user, assistant, user, assistant, user` and no `model` key in any message;
  the file's assistant lines record models A and B; `OnResumable` fired no
  more.
- Lost conversation, Ur harness, new `TestResumeLost`: a token with no file,
  and a file whose last line is truncated, each make `Start` return the
  lost-conversation error and call `OnResumable("")`.
- Save failure, Ur harness, new `TestSaveFailure`: with `dir` a regular file,
  a send ends with an error result naming the prompt and posts no request.
  With a working dir, making the conversation file read-only while the
  request is in flight ends the turn with an error result naming the reply,
  and the next request omits the reply.
- Partial reply on `Stop`, Ur harness, `TestStopDuringTurn`: the handler
  streams "partial" before blocking; after `Stop`, a `Start` with the token
  sends a request whose messages end with `hi`, `partial`, then the new
  prompt.
- `Parkable`, Ur harness, `TestInputDuringTurnRunsNext`: false while a turn
  runs and while an input waits, true once idle.
- Interrupted reply on disk is already pinned in memory by `TestInterrupt`;
  `TestResume`'s memory-equals-file assertion covers that the same entries
  reach the file.
- `start` in `ur_test.go` takes `t.TempDir()` as the dir and records
  `OnResumable` tokens; `TestStartWithoutKey` uses the new `Harness`
  signature.
- Live, `TestLiveUr`: run the server with a short `parkAfter`, wait for the
  `parked` update after the first turn, then check that the second turn still
  recalls the number. This drives parking and resume through the server with
  the real model.

## Implementation plan

1. `internal/paths/paths.go`: add `Ur()`, the directory of Ur's
   conversation files beneath the state directory.
2. `internal/ur/ur.go`: `Harness(endpoint, dir string, providers …)`; add
   `entry`, `id`, `dir`, `onResumable`, `onError`; make `history` a
   `[]entry`; add `save` and `load`; in `Start`, load a resumed conversation
   or return the lost-conversation error after `OnResumable("")`, else
   generate the ID; `begin` saves the user message before posting, ending the
   turn on failure; a shared helper used by `finish` and `Stop` saves a
   non-empty reply with the current model; `Parkable` returns
   `a.tracker.Idle()`. Update the `Agent` doc comment.
3. `internal/ur/stream.go`: `request` builds `messages` from the entries'
   embedded `message`.
4. `cmd/oxd/main.go`: resolve `paths.Ur()` and pass it to `ur.Harness`.
5. `internal/server/live_test.go`: pass `t.TempDir()` to `ur.Harness`, and
   park between the turns.
6. Tests in `internal/ur/ur_test.go` as above.
7. Documentation and requirements as below.

## Requirement changes

- **Changed:** **Ur sessions.** Old: "Ur chats through OpenRouter with the
  user's `OPENROUTER_API_KEY`, streaming text replies with no tools; a send to
  an Ur session without the key is refused with an error. An Ur session never
  parks while `oxd` runs, and after an `oxd` restart or unarchiving it keeps
  its transcript, but its next prompt starts a new conversation without the
  earlier context." New: "**Ur sessions.** Ur chats through OpenRouter with
  the user's `OPENROUTER_API_KEY`, streaming text replies with no tools; a
  send to an Ur session without the key is refused with an error. Ur saves
  each prompt, and each reply as far as it streamed, so an Ur session parks
  and resumes its conversation like a Claude session. If its saved
  conversation can no longer be read, the send is refused with an error
  saying so, and the next send starts a new conversation without the earlier
  context."
- **Changed:** **Ox's own files.** Old: "Ox's own bookkeeping writes only its
  database and socket." New: "**Ox's own files.** Ox's own bookkeeping writes
  only its database, its socket, and Ur's saved conversations."
- **Changed:** **File locations.** Old: "Ox reads its configuration and keeps
  its database and socket in the user's XDG directories, and the user can
  choose other paths." New: "**File locations.** Ox reads its configuration
  and keeps its database, socket, and Ur's saved conversations in the user's
  XDG directories, and the user can choose other paths."
- **Changed:** **Deleting a session.** Old: "A session, archived or not, can
  be deleted only when it has no child sessions. Deleting it asks first, then
  stops Claude, deletes Ox's history of it, leaves Claude's transcript and the
  workspace on disk, and removes it from every page." New: "**Deleting a
  session.** A session, archived or not, can be deleted only when it has no
  child sessions. Deleting it asks first, then stops Claude, deletes Ox's
  history of it, leaves Claude's transcript, Ur's saved conversation, and the
  workspace on disk, and removes it from every page."

## Documentation updates

- `docs/architecture.md`: the `ur` row says Ur appends its conversation to a
  JSONL file named by its resume token and reports itself parkable when
  idle; the `paths` row adds Ur's conversation directory.
