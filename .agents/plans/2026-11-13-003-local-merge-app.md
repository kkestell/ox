# Local merge in the app

## Goal

The app runs the local merge workflow of [git-workflows](../specs/git-workflows.md) on the `oxd` primitives the Daemon and CLI item added (`branch.move`, comparison refs and `SessionInfo.comparison`):

- The workspace's **+** menu holds the agent picker, **New Task** (Git workspaces only) and **Session in Checkout**. New Task starts a worktree session on `ox/<id8>` from `HEAD`, compared with the branch the workspace's checkout is on, and refuses with "<workspace> is not on a branch" when the checkout has none.
- A local merge session's row shows its git status as an icon at the row's right end, under the row actions.
- The conversation header has **Merge into main**, enabled when the branch is one commit ahead of the ref and not behind it. It fast-forwards the ref's branch with `branch.move` and then offers to delete the session.
- The existing-branch and new-branch dialogs and `useBranches.ts` go.

`oxd` gains one fact for this: `workspace.branches` reports the branch the workspace's checkout is on.

## Related code

- `crates/oxd/src/branches.rs` — `list` (line 60) runs `for-each-ref` in the workspace directory; `Branches` is assembled in `Daemon::branches` (`crates/oxd/src/daemon.rs`, line 509).
- `crates/ox-protocol/src/messages.rs` — `Branches` (line 351) and its serialization test (line 815); `Checkout::Worktree.compare`, `WorktreeInfo.compare`, `Comparison` and `SessionInfo.comparison` from the previous item.
- `crates/oxd/tests/worktrees.rs` — `branches` helper (line 431) and the listing tests at lines 1258–1300.
- `crates/ox/src/lib.rs` — Tauri commands: `workspace_branches` (line 176), `new_session` (line 189), `delete_session`, the `generate_handler!` list.
- `ui/src/SessionList.tsx` — the **+** popover menu, `newSession`, the delete, archive and unarchive flows with the unsaved-work and kept-branch dialogs, `sessionRow`.
- `ui/src/Workbench.tsx` — the content header (`pane-header content-header`) holding the panel toggles.
- `ui/src/App.tsx` — renders `Workbench`, `SessionList`, `Conversation`, `Composer`; knows the viewed `session`.
- `ui/src/protocol.ts` — `Checkout`, `WorktreeBranch`, `WorktreeInfo`, `Branches`, `LocalBranch`, `SessionInfo`.
- `ui/src/ExistingBranchDialog.tsx`, `ui/src/NewBranchDialog.tsx`, `ui/src/useBranches.ts` — removed.
- `ui/src/styles.css` — `.status` (line 501) and its state colours, `.row-actions` (line 559), `.text-button` (line 804), `.pane-header` (line 154).
- `ui/e2e/specs/worktree.e2e.ts`, `ui/e2e/helpers.ts` — the menu and dialog tests, `gitWorkspace`, `commit`, `worktreeEntry`, `oxctl`.

## Decisions

- **`Branches.head`.** `pub head: Option<String>`: the local branch the workspace's checkout has checked out, `None` when `HEAD` is detached or unborn. `branches::list` adds `%(HEAD)` to its `for-each-ref` format; run in the workspace directory, it marks with `*` the branch that checkout's `HEAD` points to, so no further git process runs. `list` returns the head with the local and remote lists.

- **New Task.** In `SessionList`, `newTask(workspace)` calls `workspace_branches`, refuses with `${directoryName(workspace.path)} is not on a branch` through `showError` when `head` is `null`, and otherwise creates the session with `{ type: "worktree", compare: head }` (`branch` absent, so `oxd` names it `ox/<id8>` and starts it at `HEAD`). The comparison ref comes from the listing and the start point from `HEAD` at creation; a branch switch in the workspace's checkout between the two calls gives a session compared with a branch it did not start from, which its row and Merge button show as behind or ahead.

- **Menu.** The **+** button's title and label become "New Task". The menu keeps the agent select, then **New Task** when `workspace.git_working_tree`, then **Session in Checkout** (`{ type: "workspace" }`, the renamed Existing Checkout).

- **Which sessions are local merge.** A session that is not archived and has `worktree?.compare` set. The ref's name is `worktree.compare`, used in every label. Other sessions render neither the status icon nor the Merge button.

- **Labels in one module.** `ui/src/localMerge.ts` holds the wording, shared by the row and the header:

  ```ts
  /** A local merge row's status icon, or null for no icon. */
  export function branchStatus(compare: string, comparison: Comparison): { attention: boolean; tooltip: string } | null;
  /** Why Merge into `compare` is disabled, or null when it is enabled. */
  export function mergeBlocker(compare: string, comparison: Comparison): string | null;
  ```

  `branchStatus`: `failed` → attention, git's error; `ahead` 0 → `null`; `ahead` 1 and `behind` 0 → "1 commit ahead of main"; otherwise attention, "3 commits ahead, 2 behind main", or "2 commits ahead of main" when `behind` is 0. `mergeBlocker`, checked in this order: `failed` → git's error; `ahead` 0 → "No commits"; `behind` > 0 → "Behind main: ask the agent to rebase"; `ahead` > 1 → "2 commits: ask the agent to squash".

