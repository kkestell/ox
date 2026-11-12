# The app offers the checkout choices in Git workspaces and confirms discarding work in-window

- Task: `2026-11-12-006-app-session-checkout`
- Status: complete
- Plan: `.agents/plans/2026-11-12-006-app-session-checkout.md` (revision 4),
  blob `1209438ff63ee43896f7f3cdc78a4a3b8d4d59e7`
- Executed on: HEAD `633f1c3` (units 002–005 committed), branch
  `hypercube/session-2ea441ae`
- Review: `.agents/reviews/2026-11-12-006-app-session-checkout.md` (round 1)

## Decisions confirmed for this run

- Check-your-assumptions entry 3 is confirmed by the user (relayed by the
  parent, Hypercube session 2ea441ae): a delete that `oxd` answers with
  `NOT_FOUND` counts as done, and the app shows no error.
- The kept-branch notice is neutral: "Ox kept the branch `<name>`.".
- The Delete Session button is disabled while its delete is in flight.

## Changes

Design as in the plan; by path:

- `crates/ox/src/lib.rs`: `new_session` takes a `Checkout`;
  `workspace_branches(workspace_id, fetch)` is new; `close_session` became
  `delete_session(session_id, discard) -> Deletion`, whose `deletion` function
  maps `UNSAVED_WORK` to `unsaved_work`, `NOT_FOUND` to `deleted` with no
  kept branch, and other errors to strings. Unit test
  `tests::deletion_maps_the_answer` covers all cases.
- `ui/src/protocol.ts`: `Checkout`, `WorktreeInfo` (and
  `SessionInfo.worktree`), `WorkspaceInfo.git_working_tree`, `Branches`,
  `Deletion`.
- `ui/src/Dialog.tsx` (new): the shared `<dialog>`, `showModal()` on mount,
  Escape calls `onClose`.
- `ui/src/NewBranchDialog.tsx` (new): Branch / Start From with one shared
  `<datalist>` of local then remote branches; unfetched list, then fetched
  list; fetch error note; Start From disabled and blanked for a known branch;
  Create disabled while sending or while Branch is empty.
- `ui/src/SessionList.tsx`: per-agent `role="group"` with the three items
  (worktree items only when `git_working_tree`); New Worktree on Branch dialog
  state; Delete Session (label, title, disabled while in flight); the
  "Discard unsaved work?" and "Branch kept" dialogs; workspace remove title.
- `ui/src/Permission.tsx`, `ui/src/styles.css`: see Deviations (shared
  `.text-button`); layout rules for the menu group and label and the dialogs.
- `ui/e2e/wdio.conf.ts`: `CLAUDE_ACP_STORE_DIR` set to the run's directory.
- `ui/e2e/helpers.ts`: `gitWorkspace`, `gitWorkspaceWithRemote`,
  `startWorktreeSession`, `worktreeEntry`, `git`, `commit`.
- `ui/e2e/specs/worktree.e2e.ts` (new): ten tests (below).
- `ui/e2e/specs/session.e2e.ts`, `ui/e2e/specs/workspace.e2e.ts`: the Delete
  Session label.
- `docs/architecture.md`: the app paragraph describes the menu, the branch
  dialog, deleting, and the in-window dialogs. `README.md`: menu-started
  sessions in e2e run the bridge without a prompt.

Changed paths:

- `README.md`
- `crates/ox/src/lib.rs`
- `docs/architecture.md`
- `ui/e2e/helpers.ts`
- `ui/e2e/specs/session.e2e.ts`
- `ui/e2e/specs/workspace.e2e.ts`
- `ui/e2e/specs/worktree.e2e.ts` (new)
- `ui/e2e/wdio.conf.ts`
- `ui/src/Dialog.tsx` (new)
- `ui/src/NewBranchDialog.tsx` (new)
- `ui/src/Permission.tsx`
- `ui/src/SessionList.tsx`
- `ui/src/protocol.ts`
- `ui/src/styles.css`
- `.agents/plans/2026-11-12-006-app-session-checkout.md` (untracked input,
  unchanged)
