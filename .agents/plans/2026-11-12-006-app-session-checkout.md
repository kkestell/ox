# The app offers the checkout choices in Git workspaces and confirms discarding work in-window

- Task: `2026-11-12-006-app-session-checkout` (numbered 005 before the
  renumbering)
- Status: ready (revision 4; see **Revision history** at the end). Revision 3 absorbed the former shared design's
  settled menu entry and its final-state checks, and replaced its links with
  the contracts of the units below. The design was then deleted.
- Source HEAD: planned at `e7fdf22c675479f9f2fa328afe285c173ca99618`; executes
  on the commit of `2026-11-12-005-remove-session-worktrees`
- Author: planner child of Hypercube session 2ea441ae, Claude Code, Opus 5.5
  (claude-opus-5-5); coordinator ID `planner`; native conversation ID
  `5ce61adf-7d6c-4b85-ab78-5167733b9e23`
- Dependencies: `2026-11-12-003-create-session-worktrees`,
  `2026-11-12-004-workspace-branches`, and
  `2026-11-12-005-remove-session-worktrees`. `2026-11-12-002-xdg-data-dir` is
  committed context.

## Outcome and boundaries

This is the app side of the session checkout request recorded in
`2026-11-12-003-create-session-worktrees`, together with correction I
(in-window dialogs).

In a Git workspace, the New Session menu lists **Existing Checkout**, **New
Worktree** and **New Worktree on Branch…** under each agent. Outside Git it
lists only **Existing Checkout**. **New Worktree on Branch…** opens a dialog
with **Branch** and **Start From**. Branch suggestions appear at once and
update when a background fetch finishes; a failed fetch shows a note.

**Delete Session** asks in-window before discarding unsaved work, and tells the
user in-window when a branch was kept. One shared dialog component serves all
three dialogs. Error messages stay on the native `showError`. CSS is layout
only (entry 1).

Not in this unit:

- daemon or protocol changes;
- a sidebar marker for worktree sessions;
- remembering the choice.

## Contracts relied on

From `2026-11-12-003-create-session-worktrees`:

- `session.new` takes `checkout`: `{type: "workspace"}` or `{type:
  "worktree", branch: string | null, start: string | null}`.
- `branch: null` gives a new `ox/<id8>` branch from `start` (default `HEAD`).
- A named branch is resolved in this order:
  - an existing local branch is checked out, and is refused if it is checked
    out elsewhere;
  - `<remote>/<name>` typed, or a name on exactly one remote, gives a local
    tracking branch;
  - a name on several remotes uses `checkout.defaultRemote`, else it is
    refused;
  - any other name creates a new branch.
- `start` is refused for anything but a new branch.
- Refusals in validation answer at once with `INVALID_REQUEST` and create
  nothing. Otherwise the answer is the session in `starting`. A later git
  failure ends it `exited` with git's error.
- `SessionInfo.worktree` is `{path, branch, base, created_branch}`, and `cwd`
  lies under `<socket dir>/worktrees/`.

From `2026-11-12-004-workspace-branches`:

- `workspace.branches { workspace_id, fetch, env }` answers `{local, remote,
  fetch_error}`.
- With `fetch: true`, it waits for a coalesced `git fetch --all` (at most 60
  seconds), and a failure arrives in `fetch_error` beside the unchanged lists.
- `WorkspaceInfo.git_working_tree` says whether to offer worktree items.

From `2026-11-12-005-remove-session-worktrees`:

- `session.delete { session_id, discard, env }` answers `{kept_branch}`.
- It refuses with `code::UNSAVED_WORK` (7), with the unsaved items in the
  message, unless `discard`.
- It never deletes a branch Ox did not create, and reports a kept branch by
  name. A branch is reported as kept when it has commits no other ref has, and
  also when the repository was deleted or moved, because Ox could not check it.
- A delete can wait for seconds. It waits for a session's startup, or for the
  background cleanup that `oxd` runs after `ready`.
- A delete answers `NOT_FOUND` when the session is already gone, for example
  because that cleanup removed it while the delete waited.
- Deleting a worktree session records `agent_exited` (with no error) before
  `session_closed`, because `oxd` stops the agent before removing the
  worktree.
- After a daemon restart, sessions that never started and await cleanup are
  listed `exited`, with no `agent_session_id`, until the cleanup removes them
  or leaves them for deletion.
- Git runs with the `env` each request carries.

## Current system

- `crates/ox/src/lib.rs`:
  - `new_session(agent, workspace_id)` passes `Checkout::Workspace` (003);
  - `close_session(session_id)` calls `Client::delete(.., false, utf8_env())`
    (005);
  - errors are `String`s;
  - `utf8_env()` builds the environment the app passes.
- `ui/src/SessionList.tsx`:
  - a `popover="auto"` `.menu` of `role="menuitem"` buttons per agent;
  - `run` with `showError`;
  - the close button;
  - the workspace remove button, whose title mentions closing sessions.
