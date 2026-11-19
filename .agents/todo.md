# Todo

Gaps in features Ox already supports, checked against the source at `7772f7c`.
Ordered by priority.

## Check first

- [ ] **Plan mode can change without Ox knowing.** Ox never reads a permission
  mode change from Claude's output. Check live with Haiku: start a session in
  `plan`, approve `ExitPlanMode`, and see whether the next approval request or
  the `system` `init`/status lines show a different mode. Repeat for
  `EnterPlanMode` from `manual`. If approving `ExitPlanMode` leaves plan mode,
  the page shows "Plan" while Claude edits files, which is above the fix
  threshold (permissions other than shown). A resumed process returns to the
  stored mode (`docs/claude-code.md:33`), so the mismatch lasts until the
  process parks.

## Prompting

- [ ] **Stop a running turn.** Needs a new method (for example
  `session/interrupt`) that sends Claude's `interrupt` control request
  (`docs/claude-code.md` §2, "Interrupt" in §4), plus a Stop button that
  replaces Send while the session is running. Today a turn that asks for no
  approval can be stopped only by deleting the session.
- [x] **Composer follows session state.** Disable Send while the session is
  `running` or `requires_action`, so it stops returning "Session is busy".
  Enter sends and Shift+Enter adds a newline, in both `SessionView` and
  `NewSessionView`.
- [ ] **First prompt failure leaves a session behind.** `startSession`
  (`cmd/ox/web/src/ox.tsx`) creates the session, then sends. If the send fails
  (for example a startup failure), the draft view shows the error and stays
  open, and the created session sits in the sidebar with no prompt. Sending
  again creates a second one. Select the created session once it exists, and
  show the send error there.
- [ ] **Transcript stays pinned to the bottom** while output streams in,
  unless the user has scrolled up.
- [ ] **Render replies as Markdown.** Replies and the `ExitPlanMode` plan show
  as plain text.

## Approvals

- [ ] **Answer `AskUserQuestion`.** Show the questions and options as a form
  and allow with `updatedInput` holding the original input plus `answers`
  (`docs/claude-code.md` §5). Today Allow echoes the input with no answers.
- [x] **Deny with a message and stop.** Let the user type a deny `message`,
  and offer deny with `interrupt: true`.
- [ ] **Allow always.** Offer it when `permission_suggestions` is non-empty
  and `suppress_always_allow_rule` is false, sending the suggestions as
  `updatedPermissions`. The suggestions usually target `localSettings`, which
  writes `.claude/settings.local.json` in the workspace; decide whether that is
  acceptable before building it.
- [x] **Show `ExitPlanMode` as its plan**, not raw JSON.

## Sessions and workspaces

- [ ] **Remove a workspace.** Needs a new method and a store delete; neither
  exists. Decide what happens to its sessions (destroy them, or refuse while
  any exist).
- [ ] **Keep the selected session in the URL**, so a reload keeps it.
- [x] **Exit errors read "Claude exited: Claude exited: …"** (audit F10). The
  stored error already has the prefix; drop the one the UI adds
  (`SessionView.tsx`, `Transcript`).
- [ ] **Startup failures lose Claude's reason** (`docs/known-issues.md`).
  The same path will hide unknown-model and not-logged-in errors, which a new
  user meets first. Read the error `result` printed before exit, or the stderr
  tail, into the `session/send` error.

## Audit leftovers (`.agents/reviews/2026-11-18-001-deslop.md`)

- [ ] F3: `docs/architecture.md:126-127` still describes `index.html` as a Go
  template with a default workspace. `ox` serves the build as static files.
- [ ] F6, F8, F9: still present, and none has an entry in
  `docs/known-issues.md`. F8 holds: `make test` runs `lint`, not `web`, so
  `cmd/ox` fails setup without `web/dist`.
- [x] F7: the live failed-turn check passes without confirming the
  interrupting deny was sent. Fail unless `denyBash` denied Bash.
- [ ] F11: `docs/architecture.md:10` (server imports `config`), `:61`
  (`Server.mu` also guards `closed`), `:100-101` (exit ends `queued` and
  `prompting`), `:136` (`reload` writes the store); `docs/protocol.md`
  marks `effort` required at creation though it may be omitted;
  `docs/claude-code.md:3` gives 2.1.326 as the latest version observed while
  later lines cite 2.1.327.
- [x] F12: the `img, picture, …` and heading selectors in `style.css` match
  nothing. The `details.error` part no longer applies: tool calls use
  `.marker` now.
