# Local merge in the app

## Plan

`.agents/plans/2026-11-13-003-local-merge-app.md`

## Summary

`workspace.branches` reports `head`. The app's **+** menu offers New Task, which starts a local merge session, and Session in Checkout. Local merge rows show a `GitBranch` status icon. The conversation header has **Merge into <ref>**, which fast-forwards the ref's branch with `branch.move` and offers to delete the session. The two branch dialogs and `useBranches.ts` are gone. The plan's goal is met.

## Departures from the plan

- `branches::list` returns a `Branches` with `fetch_error: None`, and `Daemon::branches` fills in `fetch_error` with struct update syntax. The plan returned a tuple of head, local and remote lists, which would have taken three values.
- `localMerge.ts` also exports `localMerge(session)`, which returns the ref and the comparison of a local merge session or `null`. The row and `App` both use it to decide which sessions follow local merge. It returns `null` when `comparison` is `null`, so the label functions always get a comparison.
- `App` renders `actions.dialogs` beside `Workbench` rather than inside it. A modal under the left panel would not show while the panel is hidden.
- The deleted dialogs were the only users of the `.dialog .fields` and `.dialog label` CSS rules, so those rules are removed too.
- `.pane-header .text-button` follows the `.text-button` rule in `styles.css`, because Biome's descending-specificity check fails `make check-ui` when it comes before it.
- `gitWorkspaceWithRemote` in `ui/e2e/helpers.ts` went with the dialog tests, its only callers. `openMenu` and `newTask` moved into `helpers.ts`, since both specs use them, and `commit` now returns the name of the file it commits.

## Decisions

- The merge spec checks that the commit's file is in the workspace's checkout by its name, which `commit` returns.
- The `.branch-status` fade selector is `.session:is(:hover, :focus-within) .select > .status.branch-status`. That outranks the exited-row dimming rule `.session:has(.status.exited) .select > *`.

## Checks run

- `make check-ui` passed.
- `make check-rust` passed.
- `make e2e` passed: all 9 spec files, including the new `merge.e2e.ts` (6 tests) and the rewritten `worktree.e2e.ts` (3 tests).
- `make smoke-claude` passed.
- With the `%(HEAD)` match in `branches::list` removed, `branches_are_listed_most_recently_committed_first` and `branches_name_the_worktree_that_has_them_checked_out` fail. Both pass with it restored.

## Manual verification

None. Assumption 5 of the plan asked for a check in the running app that WKWebView shows the `title` tooltip of the disabled Merge button. That was not done: WebDriver cannot hover to show a native tooltip, and the app was not driven by hand. The e2e specs confirm only that the `title` attribute holds the reason.

## Follow-up work

- Hover the disabled Merge into main button in the app. If no tooltip shows, put the `title` on a wrapping `span`, as the plan's assumption 5 says.
