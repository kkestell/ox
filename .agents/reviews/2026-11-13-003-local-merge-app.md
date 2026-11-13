# Local merge in the app

## Scope and coverage

The staged changes for `.agents/plans/2026-11-13-003-local-merge-app.md`: `Branches.head` and `%(HEAD)` in `branches::list`, `Daemon::branches`, the `move_branch` and `workspace_branches` Tauri commands, `protocol.ts`, `localMerge.ts`, `useSessionActions.tsx`, the **+** menu and row icon in `SessionList.tsx`, `MergeButton.tsx`, the `Workbench` header slot and `App` wiring, the CSS, the Rust and e2e tests, and the documentation and spec edits. The plan links no brief; I checked the change against the plan's Decisions and the local merge sections of `.agents/specs/git-workflows.md`. I traced the merge flow through `branch.move`, the recompare, and the delete through `useSessionActions`, and the row icon's CSS against the exited-row dimming, the row-action fade and the title mask.

Gaps:

- Whether WKWebView shows the `title` tooltip of the disabled Merge button (the plan's assumption 5) is unverified; the app was not driven by hand, as the work log also notes.
- `make e2e`, `make check-rust` and `make smoke-claude` were not rerun; the work log reports they passed, and the review's only change is one CSS selector.

## Summary

| ID | Severity | Category    | Fix       | Title                                                        |
| -- | -------- | ----------- | --------- | ------------------------------------------------------------ |
| F1 | Medium   | Correctness | Fixed     | A disabled Merge button looks enabled                        |
| F2 | Low      | Correctness | Not fixed | Hovered title fades too far on rows with a status icon        |
| F3 | Low      | Correctness | Not fixed | Merged dialog promises to remove a branch Ox may keep         |
| F4 | Low      | Comments    | Not fixed | `workspace.branches` doc comment is unwrapped                 |

## F1. A disabled Merge button looks enabled

Medium · Correctness · Fixed · `ui/src/styles.css:839`

**Merge into main** shows in the header of every local merge session and is disabled in most states (no commits, behind, more than one commit, git error). `.text-button` sets its own border, background and `color: var(--foreground)`, which override WebKit's disabled look, and no rule styled `.text-button:disabled`. A disabled Merge button therefore looked the same as an enabled one, except that it did not highlight on hover. Its peers dim when disabled: `.icon-button:disabled` and `.text-field:disabled` both take `--disabled-opacity`.

### Evidence

`grep -n disabled ui/src/styles.css` lists only `.text-field:disabled` and `.icon-button:disabled`; no rule, global or for buttons, covers a disabled `.text-button`.

### Fix

`.text-button:disabled` joins the `.text-field:disabled` rule, so a disabled text button takes `--disabled-opacity` like its peers. This also dims the elicitation form's submit button while a reply is pending.

## F2. Hovered title fades too far on rows with a status icon

Low · Correctness · Not fixed · `ui/src/styles.css:586`

On hover, a row's title is masked over `--covered`, measured from the title's right end on the assumption that the title ends at the row's padding. On a local merge row with a status icon, the title ends one icon and one gap (22px) earlier, so the mask fades 22px more of the title than the row actions cover. Hovered titles of these rows fade out further left than on other rows.

### Evidence

`.session .select` has `gap: calc(var(--spacing) * 1.5)` and the icon is `--icon-size` wide; the mask's `--covered` does not account for either.

### Fix

Not fixed: Low findings are recorded and left unchanged for this review.

## F3. Merged dialog promises to remove a branch Ox may keep

Low · Correctness · Not fixed · `ui/src/MergeButton.tsx:77`

The "Merged into main" dialog says Ox removes the session's branch. That holds for New Task sessions, whose branch Ox created. A session started with `oxctl new --branch <existing>` and given a ref with `oxctl compare` is also local merge, and deleting it keeps the branch under the branch rule. The kept-branch dialog then reports the kept branch, so the user sees the outcome.

### Fix

Not fixed: Low findings are recorded and left unchanged for this review.

## F4. `workspace.branches` doc comment is unwrapped

Low · Comments · Not fixed · `crates/ox-protocol/src/messages.rs:51`

The edited doc comment on `Method::WorkspaceBranches` has a 101-column line, while the rest of the comment wraps at the file's usual width. It has no effect on behavior.

### Fix

Not fixed: Low findings are recorded and left unchanged for this review.

## Checks run

- `make check-ui` after the fix: Biome, `tsc` and `tsc -p e2e` passed.

## Verdict

The change delivers the plan: New Task reads `head` and starts a compared worktree session, local merge rows show their status, Merge into main fast-forwards with `branch.move` and offers the shared delete flow, and the branch dialogs are gone. F1, a disabled Merge button that looked enabled, is fixed. F2–F4 are minor and left as they are.
