# Ur saves replies as they stream

## Goal

[Audit F1](../audits/2026-11-22-001-conversation-launch-and-layout.md#f1-ur-saves-the-transcript-and-model-context-at-different-times):
`oxd` records each `text_delta` in the session's history before any page sees
it, but Ur writes the reply to its conversation file only when the turn ends
or the agent is stopped. When `oxd` is killed mid-turn, the restored transcript
shows the reply and the next Ur request omits it, with no notice. The audit
reproduced this with a local HTTP server, a valid conversation file, and a
killed daemon.

When this change is done, Ur appends each delta's text to its conversation file
before it publishes the `text_delta`, so any text a page has shown, or the
history holds, is already in the model's context. The second checkpoint that
`finish` and `Stop` keep today goes away.

## Related code

- `internal/ur/ur.go`:
  - `Handle` takes a `delta`, appends it to `turn.reply`, and publishes it.
  - `finish` and `Stop` call `keepReply`, which saves the whole reply as one
    assistant entry.
  - `save` is the only writer of the file and of `history`.
  - `request` builds the request's messages one per entry.
  - `load` reads a file into `history` an entry at a time.
- `internal/ur/stream.go`: `read` calls `onText` only for non-empty delta
  text, so every delta has text.
- `internal/server/session.go` `update`: records the update in history before
  broadcasting it. This change leaves it as it is.
- `internal/server/history.go`: stores each `text_delta` and joins a message's
  fragments for `session/get`. This change leaves it as it is.
- `internal/ur/ur_test.go`: `TestStopDuringTurn`, `TestResume`,
  `TestSaveFailure`, `TestInterrupt`, and `TestTwoTurns` pin the rules this
  change moves.

## Decisions

- **Where the conversation lives.** Ur's file stays the only record of the
  model's input, and Ur stays its only writer. It holds what the history
  cannot: the input `Content` with a message's sender wrapper, and the model
  that wrote each reply. It also outlives the session, as **Deleting a
  session** requires, and its loss is what **Ur sessions** reports when it
  refuses a send. The fix moves Ur's write to the event where the defect
  occurs: Ur saves each delta as it handles it, before the server sees it.
- **File format.** The entry type is unchanged. Each delta is saved as its own
  assistant entry (`role` `assistant`, `content` the delta's text, `model` the
  current model). A reply is the run of assistant entries after its user
  entry. Every turn begins by saving its user entry, so a run never spans two
  replies. Existing files hold each reply as one assistant entry, a run of
  one, and load and resume unchanged.
- **Request.** `request` joins each run of consecutive assistant entries into
  one assistant message, so the model sees each reply as one message, whether
  it was saved in one entry or many. `history` stays exactly the file's
  entries. Use one pass over the run to join the text without repeatedly
  copying the prefix.
- **Reply text.** Remove `turn.reply`. A completed turn's result text is the
  joined run of assistant entries at the end of `history`, or empty when the
  last entry is the turn's prompt. The reply then has one source, the saved
  entries.
- **Save before publish.** In `Handle`, a `delta` for the running turn is
  saved first. Only after the save succeeds is it published as a `text_delta`.
  If the save fails, the delta is not published, and the turn ends at once
  through `finish` with the error result `Ur could not save the reply: <err>`.
  `finish` cancels the request, and its later deltas and end belong to a turn
  that is no longer running, so they are ignored. Entries saved before the
  failure stay. The model's context and the transcript both end at the last
  saved delta.
- **Turn end and Stop.** `finish` no longer saves anything. It publishes the
  result, reports idle, cancels, clears the turn, and begins the next input.
  `Stop` no longer saves anything. It cancels, publishes `exited`, and clears
  the turn. Remove `keepReply`. The combined text that `finish` builds for a
  failed turn whose save also failed goes with it.
- **Requirements.**
  - **Ur sessions**: after a kill, the resumed context includes all reply text
    shown in the transcript. A lost or unreadable file still refuses the send.
  - **Surviving restarts** and **Resuming the conversation**: the token and
    the history are unchanged.
  - **Stop**: an interrupted turn's text is already saved when it is shown. A
    delta still in flight after the interrupt is ignored and never saved, as
    `TestInterrupt` pins.
  - **Parking**: `Parkable` is unchanged. It is true only while idle, and by
    then every shown delta is saved.
  - **Setting changes keep the rest**: Configure refuses while a turn runs, so
    every entry of one reply records the same model.
  - **Deleting a session**: the file stays, unchanged.
  - **Database failures**: unchanged. A failed history write is logged, and
    the model keeps the delta that the transcript lacks.
- **Construction.**
  - Text a page sees, or the history holds, is in Ur's file. Ur publishes a
    delta only after `save` returns, and the server records and broadcasts
    only what Ur publishes.
  - `history` equals the file's entries because `save` is still their only
    writer.
  - A reply's text has one source, the trailing run of assistant entries.
  - If `oxd` is killed between Ur's save and the server's record, the file
    holds one delta the transcript lacks. Claude's own transcript has the same
    relation to Ox's history.
- **Ownership.** No new state, timer, retry, or lock. The stored change is that
  each delta becomes its own assistant entry in the existing file, written on
  the session goroutine, the file's only writer. `turn.reply` is removed. This
  keeps the displayed text in resumed context after a kill. The stream
  goroutine never touches the file. The file's lifetime is unchanged.
- **Settling.** No new waits. Each save is a local open, append, and close on
  the session goroutine, as it is for a prompt today.

## Check your assumptions

None.

## Test plan

All Ur harness tests, in `internal/ur/ur_test.go`:

- **Resume mid-turn.** Add a new `TestResumeDuringTurn`. Existing tests stop
  the first agent before resuming it. The handler's first request streams
  `remember` and ` blue` as two deltas, flushes, and blocks on the request's
  context; later requests stream `ok`.
  1. Send `hi`, drain the request, and handle updates until the ` blue`
     `text_delta`.
  2. Without calling `Stop` or `Interrupt` on the first agent, as when `oxd`
     is killed, `launch` a second agent with the token. The first agent is
     stopped by cleanup.
  3. Send `next`. The second request's contents are `hi`, `remember blue`,
     `next`, with roles `user`, `assistant`, `user`.

  This fails before the change, which sends only `hi`, `next`. It also fails
  if `request` stops joining the run.
- **Stop.** Keep `TestStopDuringTurn`'s assertions, which still hold. Reword
  its comment: Stop ends the turn and drops waiting inputs, and the reply is
  already saved as far as it streamed.
- **Delta entries and attribution.** In `TestResume`, turn one streams `o` and
  `ne` as two deltas on model A; turns two and three stream one delta each.
  - The resumed request is still `hi`, `one`, `again`, `two`, `third`, with
    alternating roles and no `model` keys.
  - The file's lines are `user hi`, `assistant o A`, `assistant ne A`,
    `user again`, `assistant two B`, `user third`, `assistant three B`.
  - The one-entry replies have the form every existing file has, so they also
    cover loading an existing file.
- **Save failure.** In `TestSaveFailure`, replace the `completed reply` and
  `failed reply` cases with one `reply` case. The first request streams `one`,
  flushes, waits on a release channel, then streams `two` and finishes.
  1. Handle updates until the `one` `text_delta`, make the file read-only,
     and release.
  2. The result is an error starting `Ur could not save the reply: ` that
     names the denied write, and no `text_delta` carries `two`.
  3. After restoring the mode, the next request's contents are `hi`, `one`,
     `again`.

  Keep the `prompt` subtest. Remove the `stop` subtest, since `Stop` no longer
  writes.
- **Unchanged tests.**
  - `TestTwoTurns`' result text `Hello, world` pins the result derived from
    the saved entries.
  - `TestInterrupt`'s `hi`, `partial`, `again` pins that the in-flight
    ` late` delta is never saved.
  - `TestStreamErrors`' roles pin that a failed turn's streamed text is kept
    and an empty one adds nothing.
- **Live.** `TestLiveUr`, unchanged, sends the joined reply to the real model
  on its second turn.

## Implementation plan

1. `internal/ur/ur.go`:
   - Remove `turn.reply` and `keepReply`.
   - In `Handle`, save a running turn's delta as an assistant entry with the
     current model before publishing it. On a save failure, call `finish` with
     the error result `Ur could not save the reply: <err>` and publish nothing
     for the delta.
   - On `ended` without an error, finish with the text of the trailing run of
     assistant entries.
   - Make `finish` and `Stop` save nothing.
   - Make `request` join each run of consecutive assistant entries into one
     message, and update its comment.
   - Update the `turn` and `entry` comments: an entry is a prompt, or one
     delta of a reply.
2. `internal/ur/ur_test.go`: the tests above.
3. Documentation as below.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`: the `ur` row says Ur appends each prompt, and each
  delta of a reply as it streams, to the JSONL file named by its resume token.
- `docs/todo.md`: remove the **Ur forgets a streamed reply after a daemon
  crash.** entry.