- `.agents/work/2026-11-12-006-app-session-checkout.md` (this log)

## Deviations

- **Shared text-button class.** The dialogs' action buttons are the same kind
  of element as the permission options, so the existing
  `.permission .options button` rules became `.text-button`, which both use
  (`Permission.tsx` gains the class). No values changed. The plan said CSS is
  layout only; AGENTS.md requires a new element to take its peers' class.
- **`role="group"` keeps a Biome suppression.** Biome's
  `useSemanticElements` asks for a `<fieldset>`; the ARIA menu pattern groups
  menu items with `role="group"`, so the rule is suppressed with that reason.
- **`startWorktreeSession(workspace)`** takes the workspace ID rather than
  the cwd, because `oxctl new` needs it; `gitWorkspace*` return both.
- **One extra e2e test**: "disables its Delete button while the delete waits
  for the startup". The plan listed no test for the disabled button; a
  `post-checkout` hook that blocks on a FIFO holds the worktree startup so the
  delete stays in flight. The spec also closes any dialog a failed test left
  open (`afterEach`), so one failure does not cascade into the next tests.
- The fetch-error note reads "Could not fetch the remotes: <git's error>".

## Verification

Environment: macOS (Darwin 25.6.0), in this checkout; Rust toolchain per
`rust-toolchain.toml`; WebKit webview via the embedded WebDriver.

| Command | Result | Evidence |
| --- | --- | --- |
| `make check` | passed (fmt, clippy `-D warnings`, workspace tests, Biome, `tsc`, ruff) | `/tmp/ox-006-check.log` |
| `make e2e` | passed: 5 spec files, 28 tests (connection 1, model 3, session 12, workspace 2, worktree 10) | `/tmp/ox-006-e2e-final.log` |
| `make smoke-claude` | passed (`claude-haiku-5-5`), every check through `delete_and_idle_exit`, then `passed` | `/tmp/ox-006-smoke.log` |

Earlier failing runs during development (kept for the record): the first
`make e2e` failed the two delete tests because an earlier test left its dialog
open (`/tmp/ox-006-e2e-1.log`); the unsaved-work test then failed on the
session's `cwd` having a trailing `/` that git's message lacks
(`/tmp/ox-006-e2e-2.log`); the first in-flight test hung because `oxctl new`
waits for the startup the test held (`/tmp/ox-006-e2e-4.log`). All were test
fixes.

Each behaviour's test fails without it (behaviour undone, test run, behaviour
restored; the restored files were confirmed identical to the saved copies):

| Undone behaviour | Failing test | Evidence |
| --- | --- | --- |
| Worktree items hidden outside Git | offers only the existing checkout outside Git (3 items, expected 1) | `/tmp/ox-006-undo-1b.log` |
| New Worktree sends a worktree checkout | starts a session in a new worktree on a new branch | same |
| Second `workspace_branches` call with `fetch: true` | suggests the branches, and again once the remotes are fetched (`origin/late` missing) | same |
| Start From disabled for a known branch | takes a starting point only for a new branch | same |
| Unsaved-work dialog | asks before discarding unsaved work | same |
| Delete button disabled in flight | disables its Delete button while the delete waits for the startup | same |
| Kept-branch dialog | says which branch it kept | same |
| Fetch-error note | notes a failed fetch and keeps the branches it has | `/tmp/ox-006-undo-2.log` |
| `NOT_FOUND` → deleted | `cargo test -p ox deletion`: `left: Err("no session s")` | conversation tool result |

Batch 1 was first run without the dialog cleanup and hook release
(`/tmp/ox-006-undo-1.log`); its unsaved-work and in-flight failures were
confounded by leftovers, so it was rerun after those test fixes as
`/tmp/ox-006-undo-1b.log`, where each failure has its own cause.

## Session provenance