- `ui/src/protocol.ts` mirrors the wire types. `ui/src/styles.css` has
  `.menu`, `.menu button` and `--menu-item-padding`.
- `ui/e2e`:
  - `helpers.ts` has `startSession`, `resetDaemon` (which uses `oxctl delete
    --discard` after 005), and `sessionEntry` (matched by `title` = cwd);
  - `wdio.conf.ts` sets `OX_E2E_DIR` and `OX_SOCKET`, so worktrees land in
    `$OX_E2E_DIR/worktrees/`.

## Design

### Tauri commands

`new_session(agent, workspace_id, checkout)`. `close_session` becomes
`delete_session(session_id, discard) -> Deletion`:

```rust
#[derive(Serialize)]
#[serde(tag = "type", rename_all = "snake_case")]
enum Deletion { Deleted { kept_branch: Option<String> }, UnsavedWork { message: String } }
```

Errors from `oxd` map as follows:

- A `Remote` error with `code::UNSAVED_WORK` becomes `UnsavedWork`.
- A `Remote` error with `code::NOT_FOUND` becomes `Deleted { kept_branch: None
  }`, because the session is gone either way.
- Other errors stay `String`s.

The mapping is a small function with a unit test covering all three cases.

`workspace_branches(workspace_id, fetch) -> Branches`. Every command that
reaches git passes `utf8_env()`.

### `ui/src/Dialog.tsx`, the shared component

A `<dialog>` opened with `showModal()` on mount, with a heading from `title`,
`children` for the body, and an `actions` slot of buttons. Escape (the `cancel`
event) calls `onClose`. The three dialogs are its only users.

### Menu

Per agent, a `role="group"` labelled with the agent's name, holding the three
items. The worktree items render only when `workspace.git_working_tree` is
true. **New Worktree** sends `{type: "worktree", branch: null, start: null}`.

### `ui/src/NewBranchDialog.tsx`, built on `Dialog`

- **Branch** is required. **Start From** has `placeholder="HEAD"` and a
  `<datalist>` of local branches, then remote ones.
- On open, it calls `workspace_branches` with `fetch: false` and shows the
  result. It then calls it with `fetch: true` and replaces the suggestions
  when that answers.
- A `fetch_error` shows as a note in the dialog, and the suggestions stay as
  they were. Answers that arrive after the dialog closed are ignored.
- **Start From** is disabled, and its value cleared, while **Branch** equals a
  local branch, a remote entry, or `<remote>/<Branch>` for a listed remote
  entry, because `oxd` refuses Start From for those.
- **Create** sends `{type: "worktree", branch, start: start || null}` and stays
  disabled while sending. On success it closes the dialog and selects the
  session. On error it calls `showError` and keeps the dialog open with the
  fields as typed.

### Delete

- The button becomes **Delete Session** (title and `aria-label`). The workspace
  remove button's disabled title becomes "Delete the workspace's sessions to
  remove it".
- The delete action calls `delete_session(discard: false)`. The session's
  **Delete Session** button is disabled while its delete is in flight, since a
  delete can wait on startup or cleanup.
- On `unsaved_work`, it opens a `Dialog` titled "Discard unsaved work?", whose
  body is the message as text, with **Cancel** and **Discard and Delete**.
  Confirming calls again with `discard: true`.
- A `deleted` answer with `kept_branch` opens a `Dialog` titled "Branch kept",
  with the neutral body "Ox kept the branch `<name>`." and an **OK** button.
  The body gives no reason, so it is true both for an unmerged branch and for
  a repository Ox could not reach.
- The outcome of a delete comes only from the `delete_session` answer. The
  `agent_exited` without an error that precedes `session_closed` keeps the
  conversation's existing neutral "The agent exited" line and is never shown
  as a failure.
- A session awaiting background cleanup is an `exited` session with no
  `agent_session_id`. Selecting it shows it without loading, as for any
  session that never started, and deleting it waits as described above.

### CSS

Layout only, with one rule per element kind:

- The group label uses `--menu-item-padding`, so it lines up with the items.
- Dialog content is a one-column grid with a `--spacing` multiple as the gap,
  and the actions row is right-aligned.
- Message bodies use `white-space: pre-wrap`, since git's messages span lines.

## Check your assumptions

Entries 1–2 are settled; entry 3 is new in revision 4. Entry 1 was numbered 5 among the entries for the
whole request. Entry 2 was this unit's own, numbered 005-1 before the
renumbering.

1. **Confirmed.** **The menu shows three items per agent, the choice is not
   remembered, there is no sidebar marker, and CSS is layout only.** The
   worktree items are hidden outside Git (correction F).
2. **Confirmed.** **Start From is disabled whenever Branch matches a suggested
   branch**, local or remote, because only new branches take a starting point.
   A branch the suggestions do not know yet (before the fetch finishes) is
   refused by `oxd` if Start From is filled.
