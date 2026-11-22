# Fix the Codex contributions audit findings F1–F10

## Goal

Close findings F1–F10 of
[audit 2026-11-22-002](../audits/2026-11-22-002-codex-contributions.md), and
remove their ten entries from `docs/todo.md`. Each finding is small, and
together they land as one commit:

- [F1](../audits/2026-11-22-002-codex-contributions.md#f1-a-compatibility-fallback-for-sender-labels):
  the page treats `from_label` as optional and keeps a fallback, tested by
  editing SQLite, for databases older than the field.
- [F2](../audits/2026-11-22-002-codex-contributions.md#f2-draft-recovery-is-decided-in-two-places)
  and
  [F7](../audits/2026-11-22-002-codex-contributions.md#f7-the-new-session-view-decides-when-it-can-create):
  `App.tsx` and `control.create` each decide when a draft recovers instead of
  sending, and `NewSessionView` and `control.create` each decide when a draft
  can be submitted. The `existing: true` path has no test.
- [F3](../audits/2026-11-22-002-codex-contributions.md#f3-two-oxd-guards-have-no-test):
  the socket directory lock in `listen` and the `session_id` format check in
  `session/create` have no test.
- [F4](../audits/2026-11-22-002-codex-contributions.md#f4-ur-copies-an-unread-model-into-every-reply-fragment),
  [F5](../audits/2026-11-22-002-codex-contributions.md#f5-ur-decides-where-a-reply-starts-in-two-places),
  and
  [F6](../audits/2026-11-22-002-codex-contributions.md#f6-a-stop-assertion-that-cannot-fail):
  Ur writes an unread model on every fragment and pins it with a file-format
  test, finds a reply's extent in two places, and keeps a `Stop` assertion that
  cannot fail.
- [F8](../audits/2026-11-22-002-codex-contributions.md#f8-one-workspace-test-checks-four-behaviors-under-one-name):
  one workspace spec checks four behaviors under the malformed-link name.
- [F9](../audits/2026-11-22-002-codex-contributions.md#f9-a-stack-test-timeout-left-without-an-outcome):
  the stack test "one terminal Ctrl-C signals each service once" timed out
  under `make test` and got no outcome.
- [F10](../audits/2026-11-22-002-codex-contributions.md#f10-the-audit-skill-restates-the-workflow):
  `/ox-audit` restates `AGENTS.md`'s workflow handoff.

## Related code

- `cmd/ox/web/src/protocol.ts:33-37` — `Prompt`, where `from_label` is
  optional.
- `cmd/ox/web/src/SessionView.tsx:131-144` — `PromptBubble` and its
  `senderLabel` fallback.
- `internal/server/mcp.go:291,395,587-590` — every message `oxd` builds carries
  the sender's label at send time, never empty (`methods_test.go:1501` checks
  the unlabeled case, `cs[:8]`).
- `cmd/ox/web/src/ox.tsx:144-310` — `createSessionControl`: `creating`, `free`,
  `canSend`, `recover`, and `create`, whose reply handler recovers on
  `existing: true` (`:270`).
- `cmd/ox/web/src/App.tsx:112-125` — the effect that recovers a draft whose
  create failed once its session is live and the draft or session is shown.
- `cmd/ox/web/src/NewSessionView.tsx:67,76,83` — three controls disabled on
  `creating() || !ox.ready()`.
- `internal/server/methods.go:306-334` — `session/create`'s `session_id` check
  and its `existing: true` reply.
- `cmd/oxd/main.go:180-222` — `listen` and its directory `flock`.
- `internal/ur/ur.go:105-110,203-226,253-280` — `entry`, `Handle`, `request`,
  and `replyText`.
- `cmd/ox/web/e2e/session.spec.ts:827-870` — `cutReply`, `armCutReply`, and
  the lost-creation-reply test, whose setup the new recovery test reuses.
- `cmd/ox/web/e2e/workspace.spec.ts:9-45` — the four-behavior test.
- `cmd/ox/web/stack.mjs`, `cmd/ox/web/stack.test.mjs:160-180` — `runStack` and
  `signalStack`.
- `.claude/skills/ox-audit/SKILL.md:16,42` — the restated handoff and step 11.

## Decisions

- **F1, sender labels.** `oxd` sets `from_label` on every message it builds, so
  the page's type requires it with `from` and `PromptBubble` renders it
  directly. The fallback, its test, the `database` field of `Services`, and the
  protocol sentence about older prompts go. Ox's test state is disposable, so a
  database from before `from_label` is not supported.
- **F2, one recovery rule.** The session control owns the create's lifecycle
  and decides recovery in one place. Its `creating` boolean becomes one signal,
  `creation`, of type `"none" | "creating" | "unconfirmed"`. `"unconfirmed"`
  means a create was sent and `oxd` did not confirm it as new: its request
  failed, or `oxd` answered `existing: true`. The control derives
  `recoverable()` as `creation() === "unconfirmed"` and its session live. That
  is the only rule; `create` no longer calls `recover` or `onCreated` for
  `existing: true`, it sets `"unconfirmed"` and returns. `App.tsx`'s effect
  keeps navigation, which it owns: it finds a shown draft whose control is
  `recoverable()`, and calls `recover()` and `finish`. `recover()` sets
  `creation` back to `"none"`, so the effect fires once. A create that `oxd`
  refused, such as an invalid model, is also `"unconfirmed"`; its session never
  becomes live, so it never recovers, and the user submits again.
- **F7, one submit rule.** The control exposes `canCreate`, which is `free()`:
  connected, with no create, send, or configuration in flight. A draft has
  only its create in flight, so this matches today's
  `creating() || !ready()`. `create` checks `canCreate()` and the blank prompt,
  and `NewSessionView`'s Send, model picker, and permission mode select read
  `canCreate()`.
- **F3, guard tests.** The lock test plays a second starter: it holds the socket
  directory's `flock` while `listen` runs in a goroutine, checks that `listen`
  has not returned, binds a live listener at the path in place of the stale
  socket, and releases the lock. `listen` must then refuse with "another oxd is
  serving". Without the lock, `listen` returns at once with the replaced socket.
  The format check gets two `session/create` rows in `TestSettingsValidation`,
  checked by message, since every row there already fails for its missing
  workspace: 32 non-hexadecimal characters, and 30 hexadecimal digits.
- **F4, no model in Ur's file.** Ur's conversation file holds `message` values:
  `entry` and its `Model` go, and `history` becomes `[]message`. Files written
  before keep a `model` key, which decoding ignores. `TestResume` loses its
  file-line block, its check that no request message carries a model (a
  `message` has no such field), and the `Configure` step that fed only the
  file check.
- **F5, one reply rule.** One function, `conversation(history []message)
  []message`, joins each run of assistant entries into one message. `request`
  sends its result, and `Handle(ended)` takes the last message's content as the
  result text when its role is `assistant`, and `""` otherwise. `replyText`
  folds into it.
- **F6.** The `Stop` assertion in `TestStopDuringTurn` goes. The rig keeps its
  `errors` field, which `TestResumeLost` reads.
- **F8.** The test splits into four, each starting its own services as the file
  does now: the malformed link, Send held until the session list loads, a
  draft's permission mode through a relay restart, and a model without efforts
  sending no `effort`. Only the last needs the effortless configuration.
- **F9, the test's shell drops SIGINT.** The cause is the test's own service
  script, not `runStack`. macOS `/bin/sh` (bash 3.2) sometimes drops a SIGINT
  sent to the shell alone while it waits on a foreground `sleep`, so the
  trap never runs and the shell keeps looping. `signalStack` then times out
  in its `!alive` wait (`stack.test.mjs:166`). A probe of the script body
  lost 4 of 18,000 SIGINTs, and none of 6,000 SIGTERMs. A shell that lost one
  was still looping five seconds later, with no log written, and exited on a
  second SIGINT. A terminal Ctrl-C also signals the `sleep`, and the real
  services are Go and Node binaries, so `stop(130, null)` in `stack.mjs` is
  correct. The fix waits on the `sleep` with the `wait` builtin, which a
  trapped signal interrupts at once: `while :; do sleep 0.05 & wait $!; done`.
  The same probe under the same load lost none of 30,000 SIGINTs, and the
  patched file passed 20 of 20 runs. The failure rate, about one in 5,000
  signals, is too low for a test that fails without the fix. The stack test
  itself is the check.
- **F10.** The skill keeps its link to the project workflow and drops the
  handoff it restates. Step 11 says what a `Plan` finding contains, without
  naming who receives it.
- **Requirements:** Creating a session, Lost creation reply, Failed creation,
  Creation keeps the user's view, and Only the user sends hold through both
  recovery paths. A create whose reply is lost leaves the draft `"unconfirmed"`
  with its error; a reconnect lists the session, and once its snapshot loads
  while the draft or the session is shown, the page opens it with the prompt
  unsent and the recovery notice. A retried create answered `existing: true`
  sends nothing and opens the session the same way once it loads. A second
  page has its own draft ID and cannot reach either path for this one.
  Messages in the transcript holds: every recorded message carries its label.
- **Construction:** a draft's create state is one `creation` value, so
  "creating" and "unconfirmed" cannot both hold, and recovery is decided only
  by `recoverable()`. Submission is decided only by `canCreate()`. Ur's reply
  extent is decided only by `conversation`. `from_label` is present whenever
  `from` is, by type.
- **Ownership:** `creation` replaces `creating` in the session control, which
  already owns the create request. It adds no timer or retry. It is cleared by
  `recover()`, and the control is released with its draft as today.

## Check your assumptions

None.

## Test plan

- A retried create answered `existing: true` sends nothing and recovers the
  session with the prompt in the composer: e2e, a new test in
  `session.spec.ts`, "a retried creation recovers the session without
  sending", beside the lost-reply test and reusing `cutReply`. It records the
  draft's `session_id` from the `session/create` post, holds every `session/get`
  for that ID through its own `page.route`, cuts the create reply, restarts the
  relay so the create fails, and submits again. Once the second
  `session/create` reply arrives, it releases the held `session/get`s, and
  expects the recovery notice, the prompt still in the composer, no `.prompt`
  in the transcript, and no `session/send` post. No existing test reaches
  `existing: true`. It fails if `create` sends on `existing: true`.
- The lost-reply path still recovers through `recoverable()`: the existing
  "a lost creation reply is recovered after leaving the draft".
- A message shows its send-time label: the existing "all waiting inputs show in
  order with their send-time labels". The legacy-label test is deleted.
- Draft submission is held while disconnected or creating: the existing
  workspace tests split out under F8, and "a rejected creation keeps the draft
  and its settings after switching views".
- A second starter waits for one replacing the socket: a new
  `TestListenWaitsForAnotherStarter` in `cmd/oxd/main_test.go`, sharing a
  `staleSocket` helper extracted from `TestListenReplacesOnlyAStaleSocket`. It
  fails without the `flock`.
- A malformed `session_id` is refused: two rows in `TestSettingsValidation`,
  each failing without its half of the check.
- Ur's request and result after the `conversation` change: the existing
  `TestTwoTurns`, `TestResume` (contents and roles of the resumed request), and
  `TestResumeDuringTurn`.

## Implementation plan

1. F1: in `protocol.ts`, make the message branch of `Prompt`
   `{ from: string; from_label: string }`. In `SessionView.tsx`, delete
   `senderLabel` and the `useOx` call it needs, and render
   `props.input.from_label` in the `.from` element when `from` is set. Delete
   "a recorded message without a saved label still shows its sender" and the
   `node:sqlite` import from `session.spec.ts`, and the `database` field from
   `Services` in `services.ts`, keeping the local `database` path `oxd` is
   started with.
2. F2 and F7: in `ox.tsx`, replace `creating` with `creation` as decided, with
   `free()` reading `creation() !== "creating"`, drop `creating` from the
   control's returned object, and add `canCreate` and `recoverable` to it. `create` returns unless
   `canCreate()` and the prompt is not blank; its reply handler posts and calls
   `onCreated` for a new session, and sets `"unconfirmed"` for
   `existing: true`; a failed request sets `"unconfirmed"`. `recover` also sets
   `"none"`. Update the comments on `create` and `recover`. In `App.tsx`, the
   effect's condition becomes `d.control.recoverable()` with the shown-draft
   check kept, and its comment names both ways a create goes unconfirmed. In
   `NewSessionView.tsx`, the three `disabled` attributes read
   `!draft.control.canCreate()`.
3. Add the F2 e2e test to `session.spec.ts`.
4. F3: in `cmd/oxd/main_test.go`, extract `staleSocket(t, path)` and add
   `TestListenWaitsForAnotherStarter`. In `internal/server/methods_test.go`,
   add the two malformed-`session_id` rows to `TestSettingsValidation` and check
   their message, "session_id must be 32 hexadecimal digits".
5. F4 and F5: in `internal/ur/ur.go`, delete `entry`, store `[]message` in
   `history`, and update `load`, `save`, and the `delta` case. Add
   `conversation`, use it in `request` and in `Handle(ended)`, and delete
   `replyText`. In `ur_test.go`, cut `TestResume` as decided and drop imports
   it no longer needs.
6. F6: delete the `r.errors` assertion in `TestStopDuringTurn`.
7. F8: in `workspace.spec.ts`, replace the first test with four:
   - "a malformed session link still lists workspaces": open `/#%`, add `/`,
     and see it in the sidebar.
   - "a draft holds Send until the session list loads": hold `workspace/list`,
     open the page, add a workspace, open its draft and fill the prompt, expect
     Send disabled, release, expect it enabled.
   - "a draft keeps its permission mode through a relay restart": choose
     `plan`, restart the relay, and expect `plan` and Send enabled.
   - "a model without efforts creates a session without effort": with the
     effortless configuration, send a draft and check the `session/create`
     post has no `effort` and the reply arrives.
8. F9: in `stack.test.mjs`'s `script`, change the trap branch's loop to
   `while :; do sleep 0.05 & wait $!; done`, and say in its comment that the
   shell waits with `wait` because a trapped signal interrupts `wait` at once,
   while `sh` can drop a SIGINT that arrives during a foreground command.
9. F10: in `.claude/skills/ox-audit/SKILL.md`, replace line 16 with "The audit
   recommends which finding to take up first. The user chooses which findings
   go through the [project workflow](../../../AGENTS.md#workflow)." Replace
   step 11's first sentence with "Make each `Plan` finding complete enough to
   plan from:" followed by the existing list of what it includes.
10. Remove the ten F1–F10 entries of this audit from `docs/todo.md`.

## Requirement changes

None.

## Documentation updates

- `docs/protocol.md`: delete "Older recorded prompts can omit `from_label`."
- `docs/todo.md`: remove the entries for F1–F10.