- **Row icon.** Rendered at the end of the row's `.select` button as `<span className="status branch-status[ attention]" role="img" aria-label={tooltip} title={tooltip}><GitBranch /></span>`. It is a `.status`, so it takes that class's box and muted colour; `.status.attention` joins the `--warning` rule with `awaiting_permission` and `awaiting_input`. Its centre already matches the row action's: both sit `--sidebar-row-padding` + half `--icon-size` = `--row-action-inset` + half `--icon-button-size` from the row's end. A `.branch-status` rule fades it out over `--transition-duration` while the row is hovered or has focus, winning over the exited-row dimming.

- **Shared session actions.** The delete, archive and unarchive flows move out of `SessionList` into `ui/src/useSessionActions.tsx`, called once in `App`: it returns `busy`, `deleteSession(sessionId, discard)`, `archiveSession`, `unarchiveSession` and `dialogs` (the unsaved-work and kept-branch dialogs), and `App` renders `dialogs`. `SessionList` takes the actions as a prop, so a delete started from the Merge flow disables the row's actions and goes through the same unsaved-work dialog.

- **Merge button.** `ui/src/MergeButton.tsx`, a header-sized `text-button` reading `Merge into ${compare}`, disabled with `title={mergeBlocker(…)}` or while the move runs. Click: `invoke("move_branch", { workspaceId, branch: compare, to: comparison.tip, expected: comparison.compare_tip })`. An error goes to `showError` and nothing else happens; success opens a `Dialog` titled "Merged into main" with the text "Delete the session? Ox removes its worktree and its branch `ox/…`." and the actions **Keep Session** and **Delete Session**, which calls `deleteSession(session.id, false)`. The merged branch is fully contained in the ref, so the branch rule deletes it.

- **Header slot.** `Workbench` takes `actions?: ReactNode`, rendered in the content header before the toggles. `App` passes `<MergeButton key={session.id} …/>` for a local merge session. `.content-header` gets a `gap` of `calc(var(--spacing) * 2)` so the button does not touch the toggles.

- **Header-sized text button.** A text button in a pane header keeps the `text-button` look at the height of the header's icon buttons, so it fits the header's 28px content box and shares their centre line. A new token and one shared rule for the kind:

  ```css
  --pane-header-button-height: var(--icon-button-size);

  /* A text button in a pane header, as tall as the header's icon buttons. */
  .pane-header .text-button {
    box-sizing: border-box;
    height: var(--pane-header-button-height);
  }
  ```

  `box-sizing: border-box` makes the border part of the height, so the button's outer height equals an icon button's.

- **Tauri commands.** `move_branch(workspace_id, branch, to, expected)` calls `Client::move_branch` with `force: false` and `utf8_env()`. `workspace_branches` drops its `fetch` parameter and always lists without fetching, since nothing in the app fetches any more. `new_session` is unchanged: `Checkout` already carries `compare`.

- **`protocol.ts`.** `Checkout` becomes `{ type: "workspace" } | { type: "worktree"; compare: string }`; `WorktreeInfo` gains `compare: string | null`; `SessionInfo` gains `comparison: Comparison | null` with `Comparison` mirroring the Rust enum; `Branches` shrinks to `{ head: string | null }`; `WorktreeBranch` and `LocalBranch` go.

## Check your assumptions

1. **Refusals shown in a message box have no e2e test.** Kept.
   - **Decided:** New Task's "not on a branch" refusal and a refused merge go through `showError`, a native message box the WebDriver tests cannot see or close; no e2e spec exercises them. The Rust tests cover `head` being `None` for a detached `HEAD`, and the previous item's tests cover every move refusal.
   - **Consequences:** The two TypeScript branches that call `showError` are checked only by `tsc` and review.
   - **Alternative:** Show these refusals in an in-window `Dialog`, which e2e can read.
   - **Trade-offs:** The decision keeps errors in one place, as the app shows every other error. The alternative tests the paths end to end, at the cost of a second way to show errors.

2. **"No commits" shows no tooltip.** Changed: the spec's table now gives this state no icon and no tooltip, and the plan follows it.
   - **Decided:** With no commits ahead the row has no icon and no git tooltip; the row's own tooltip stays the session's directory.
   - **Consequences:** Hovering a fresh task's row shows its path, not its git status.
   - **Alternative:** An invisible hover target at the icon's place, or the text appended to the row's tooltip.
   - **Trade-offs:** The decision adds nothing to the row. The alternatives keep the tooltip at the cost of an element or a mixed tooltip.

