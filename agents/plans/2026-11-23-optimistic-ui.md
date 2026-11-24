# Show actions at once, and session status on first load

## Goal

On first load no session shows a status dot until its `session/get` returns
with its whole history, so busy sessions with long histories stay blank last.
And each user action leaves the page unchanged until `oxd` answers: the composer
keeps the text, dialogs stay open with a disabled button, approval cards stay
with disabled buttons, Stop stays.

When the change is done, every session's status shows as soon as the session
list does, and every action changes the page as soon as the user takes it,
showing the result it expects. On success the page stays as shown; on failure
it returns to `oxd`'s state and shows the error where the action was taken.

Timings measured live through the relay against `make dev`, with Haiku, for
context:

| Action                                                    | Reply  | Until the outcome    |
| --------------------------------------------------------- | ------ | -------------------- |
| `session/create`                                          | 1–5 ms |                      |
| `session/send` to a parked Claude session                 | 9 ms   | 3.6 s until idle     |
| `session/send` to an idle running Claude session          | 1 ms   |                      |
| `session/interrupt` during a turn                         | 2 ms   | 14 ms until idle     |
| `session/archive`, family of two, running then idle       | 5 ms   | `session/archived` 3 ms |
| `session/unarchive`                                       | 4 ms   |                      |
| `workspace/create`                                        | 3–17 ms |                     |

Every reply is fast locally; the wait users see is the round trip over the
tailnet, the first load, and the interval between a reply and the update that
shows its outcome (Stop).

## Related code

- `internal/server/methods.go` — `listWorkspaces` answers under `s.mu` with each
  session's label; `publishLocked` broadcasts `session/created` under `s.mu`
  before `createSession` replies; `sendSession` generates the prompt's message
  ID through `sess.sendPrompt`.
- `internal/server/session.go` — `session.update` records and broadcasts every
  update; `labelFrom` shows the pattern this change follows for `state`: a field
  written by the session's goroutine under `s.mu`, broadcast while held, so
  `workspace/list` and the notification are ordered. `archive` broadcasts
  `session/archived` before it stops processes and replies; `destroy`
  broadcasts `session/destroyed` before replying.
- `internal/server/mcp.go` — `addChild` starts the child's agent before
  `publishLocked`, so a child is published idle and its `running` update
  follows.
- `internal/agent/tracker.go` — `Accept` publishes `prompt_queued` inside
  `Send`, before `session/send` replies. `internal/claude/approval.go` —
  `Approve` publishes `approval_answered` before `session/approve` replies.
- `cmd/ox/web/src/ox.tsx` — `Session`, `SessionSnapshot` (holds `state`
  today), `apply`, `load`, `reload`, `receive`, `createSessionControl`
  (`sending`, `sent`, `edits`, `creation`, `requested`, `post`, `create`,
  `finishPrompt`, `reconcilePrompt`), and `createOx`'s `archive`, `unarchive`,
  `destroy`, `removeWorkspace`.
- `cmd/ox/web/src/Sidebar.tsx` — `SessionRow` reads `load.snapshot?.state`;
  `archiveBlocked` requires every member live; `WorkspaceDialog` holds delete and
  remove open with `busy`.
- `cmd/ox/web/src/SessionView.tsx` — `running`, the Stop button, the approvals
  section, `Transcript`'s pending region, the Unarchive button.
- `cmd/ox/web/src/NewSessionView.tsx` — the draft's composer and empty log.
- `cmd/ox/web/src/ApprovalRequest.tsx` — `PermissionCard` and `QuestionForm`
  disable their controls with a `sending` signal.
- `cmd/ox/web/src/App.tsx` — `Layout` selects sessions through `ox.session`,
  finishes a draft on the create reply, and drops drafts of removed workspaces.
- `cmd/ox/web/e2e/ui.ts` — `hold` holds a method's `/rpc` post until released.

## Decisions

