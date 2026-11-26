# Chat request order and status behind file tabs

## Goal

Resolve [#148](https://github.com/kkestell/ox/issues/148), audit findings F4
and F5 in [the 2026-11-26 audit](../audits/2026-11-26-main.md).

- **F4.** A page can have a steering `session/send` and a `session/interrupt` in
  flight for one session at once, as independent POSTs, so `oxd` can take them
  in the opposite order from the clicks. When done, the page has at most one of
  create, send, configure, and Stop in flight per session, so `oxd` always
  receives them in click order. No `oxd` change is needed: `oxd` replies to
  `session/send` after the prompt is queued or taken, and to
  `session/interrupt` after the stopped turn has ended
  ([protocol](../../docs/protocol.md#sessioninterrupt)), so a request the page
  posts after the previous reply is ordered after it.
- **F5.** A create error, a recovery notice, a refused setting or approval
  answer, and a newly waiting approval all show only inside the chat view,
  which a file or Changes tab hides. When done, the chat tab carries a mark
  while its chat holds one of these and is hidden, and Reopen draft shows the
  draft's chat.

The change stays inside the web client: no state owner in `oxd`, protocol,
persistence, or harness driving.

## Check your assumptions

1. **The send button while a request is in flight.**
   - **Plan:** Stop shows only when it can be pressed. While the page's prompt
     is on its way to Ox during a running turn, the button stays a disabled
     Send for the moment it takes Ox to accept it, then becomes Stop, as it
     already does for the first prompt of an idle session. After Stop is
     pressed, the button is a disabled Send until the stopped turn has ended;
     the user can type meanwhile and send once it ends, and the new prompt
     starts the next turn instead of being dropped by that Stop.
   - **Alternative:** Show Stop disabled while the prompt is on its way. The
     button would not flicker between Send and Stop, but the idle and running
     cases would differ, and the rule for which icon shows would have a third
     state.
2. **How hidden chat status shows while a file tab is in front.**
   - **Plan:** While another tab hides the chat, the chat tab shows a dot: red
     when the chat holds an error, amber (the session list's "waiting on you"
     color) when an approval waits or Ox recovered a creation and is waiting for
     the user to send. Its tooltip is the error, the notice, or "Waiting on
     you". Selecting the tab shows it. On a narrow screen the tab picker's
     button and its chat item carry the dot. The page never switches tabs by
     itself. Reopen draft, on the page-level error for a failed creation, opens
     the draft with its chat tab in front.
   - **Alternative:** Switch to the chat tab whenever one of these arrives.
     Nothing could be missed, but a reply or approval would pull the user out of
     the file they are reading, which **Creation keeps the user's view** avoids
     for the same reason.

## Requirement changes

- **Changed: Stop.**
  - Old: "While Claude works and the composer is empty, the Send button becomes
    Stop. Once pressed, Stop does not return until the turn it stopped has
    ended. Stop ends the turn, …"
  - New: "**Stop.** While Claude works, the composer is empty, and Ox has
    accepted any prompt this page sent, the Send button becomes Stop. Once
    pressed, Stop does not return, and Send stays unavailable, until the turn it
    stopped has ended. Stop ends the turn, the transcript shows it was
    interrupted, and the session takes prompts again; a pending prompt is
    dropped, and its text returns to the composer of the page that sent it,
    ahead of anything typed since. Messages from other sessions that Claude has
    not taken are dropped, and their senders are not told."
- **Changed: Failed creation.**
  - Old: "If creating a session fails, its text and images return to the draft
    with its settings and the error, with a way back to the draft from any other
    view, and the user can submit again."
  - New: "**Failed creation.** If creating a session fails, its text and images
    return to the draft with its settings and the error, with a way back to the
    draft's chat from any other view, and the user can submit again."
- **Added (Layout, after Tabs): Chat status behind other tabs.** "**Chat status
  behind other tabs.** While another tab hides a session's or draft's chat, its
  chat tab is marked when the chat shows an error, a notice, or an approval
  waiting on the user, and selecting it shows them. On a narrow screen the tab
  picker carries the mark."

## Related code

- `cmd/ox/web/src/ox.tsx:216`–`498` — `createSessionControl`. `outgoing`
  carries the prompt and its phase (`creating`, `sending`, `sent`), `stopping`
  is a separate boolean, and `requested` a separate patch. `free()` (`:258`)
  excludes a create, send, or configure, but not a Stop.
- `cmd/ox/web/src/SessionView.tsx:49`, `:167`–`186` — decides Stop from the
  session state, the composer, and `stopping`, ignoring a send in flight.
  `:76` derives the waiting approvals; `:110`–`111` show the control's error and
  notice inside the chat.
- `cmd/ox/web/src/NewSessionView.tsx:32`, `:73` — the draft's pending prompt and
  error, inside the chat.
- `cmd/ox/web/src/TabArea.tsx:202`–`204` hides the chat when another tab of its
  group is active; `Strip` (`:296`) and `PhoneBar` (`:360`) draw the tabs.
- `cmd/ox/web/src/TabPicker.tsx` — the narrow layout's picker and menu.
- `cmd/ox/web/src/App.tsx:203`–`216` — page-level alert with Reopen draft for a
  draft that is not shown.
- `cmd/ox/web/src/tabs.ts:129` — `activate(layout, key)`; the chat tab's key is
  `"chat"`.
- `cmd/ox/web/e2e/session.spec.ts:744`–`807`, `:1016` — the Stop specs;
  `e2e/workspace.spec.ts:33`–`80` — file-tab helpers; `e2e/ui.ts` — `hold`,
  `cutReply`.

## Decisions

- **Construction (F4).** The session control holds one `request` signal, the
  chat request it has in flight:

  ```ts
  // Request is the chat request the page has in flight for the session. The
  // page sends one at a time, so oxd takes them in the order the user made them.
  type Request =
    | { kind: "create" }
    | { kind: "send" }
    | { kind: "configure"; patch: SettingsPatch }
    | { kind: "interrupt" };
  ```

  It replaces `stopping`, `requested`, and `Outgoing.phase`. `outgoing` keeps
  only the prompt (`{ id, text, images }`) from `take` until Claude takes it, it
  drops, or `oxd` refuses it. What the phases said is derived: `requested()` is
  the `configure` patch, `stopping()` is `kind === "interrupt"`, a prompt is
  being created or sent while `request()` is `create` or `send`, and it is sent
  once `outgoing()` is set and neither is in flight. A Stop and a send cannot
  both be in flight because the type has one slot.
- **One gate.** `free()` becomes `connected() && request() === undefined &&
  !busy()`. `canSend` and `canConfigure` keep their extra conditions. A new
  `canStop()` is `free()` and the session `running` or `requires_action`; it
  moves the running check out of `SessionView`. The view shows Stop when
  `canStop() && !sendable()`, otherwise Send, enabled by `canSend() &&
  sendable()`. Enter on an empty composer still sends nothing.
- **Slot ownership.** Each request clears the slot when it settles only if the
  slot still holds that request object (`request() === mine`), so `create`'s
  success handler can hand the slot straight to its `send` through `post`
  without a gap in which Stop or a configure could start. Every path settles:
  success, `RpcError`, relay failure, and `Disconnected` all reject or resolve
  the rpc promise, and the slot clears in `finally`.
- **Prompt reconciliation.** `post`'s success handler clears the slot, then
  reconciles `outgoing` against the live snapshot as today (taken, still
  queued, or dropped). `reconcilePrompt` treats a prompt as accepted when no
  `create` or `send` is in flight, in place of `phase === "sent"`. A `prompt` or
  `prompt_cancelled` update that arrives before the send's reply still finishes
  `outgoing` at once; the slot stays until the reply.
- **Attention (F5).** The control derives `waiting()` (the live snapshot's
  approvals with no answer in flight, moved from `SessionView`) and
  `attention()`:

  ```ts
  // Attention is what the chat shows that needs the user: an error, an
  // approval waiting on them, or a notice.
  type Attention = { kind: "error" | "notice"; text: string } | { kind: "waiting" };
  ```

  An error comes first, then a waiting approval, then a notice. It is derived
  from `error()`, `waiting()`, and `notice()`, never stored. Drafts and
  sessions both have it, since a draft's view is its control's.
- **Where the mark shows.** `Strip` marks the chat tab when `attention()` is
  set and the chat is not its group's active tab. `PhoneBar` gives the chat's
  `PickerTab` an `attention`; the picker marks that item, and marks its button
  when the tab carrying attention is not the active one. The mark is a
  `span.status` dot (`role="img"`, `aria-label` and `title` the error text, the
  notice text, or "Waiting on you"), reusing the session list's dot. Its color
  is `--critical` for an error and `--warning` otherwise, in the tab and picker
  sections of `style.css`; no new token.
- **Reopen draft.** The button selects the draft and activates its chat tab
  with `failed.control.edit((l) => activate(l, "chat"))`.
- **Requirements through the request lifecycle.** **Stop**: a reply lost to a
  reconnect rejects with `Disconnected` and clears the slot; the page then
  holds Send until it has caught up, as **Lost connection** says. A second page
  acting on the same session sends independent requests, whose orders are all
  valid outcomes. **One prompt at a time**, **Composer after a send**, and
  **Pending setting changes** keep their behavior, now derived from `request`.
  **Every request settles**, **Failed creation**, **Lost creation reply**,
  **Permission requests**: the error, notice, or approval stays where the
  request was made, in the chat, and the mark makes it reachable while another
  tab is in front. **Creation keeps the user's view**: the page never switches
  tabs by itself.
- **Ownership.** No new state is stored. `request` replaces three signals with
  one, owned by the session control, cleared when its request settles. Races:
  none new; the slot removes the send/Stop crossing.

## Test plan

All e2e, against `ox` and `oxd` with the fake harness, holding requests with
`hold` or `cutReply`. Each new spec fails before the change.

F4, in `e2e/session.spec.ts` beside the Stop specs:

- New: **"Stop waits for a steering send to be accepted, then drops its
  prompt"**. Start `wait 60000`; hold `session/send`; send `steer`; on arrival,
  Stop is absent and Send disabled; release; Stop shows; click it; the pending
  `steer` returns to the composer and the transcript shows `Interrupted` with no
  reply to `steer`. Before the change Stop shows while the send is held.
- New: **"Send stays unavailable while Stop is held, and a prompt typed
  meanwhile starts the next turn"**. Start `wait 60000`; hold
  `session/interrupt`; click Stop; on arrival type `next`; Send is disabled and
  Enter posts no `session/send` (`rpcCalls`); release; Send enables; send;
  the transcript shows `Interrupted` then `You said: next`. Before the change
  Send is enabled while Stop is held.
- Existing **"Send becomes Stop once the page's send is accepted, and stops
  it"** and **"Stop drops a pending prompt…"** keep passing unchanged.

F5, in `e2e/workspace.spec.ts`, in a new `describe("chat status behind a file
tab")` using `openFile` on a fixture file. Each asserts the chat tab's mark by
role (`tab` named for the chat, then `img` by name), then selects the chat tab
and sees the alert, status, or approval:

- **"a creation refused while a file tab shows marks the chat tab"**: draft;
  hold `session/create`; submit; open a file; `release({ model: "not-offered" })`;
  the chat tab's mark names `not-offered`; selecting the chat shows the alert
  and the restored text.
- **"Reopen draft shows the draft's chat"**: as above, but switch to another
  session before the release; Reopen draft; the chat tab is selected
  (`aria-selected`) and the prompt holds the text.
- **"a refused setting change while a file tab shows marks the chat tab"**:
  session; hold `session/configure`; choose `plan`; open a file;
  `fail("configuration refused")`.
- **"a refused approval answer while a file tab shows marks the chat tab"**:
  `ask` Bash; hold `session/approve`; Allow; open a file;
  `fail("relay unavailable")`; the mark names the error, and the chat shows the
  approval again.
- **"an approval that arrives while a file tab shows marks the chat tab"**:
  hold `session/send`; send `approve Bash {}`; open a file; close the session
  list; release; the mark is "Waiting on you"; selecting the chat shows the
  approval.
- **"a recovered creation behind a file tab marks the chat tab with its
  notice"**: `cutReply` on `session_id`; arm; submit a draft; open a file;
  `restartRelay`; the mark names "Session recovered"; selecting the chat shows
  the notice and the restored text, and no `session/send` was posted. Move
  `open` from `session.spec.ts` to `e2e/ui.ts` for this.
- **"a chat shown beside a file has no mark"**: split the file to the right;
  refuse a held configure; no mark on the chat tab.
- In the narrow `describe`: **"an approval behind a file marks the tab
  picker"**: as the arriving-approval case; the picker button and its chat item
  carry "Waiting on you".

## Implementation plan

1. `cmd/ox/web/src/ox.tsx`: add `Request`; replace `stopping`, `requested`, and
   `Outgoing.phase` with `request`; rework `free`, `take`, `post`, `create`,
   `configure`, `interrupt`, `restore`, and `reconcilePrompt` around it, with
   the identity check on settle; add `canStop`, `waiting`, `attention`; expose
   `submitting()` (`create` or `send` in flight with `outgoing` set) for the
   views. Update the control's doc comment: a create, send, configuration, and
   Stop exclude one another.
2. `cmd/ox/web/src/SessionView.tsx`: use `control.canStop()` and
   `control.waiting()`; drop the local `running` and `waiting`; `Transcript`
   shows `outgoing` while `submitting()` and not yet reported.
3. `cmd/ox/web/src/NewSessionView.tsx`: `creating()` reads `request()?.kind ===
   "create"`.
4. `cmd/ox/web/src/TabArea.tsx` and `TabPicker.tsx`: the chat tab's mark and the
   picker's, as in Decisions.
5. `cmd/ox/web/src/App.tsx`: Reopen draft activates the draft's chat tab.
6. `cmd/ox/web/src/style.css`: the mark in the tab strip and tab picker
   sections, from existing tokens.
7. Specs from the Test plan; move `open` to `e2e/ui.ts`.
8. `docs/functional-requirements.md`: the approved requirement text.
9. `docs/architecture.md`, "Session control, drafts, and optimistic state" and
   "Chat tab": as below.

## Documentation updates

- `docs/architecture.md`, Session control: "Sends and settings changes are
  mutually exclusive" becomes "A creation, send, settings change, and Stop
  exclude one another: the control holds one in flight, so `oxd` receives them
  in the order the user made them." Chat tab: the tab is marked while hidden
  and its chat holds an error, a notice, or a waiting approval.