3. **Older worktree sessions get neither icon nor button.** Kept.
   - **Decided:** Only sessions with a comparison ref are local merge, as the spec says; worktree sessions created before this change have none.
   - **Consequences:** To merge an older session from the app, the user first runs `oxctl compare <session> main`.
   - **Alternative:** The app sets the workspace's branch as the ref on worktree sessions that lack one.
   - **Trade-offs:** The decision adds no migration path. The alternative saves a command for a handful of sessions and guesses a ref for sessions made deliberately without one.

4. **The Merge button is a header-sized `text-button`.** Changed: from a plain 32px `text-button` that overflowed the header's content box to the header-sized text button in Decisions.
   - **Decided:** A text button in a pane header takes `--pane-header-button-height`, the icon buttons' height, through one shared `.pane-header .text-button` rule, keeping the `text-button` border, background and shadow.
   - **Consequences:** Merge into main fits the 28px header row beside the panel toggles, sharing their height and centre line; nothing overflows into the padding.
   - **Alternative:** The plain `text-button`, 32px tall, overflowing into the header's padding.
   - **Trade-offs:** The decision adds a token and a rule that every future header text button uses. The alternative adds no CSS but does not fit the header.

5. **The disabled button's tooltip relies on WebKit.** Kept.
   - **Decided:** The reason is the `title` of the disabled button itself.
   - **Consequences:** If WKWebView does not show tooltips on disabled buttons, the reason is invisible; `/kwork` checks it in the running app and, if so, puts the `title` on a wrapping `span`.
   - **Alternative:** Always use the wrapper.
   - **Trade-offs:** The decision keeps the markup flat when WebKit allows it. The alternative is certain but adds an element.

## Test plan

- `crates/oxd/tests/worktrees.rs`: the listing test asserts `head == Some("main")`, also from a subdirectory; after `git switch --detach` in the checkout, `head` is `None`; a session's worktree on `other` does not change the workspace's `head`.
- `crates/ox-protocol/src/messages.rs`: `Branches` serializes `head`, set and `null`.
- `ui/e2e/specs/worktree.e2e.ts`, rewritten for the menu:
  - Outside Git the menu has the agent select and only **Session in Checkout**, which starts a session in the workspace's directory.
  - In Git it has **New Task** and **Session in Checkout**. New Task starts a selected session on one new `ox/*` branch at `main`'s commit, with no status icon and `oxctl compare` printing `main` as its ref.
- `ui/e2e/specs/merge.e2e.ts`, each on a New Task session in a `gitWorkspace`, committing in the worktree with `commit` and refreshing with `oxctl compare <session>` (a helper in `helpers.ts`):
  - No commits: no icon; **Merge into main** disabled with title "No commits".
  - One commit: icon without `attention`, title "1 commit ahead of main"; the button is enabled. Clicking it moves `main` to the session's tip, the commit's file appears in the workspace's checkout, and the "Merged into main" dialog opens. **Delete Session** removes the row and the `ox/*` branch.
  - **Keep Session** closes the dialog and keeps the session; the button is then disabled with "No commits".
  - Two commits: `attention` icon titled "2 commits ahead of main"; the button's title is "2 commits: ask the agent to squash".
  - A commit on `main` in the workspace's checkout as well: title "1 commit ahead, 1 behind main"; the button's title is "Behind main: ask the agent to rebase".
  - A Session in Checkout and a worktree session started with `oxctl new --worktree` (no ref) show neither icon nor button.
- The existing archive and delete e2e specs pass unchanged through `useSessionActions`.

## Implementation plan

1. `oxd` and protocol: `Branches.head`, `%(HEAD)` in `branches::list`, `Daemon::branches`, the Rust tests above.
2. `crates/ox/src/lib.rs`: `move_branch` command, `workspace_branches` without `fetch`, both in `generate_handler!`.
3. `ui/src/protocol.ts` changes.
4. `ui/src/localMerge.ts`.
5. `ui/src/useSessionActions.tsx`, moving the flows and dialogs out of `SessionList`; `App` owns it.
6. `SessionList`: the new menu and `newTask`, the row icon; delete `ExistingBranchDialog.tsx`, `NewBranchDialog.tsx`, `useBranches.ts` and their state.
7. `ui/src/MergeButton.tsx`, `Workbench`'s `actions`, `App` wiring.
8. `styles.css`: `.status.attention`, the `.branch-status` fade, the content header's gap, `--pane-header-button-height` and the `.pane-header .text-button` rule.
9. e2e specs and helper.

## Documentation updates

- `docs/architecture.md`: the `workspace.branches` paragraph (line 228) adds `head`. The app paragraph describes the **+** menu (agent select, New Task, Session in Checkout), the row's git status icon, and Merge into main with its delete offer, in place of the two dialogs.
- `docs/worktrees.md`: the App column of the checkout table names New Task and Session in Checkout, and the paragraphs about the two dialogs and their fetch go; the existing-branch and new-branch rows say `oxctl` only.
- `.agents/todo.md`: check off **App**.