- **Published state on the session.** `session` gains `state string`, guarded
  by `Server.mu`: the state last published to clients. `session.update` writes
  it for `state` (the update's `State`) and `parked` (`"parked"`) updates and
  broadcasts those two update types while holding `s.mu`, as `labelFrom` does.
  `publishLocked` sets it from `sess.snapshot().State` before broadcasting
  `session/created`; whoever builds the session owns it until `run` starts, so
  that read is safe: a top-level session is published `parked`, a child `idle`.
  `workspace/list` and `session/created` carry `state` from this field. Each
  reply is queued under `s.mu` after every state broadcast it reflects and
  before every one it does not. `session/get` keeps reporting the agent's
  snapshot.
- **The page's state is on `Session`.** `Session` gains `state:
  SessionState`; `SessionSnapshot` loses it. The page takes the state from
  `workspace/list`, `session/created`, and `state` and `parked` updates, which
  it applies whether or not the session is loaded. `load` no longer writes the
  state, and `apply` no longer handles `state`; `parked` still stops tools in a
  loaded snapshot. One function in `createOx`, `setState(id, state)`, writes it
  from all three sources. The Sidebar dot, `archiveBlocked` (now requiring only
  `ready()` and settled states, not loaded snapshots), `running` in
  `SessionView`, and `canConfigure` read `session.state`.
- **Page-chosen message IDs.** `session/send` takes an optional `message_id`, 32
  hexadecimal digits, checked as `session/create` checks `session_id`, else
  `-32602`. `sendPrompt` takes the ID and generates one when it is empty, as
  today. The page generates the ID at submit, so its pending bubble and
  `oxd`'s `prompt_queued` name the same prompt and the page never shows both.
- **Intents in flight, and their owners.** A request the page has sent and
  `oxd` has not answered is the page's own fact. Two owners hold them:
  - The session's control holds the composer's requests:
    - `outgoing: { id; text; images: File[]; phase: "creating" | "sending" |
      "sent" } | undefined` replaces `sending`, `sent`, `edits`, and the
      `"creating"` value of `creation`, which becomes `unconfirmed: boolean`.
      Submit takes the text and images into `outgoing` and clears the composer
      at once. `create` moves `creating` → `sending` on its reply; `post` moves
      `sending` → `sent` on its reply. A failed create or send, or a create
      answered `existing`, clears `outgoing` and returns its text and images
      ahead of anything typed since, as `finishPrompt` does for a dropped
      prompt; `finishPrompt` and `reconcilePrompt` match `outgoing.id` in any
      phase. `canSend` and `canCreate` require no `outgoing` in `creating` or
      `sending`.
    - `stopping: boolean`, set by `interrupt`, cleared by its failure and by
      `createOx`'s `setState` whenever it writes a state other than `running`
      or `requires_action`. Stop shows while running and not stopping.
    - `answering: Set<request_id>` (a signal holding a new set per change),
      each added by `approve` and removed when its request settles.
    - `requested` (settings) is unchanged.
  - `createOx` holds the session list's requests in one store:
    `listing: { sessions: Record<session_id, "archive" | "unarchive" |
    "destroy">; workspaces: Record<workspace_id, "remove"> }`. A session or
    workspace has at most one list request in flight, and its menu items are
    unavailable while it has one. Each entry is removed when its request
    settles. `archive`, `unarchive`, `destroy`, and `removeWorkspace` set the
    entry, send the request, and return its promise for the caller's
    `createAttempt`.
- **What the page shows.** Views read oxd's store through the intents, without
  copying it:
  - `ox.session(id)` is undefined for a session being destroyed, so its row and
    its view go at once; `ox.workspaces()` leaves out workspaces being removed;
    `ox.sessionsOf(workspace)` leaves out sessions being destroyed.
    `ox.workspaceOf` and the store stay unfiltered for `createOx`'s own use.
  - `ox.archived(session)` walks from the session up through its ancestors and
    returns `true` at the first with `archive` in flight, `false` at the first
    with `unarchive`, and the session's `archived` otherwise. Every view reads
    it in place of `session.archived` for display.
  - Whether an action is offered follows `oxd`'s store, not the intents:
    Remove workspace while the store lists sessions, Delete while the store
    lists children, Unarchive while the store's parent is archived.
  - The control's `free()` is also false while the session or an ancestor has
    a list request in flight, so a session being unarchived holds Send and
    settings until `oxd` confirms.
  - The pending bubble: `Transcript` shows `outgoing` in the Pending region
    while it is in `creating` or `sending` and `snapshot.queued` and
    `snapshot.entries` lack its ID; `NewSessionView`'s log shows it while the
    draft's control is creating. `PromptBubble` takes local files and shows
    each through an object URL revoked on cleanup, as `Thumbnail` does.
  - An approval being answered stays mounted with `hidden`, so a refused answer
    brings the card back with the choices and answers the user gave; the
    "Waiting on you" region hides when every approval is being answered.
    `ApprovalRequest` loses `sending`, and `onAnswer` returns nothing.
  - Delete and Remove workspace close their dialog on confirm, and their errors
    show in the session list's alert. Add workspace keeps its dialog and `busy`
    until `oxd` answers (Check your assumptions, 2).
  - A new session's draft view clears its composer and shows the prompt
    pending at submit; the view moves to the session on the create reply, as
    today.
- **Requirements.** _Session status_: shown from `workspace/list` on first
  paint, and after a reconnect from the new list, which is ordered after every
  state update it reflects. _Composer after a send_, _Unsent text_, _Attached
  images in the composer_: the composer clears at submit; text and images
  return on failure ahead of anything typed since; a reply lost to a drop fails
  as today, returning the text, and the transcript after reconnect shows
  whether Claude got it (_Send cut off_). _Only the user sends_: no request
  repeats; a failed send returns its text. _One prompt at a time_: the pending
  bubble shows from submit, and Send is held while `outgoing` is creating or
  sending or the queue is not empty. _Stop_: the button hides on click and
  returns if `oxd` refuses; a dropped prompt returns through `prompt_cancelled`
  as today. _Creating a session_, _Creation keeps the user's view_, _Failed
  creation_, _Lost creation reply_: creation still sends exactly once; a
  failure or `existing` returns the text to the draft, and recovery is
  unchanged. _Prompt size limit_, _Setting commands_: refused prompts return to
  the composer with the error. _Archiving a session_, _Archived sessions_:
  rows and the session view show archived or unarchived at once; the
  family-wide checks stay with `oxd`. _Deleting a session_, _Removing a
  workspace_: rows go at once; a refusal brings them back with the error.
  _Allow and Deny_, _One answer per approval_: an answer already given on
  another page removes the card through `approval_answered`, and the refused
  second answer shows its error. _Lost connection_: a drop fails every request
  in flight, which returns each intent to `oxd`'s state with its error. _Every
  request settles_: each intent ends with its request, except `stopping`,
  which ends with the session leaving `running`. A second page acting on the
  same session sees only `oxd`'s store; intents are per page.
- **Settling.** Every new intent ends when its request settles, and every
  request settles by reply or by the connection's drop. `stopping` also waits
  for the state to leave `running`, which a turn's end, an exit, parking, or a
  failed interrupt reaches; while issue #34's startup turn ignores Stop, Stop
  stays hidden until that turn ends.
- **Construction.** One owner per intent, each a value that exists only while
  its request is in flight. `outgoing`'s phase union rules out a prompt that is
  both sending and sent; one `listing` entry per session rules out an archive
  and a delete in flight together. Displayed values come from functions over
  the store and the intents, never from writes into the store. The published
  state has one writer, `session.update`, plus `publishLocked` before the
  goroutine runs.
- **Ownership.** `session.state` (server): owner the session goroutine, written
  under `Server.mu`; needed so the list shows every status on first paint; its
  race with `workspace/list` is closed by broadcasting under `s.mu`; it goes
  with the session. Client intents: owners and cleanup above; needed so each
  action shows at once; a request's reply and the update reporting its outcome
  can arrive in either order, and both orders show the same result because the
  page shows the intent until the reply and the store after it, and the
  updates measured above arrive before their replies.
- **Claude Code.** No new assumption; Stop's intent rests on the published
  state, not on Claude's output.

## Check your assumptions

1. **Every action shows its result at once.**
   - **Plan:** Sending, creating a session, Stop, Allow and Deny, archiving,
     unarchiving, deleting a session, and removing a workspace change the page
     the moment you act. If Ox refuses, the page goes back and shows why where
     you acted. This adds the requirement "Actions show at once" and changes
     "Composer after a send", "Failed creation", and "Database failures" to
     match (Requirement changes).
   - **Alternative:** Keep today's wait for slow actions only; replies measure
     a few milliseconds locally, but the wait shows over the tailnet.
2. **Adding a workspace still waits.**
   - **Plan:** The Add workspace dialog stays open until Ox answers, because
     only Ox can tell whether the directory exists; a typo keeps the path in the
     dialog with the error.
   - **Alternative:** Close the dialog and show the workspace at once; a
     mistyped path would then reopen the dialog with the path and error, and
     adding an existing directory would show a second workspace for a moment.
3. **A new session joins the list on Ox's answer.**
   - **Plan:** Submitting a new session clears the composer and shows the
     prompt pending in the draft at once; the session's view and its row in the
     list follow when Ox creates it.
   - **Alternative:** Show a placeholder row in the list at once, which would
     show a label that then changes twice.
4. **Status shows on first paint.**
   - **Plan:** Adds to "Session status" that the status shows as soon as the
     session is listed (Requirement changes).
   - **Alternative:** Leave the requirement as it is; the fix would have no
     requirement guarding it.

## Test plan

Server, with the fake harness:

- `TestSessionLifecycle` (`internal/server/methods_test.go`):
  `session/created` and `workspace/list` give a new session `state` `parked`;
  after a send's `state` `idle` update, `workspace/list` gives `idle`.
- `TestCreateSession` (`internal/server/mcp_test.go`): the child's `session/created`
  carries `state` `idle`, and its `running` update follows.
- `TestSessionLifecycle`: `session/send` with a `message_id` returns that ID,
  and `prompt_queued` and `prompt` carry it; a malformed `message_id` returns
  `-32602`.

Page, in `make e2e` with `hold` and injected failures on `/rpc`:

- `session.spec.ts` "session list membership and order don't wait for
  transcripts": while `session/get` is held after the reload, each row shows its
  status (the `img` named `idle`).
- New "a send clears the composer and shows the prompt pending before oxd
  answers": with `session/send` held, the composer is empty, Pending shows the
  prompt once, and Send is disabled.
- New "a refused send returns its text and images ahead of text typed since":
  the held `session/send` is answered with an error; the composer holds the
  prompt, a newline, and the typed text, and the thumbnail returns, with the
  error.
- "edits typed while a new session is created and sent stay in the composer":
  rewritten for the new flow: with `session/create` held, the draft's composer
  is empty and its log shows the prompt pending; text typed meanwhile stays
  after creation and send. "text retyped equal to the sent prompt stays after
  the send" and "text retyped equal to the prompt stays after the creation"
  keep their assertions.
- "Stop appears once the page's send is accepted, and stops it": with
  `session/interrupt` held, Stop is gone before release. New "a refused Stop
  shows Stop again with the error".
- New "an answer hides its approval before oxd answers, and a refusal brings it
  back with the choice": with `session/approve` held, "Waiting on you" is
  hidden; answered with an error, the card returns with Allow checked and the
  error shows.
- "a failed archive shows its error in the session list": also the row hides
  while `session/archive` is held and returns with the error. New "Unarchive
  shows the composer before oxd answers".
- "a cancelled deletion's reply cannot close the next confirmation" is replaced
  by "Delete closes its dialog and removes the row before oxd answers, and a
  refusal brings it back with the error".
- `workspace.spec.ts` "a cancelled removal cannot settle the next removal
  dialog" and "removing a workspace asks first and shows a failure in the
  dialog" are replaced by "removing a workspace asks first, removes its section
  before oxd answers, and shows a failure in the session list".

## Implementation plan

1. `internal/server/session.go`, `methods.go`: add `session.state`, write it and
   broadcast `state` and `parked` updates under `s.mu` in `session.update`, set
   it in `publishLocked`, and carry `state` in `workspace/list` and
   `session/created`. Add `message_id` to `sendSession` and a `messageID`
   parameter to `sendPrompt`. Server tests.
2. `cmd/ox/web/src/protocol.ts`: `state` on `workspace/list` sessions and
   `session/created`; `message_id?` on `session/send`.
3. `cmd/ox/web/src/ox.tsx`: move `state` to `Session` with `setState`; the
   control's `outgoing`, `unconfirmed`, `stopping`, `answering`; `createOx`'s
   `listing` store, `workspaces`, `sessionsOf`, `session`, `archived`, and the
   list actions; pass the control its list-request accessor for `free()`.
4. `cmd/ox/web/src/Sidebar.tsx`: read `session.state`, `ox.workspaces()`,
   `ox.sessionsOf`, `ox.archived`; close Delete and Remove on confirm and report
   their errors in the list's alert; disable a target's items while it has a
   list request.
5. `cmd/ox/web/src/SessionView.tsx`, `NewSessionView.tsx`,
   `ApprovalRequest.tsx`, `App.tsx`: pending bubble from `outgoing` with local
   images, Stop from `stopping`, approvals hidden while answering, `archived`
   through `ox.archived`, Unarchive through `ox.unarchive` reported by the
   control's error, and `Layout` reading `ox.workspaces()` for display while
   draft cleanup keeps reading the store.
6. e2e specs above.
7. `docs/protocol.md`, `docs/architecture.md`, and the approved requirement
   text in `docs/functional-requirements.md`; `make docs`.

## Requirement changes

- **Added** (Pages and connection): **Actions show at once.** Each action shows
  its result on the page as soon as the user takes it, and adding a workspace
  as soon as Ox has checked the directory. If Ox refuses an action, the page
  goes back to how things stand and shows why where the action was taken.
- **Changed:** **Session status.**
  - Old: Each session shows whether it is parked, idle, idle while background
    work Claude started still runs, running, waiting on the user, or archived.
  - New: Each session shows whether it is parked, idle, idle while background
    work Claude started still runs, running, waiting on the user, or archived,
    as soon as the session list shows it.
- **Changed:** **Composer after a send.**
  - Old: After a successful send the composer clears, keeping anything typed
    since the send.
  - New: Sending clears the composer at once. If the send fails, its text and
    images return to the composer ahead of anything typed since.
- **Changed:** **Failed creation.**
  - Old: If creating a session fails, the draft keeps its text and settings and
    shows the error, with a way back to the draft from any other view, and the
    user can submit again.
  - New: If creating a session fails, its text and images return to the draft
    with its settings and the error, with a way back to the draft from any
    other view, and the user can submit again.
- **Changed:** **Database failures.**
  - Old: Adding or removing a workspace shows a failure to save in its dialog;
    any other failure to save goes to `oxd`'s log. A session whose conversation
    could not be saved still resumes it until `oxd` restarts.
  - New: Adding a workspace shows a failure to save in its dialog, and removing
    one in the session list; any other failure to save goes to `oxd`'s log. A
    session whose conversation could not be saved still resumes it until `oxd`
    restarts.

## Documentation updates

- `docs/protocol.md`: `workspace/list` sessions carry `state`, and drop "A
  session's settings and state come from `session/get`" in favor of settings
  alone; `session/created` carries `state`; `session/send` takes `message_id`.
- `docs/architecture.md` (Web UI): the control owns the page's composer
  requests in flight and shows each until `oxd` answers; `ox.tsx` owns the
  session list's requests in flight; the settings form paragraph's "Send
  disabled" stays; a session's state comes from the list and its updates.