| Stage | Role/name | Coordinator ID | Harness | Model/effort | Native conversation ID |
| --- | --- | --- | --- | --- | --- |
| parent | xtask coordinator | `2ea441ae` | Claude Code (Hypercube) | claude-opus-5-5 | `57fda6b3-c0d1-4d32-a25b-993c5391c607` |
| plan, revision 4 | planner | `planner` | Claude Code (Hypercube child) | opus / high | `5ce61adf-7d6c-4b85-ab78-5167733b9e23` |
| work | xwork child `impl-006b` | `impl-006b` (parent `2ea441ae`) | Claude Code (Hypercube child) | claude-opus-5-5 / not exposed | `4bd33e45-bcf9-4aa0-8c11-e22930c04918` |
| review round 1 | review-006b | `review-006b` | Claude Code (Hypercube child) | claude-opus-5-5 | `d42bf093-ae08-4e2a-9548-fccf149a21e1` |

## Limitations

- Not covered by e2e, as the plan names: errors shown by the native
  `showError` (for example an invalid branch name; `oxd`'s tests cover the
  refusals), and a delete waiting on background cleanup after a restart
  (`oxd`'s cleanup test covers the daemon side; the `NOT_FOUND` mapping has
  its unit test).
- Not tested: answers arriving after the branch dialog closed are ignored
  (code path only); Escape closing a dialog; Create's disabled state while
  sending.
- Not inspected visually: no screenshot was taken, so the new menu label,
  dialogs and fields have not been compared on screen with their peers. Their
  look is the browser default apart from the shared `.text-button` and the
  layout rules.
- The New Worktree on Branch dialog stays open if its workspace is removed
  meanwhile; what Create does then is untested.
- Start From's "known branch" test treats the text before the first `/` of a
  remote entry as the remote name, so a remote whose name contains `/` is not
  matched.
- The menu-created sessions in e2e run the real `claude-code-acp` bridge
  without a prompt; per the bridge's README that starts no `claude` process,
  which was not checked during the run.
- Unrelated observations: a worktree session's `cwd` ends in `/` (from unit
  003), which the e2e helper strips. Five `oxd --idle-exit-ms 1000` processes
  from this checkout's `target/debug`, started at 10:16–10:17 before this
  run, were still running on temporary sockets; they were left alone.

## Parent verification

- Verifier: parent coordinator, Hypercube session 2ea441ae, native
  conversation `57fda6b3-c0d1-4d32-a25b-993c5391c607`.
- Review round 1: accept, no blocking or plan findings. No code changed after
  the review.
- Checks on the final tree, the task's final state: `make check`,
  `make e2e` (28 tests) and `make smoke-claude` passed
  (`/tmp/ox-006-check.log`, `/tmp/ox-006-e2e-final.log`,
  `/tmp/ox-006-smoke.log`).
- Notes from review, not fixed: the menu and dialogs were not looked at on
  screen (the group label may read as an item; dialog chrome is WebKit
  default); `NewBranchDialog` holds `fetch_error` twice; two deletes answering
  together show only the later dialog; cancelling the branch dialog while
  Create is in flight still selects the session; remote names containing `/`
  are not matched.

## Revisions with the user after review

The user reviewed the app in `make dev` and asked for changes, which the
parent made:

- The New Session menu opens beside its button, level with its top, and
  starts with an Agent select in place of a group label per agent.
- The dialog shares the menu's surface (border, radius, card background,
  shadow), is centred with `height: auto` (WebKit otherwise gave the modal a
  height near the window's), and its inputs take a `text-field` class that
  shares its box with `.text-button`.
- `docs/architecture.md` describes the agent select; the e2e check for the
  group label checks the select.

Checks after these changes: `make check-ui` passed (`/tmp/ox-ui-check.log`),
`make e2e` passed, 5 spec files, 28 tests (`/tmp/ox-ui-e2e.log`). The menu and
dialog were inspected in screenshots taken through the WebDriver harness.