3. **Deleting a session that is already gone counts as done.** The app shows
   no error when `oxd` answers `NOT_FOUND`, for example when background cleanup
   removed the session while the delete waited, or another client deleted it.
   Alternative: show "no session …", which reports a failure for a delete that
   got what it asked for.

## Implementation sequence

1. `crates/ox/src/lib.rs`: the commands in **Tauri commands**, registered in
   `generate_handler!`, and the mapping's unit test.
2. `ui/src/protocol.ts`: `Checkout`, `WorktreeInfo`,
   `WorkspaceInfo.git_working_tree`, `Branches` and `Deletion`.
3. `ui/src/Dialog.tsx`, `ui/src/NewBranchDialog.tsx`, then `SessionList.tsx`:
   the menu, the dialog state (which workspace and agent), and the delete flow
   with its two dialogs.
4. `ui/src/styles.css`: the layout rules.
5. e2e support:
   - In `wdio.conf.ts`, set `CLAUDE_ACP_STORE_DIR` to `OX_E2E_DIR`, since the
     app's sessions run the real bridge, which starts without Claude until a
     prompt.
   - In `helpers.ts`, add `gitWorkspace()` (a realpath'd tempdir with `git init
     -b main`, one commit with an inline identity, and `oxctl workspace add`);
     `gitWorkspaceWithRemote()` (a clone of a bare remote with branch
     `feature`); `startWorktreeSession(cwd)` (`oxctl new --worktree --
     oxd-fake-agent`); `worktreeEntry()`, matching a `title` under
     `$OX_E2E_DIR/worktrees/`; and a `git` helper.
   - Update the existing specs' `aria-label="Close Session"` to `Delete
     Session`.

## Verification

New `ui/e2e/specs/worktree.e2e.ts`; the existing specs pass with the label
change.

- **Menu**: a plain-directory workspace's menu has only **Existing Checkout**.
  A Git workspace's menu has all three items.
- **New Worktree**: an entry appears whose `title` is under
  `$OX_E2E_DIR/worktrees/`, and the workspace's `git branch --list 'ox/*'` has
  one branch.
- **New Worktree on Branch…**:
  - the dialog shows `origin/feature` among the suggestions;
  - after the test pushes `late` to the remote and reopens the dialog,
    `origin/late` appears once the fetch answers;
  - typing `feature` disables Start From;
  - creating `topic` from `main` gives an entry whose worktree is on `topic`.
- **Fetch failure**: with the remote's URL set to a missing path, the dialog
  shows the note and the local suggestions.
- **Unsaved work**: a worktree session started with `startWorktreeSession` has
  an untracked file. **Delete Session** opens "Discard unsaved work?", with the
  file's worktree in the text. **Cancel** leaves the entry. Deleting again and
  choosing **Discard and Delete** removes the entry and the directory.
- **Kept branch**: after a commit in the worktree, **Delete Session** shows
  "Branch kept" with the text "Ox kept the branch `<name>`.". **OK** closes it,
  and the branch still exists. The entry leaves the list, and no error dialog
  is involved (the `agent_exited` before `session_closed` is not treated as a
  failure).
- **Not covered by e2e**, which the work log names:
  - errors shown by the native `showError`, such as an invalid branch name;
    `oxd`'s tests cover the refusals;
  - a delete waiting on background cleanup, which would need the app's own
    `oxd` started with a blocked git. `oxd`'s cleanup-after-`ready` test covers
    the daemon side, and the `NOT_FOUND` mapping has its unit test.

Commands: `make check`, then `make e2e`. As the last unit of the request, also
run `make smoke-claude` on the final state, and report all three results.

## Handoff

Ready once 005 is committed. Size: one normal UI assignment, made of one shared
dialog component, three uses of it, a menu change, the Tauri contract and their
e2e specs. Detail: the contracts this unit relies on are stated above, with the
units that define them. `plan_metrics.py`: 1,564 words (21 fenced), just above
the band, because of those stated contracts. The work assigned did not grow. At revision 4 it is 2,067 words. The added length is 005's revision-4
contracts and the small delete-flow additions that follow from them.

## Revision history

- **Revision 4 (2026-11-12).** This revision follows the user's acceptance of
  `2026-11-12-005-remove-session-worktrees` revision 4, entries 6–8, with the
  change to entry 6.
  - The kept-branch notice is neutral: "Ox kept the branch `<name>`.".
  - The contracts from 005 add a delete that waits on startup or background
    cleanup, `NOT_FOUND` for an already-gone session, `agent_exited` before
    `session_closed` on a worktree delete, and sessions listed `exited` while
    they await cleanup.
  - The Delete button is disabled while its delete is in flight.
  - `NOT_FOUND` maps to a successful delete (entry 3).
