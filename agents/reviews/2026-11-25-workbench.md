# Review: land the workbench redesign of the file panel

## Scope and coverage

Reviewed the uncommitted changes on branch `workbench` against
`agents/plans/2026-11-25-workbench.md`. Base revision `bda7f00` (`HEAD`, equal
to `git merge-base origin/main HEAD`). The working tree holds the user-approved
redesign under `cmd/ox/web/src/` and the plan's work on top: D1–D6, the spec
rewrites in `workspace.spec.ts`, `session.spec.ts`, and `hero.spec.ts`, the new
`diff.test.mjs` and its `Makefile` entry, `docs/functional-requirements.md`,
`docs/architecture.md`, `cmd/ox/web/AGENTS.md`, and the four regenerated
`docs/images/hero*.png`.

Read `AGENTS.md`, `cmd/ox/web/AGENTS.md`, the open `bug` issues (#106 covers
Git output the Changes tab misreads; this change does not touch it), and the
architecture sections the change edits. The design itself is the user's and
was not judged.

The requirements diff carries the ten approved changes word for word: Panel
toggles, Resizing panels, Layout through navigation, Narrow screens, File tree,
File viewer, Markdown preview retitled File view modes, Changes view, Change
diffs, and Changes updates. No other requirement changed. The code meets each
of them as traced through `Panels.tsx`, `Workbench.tsx`, `FileView.tsx`,
`Changes.tsx`, and `App.tsx`, and as the e2e suite observes. D1 through D6 are
fixed as the plan describes.

Off-plan choices from the implementing session:

- **`.center`'s minimum includes the clamped tree width, with `--tree` on
  `.center`.** Accepted. The plan put `--tree` on `.panes`, but `.center` must
  read it to keep its own minimum, since the tree now lives inside it. Without
  it, a 960 px window with the tree alone lets the session list's sash push the
  tree off screen (confirmed by removing the rule; see F1). The choice is
  required by D1 and has no test yet (F1).
- **No tabpanel while the changes listing is unanswered.** Accepted; see F4.
- **"not a git repository" twice in the Changes view.** Fix; see F3.

Gaps:

- `/ox-work`'s final response, with its Tests proved and Checks, was not passed
  to this review. The checks below were run here on the final tree.

## Summary

| ID | Severity | Category    | Action   | Status | Title                                                          |
| -- | -------- | ----------- | -------- | ------ | -------------------------------------------------------------- |
| F1 | Medium   | Testing     | Fix      | Resolved | No test protects D1's tree-aware minimum widths                |
| F2 | Low      | Testing     | Fix      | Resolved | No test checks that widening closes the narrow workbench       |
| F3 | Low      | Correctness | Fix      | Resolved | The Changes view says "not a git repository" twice             |
| F4 | Low      | Correctness | Rejected | —      | The file tab has no tabpanel until the changes are first listed |

## F1. No test protects D1's tree-aware minimum widths

Medium · Testing · Fix · Resolved · `cmd/ox/web/src/style.css:250`, `cmd/ox/web/src/style.css:517`

D1's fix rests on two rules: `.center`'s `min-width: calc(240px + var(--beside))`,
which keeps the transcript 240 px beside the tree, and the shared minimum
`min(240px, calc((100cqi - var(--beside)) / 2))` for the transcript and the
file or changes. Either can be reverted with the whole suite passing, and each
revert puts the file tree partly off screen.

### Evidence

- Replacing the shared minimum with the old `min(240px, 50cqi)`: all ten "on a
  wide screen" tests pass. With a probe that adds
  `expect(files(page)).toBeInViewport({ ratio: 1 })` to "at a 960 px window the
  transcript and the file split the width beside the tree evenly", the probe
  fails under the revert and passes on the change. The transcript and the file
  each keep 240 px, and the tree overflows the window.
- Replacing `.center`'s minimum with `240px`: the wide tests pass. A probe at
  960 px with the tree alone that drags "Resize sessions" 2000 px right, then
  asserts `near(main, 240)` and the tree `toBeInViewport({ ratio: 1 })`, fails
  under the revert and passes on the change.

### Action

Keep both probes as tests in the "on a wide screen" describe of
`e2e/workspace.spec.ts`. These are geometry checks that `cmd/ox/web/AGENTS.md`
allows.

- Add `await expect(files(page)).toBeInViewport({ ratio: 1 })` to the existing
  960 px test after the even-split check.
- Add a test, "at a 960 px window the session list's sash leaves the transcript
  240 px beside the tree". At 960 px, open a draft and show Files, drag "Resize
  sessions" 2000 px right, then check `near(main, 240)` and the tree
  `toBeInViewport({ ratio: 1 })`.

## F2. No test checks that widening closes the narrow workbench

Low · Testing · Fix · Resolved · `cmd/ox/web/src/Panels.tsx:35`

Narrow screens: "Switching between a narrow and a wide screen closes the narrow
screen's workbench." `follow` in `createPanels` does this with
`setNarrowMode(undefined)`. Removing that line leaves "crossing the breakpoint
closes the phone's workbench and leaves the wide one as it was" passing. The
test opens Files on the phone and returns to the wide screen, but it never
narrows the page again to see that the phone's workbench was closed.

### Evidence

With the line removed, `-g "crossing the breakpoint"` passes (2 passed). Trace:
the phone's Files tap sets `narrowMode` to `files`. Widening and narrowing again
leaves it set, so the tree replaces the transcript.

### Action

At the end of that test, set the viewport back to 390 × 844. Expect `main` to be
visible, `files(page)` to have count 0, and both tabs to have
`aria-selected="false"`.

## F3. The Changes view says "not a git repository" twice

Low · Correctness · Fix · Resolved · `cmd/ox/web/src/Changes.tsx:93`, `cmd/ox/web/src/Changes.tsx:98`

When the directory is not in a Git repository, the Changes view shows the error
in its header, through `BranchSummary`, and again in its body. Both lines say
the same thing. The spec has to work around it by finding the body's
`paragraph`.

### Evidence

`ChangesView` renders `<BranchSummary>` in the header, which shows
`listing.error()`, and a body `Switch` whose first `Match` shows the same
`listing.error()`. The spec "a directory outside a Git repository says why in
the Changes tab" uses `getByRole("paragraph")` to tell them apart.

### Action

In `ChangesView`, show the header's `BranchSummary` only while the listing has
a value (`<Show when={props.listing.changes()}>`). The body then says why, as it
says "No changes". The file tree keeps the reason in its header, as File tree
requires. In the spec, assert
`await expect(changes(page).getByText("not a git repository")).toHaveCount(1)`.

## F4. The file tab has no tabpanel until the changes are first listed

Low · Correctness · Rejected · `cmd/ox/web/src/FileView.tsx:124`

Until `list_changes` first answers, `mode()` is `undefined`, so no `Match`
renders and the selected "Open files" tab has no tabpanel. The plan said the
pane renders empty.

### Action

Rejected. On screen it matches an empty pane, and it lasts one listing, which is
under a second in normal use. The D3 spec observes it through
`pane(page).toHaveCount(0)`. An empty tabpanel for this state would need another
branch in the content `Switch` and would add no behavior the user can see.

## Checks run

- `make test`: passed (Go with `-race`, lint, and the Node suites including
  `diff.test.mjs`).
- `make e2e`: 197 passed, 5 skipped.
- `make live`: not applicable. The change does not alter how Ox drives Claude
  Code.
- `make screenshots`: not re-run. All four `docs/images/hero*.png` are
  regenerated in the tree.
- Headless Chrome through `make dev`: not run here. The user's stack in
  `.state/` was left alone, and `make e2e` exercises the same `list_changes`
  polling against real `oxd` and `ox` in headless Chromium.
- Spot checks, each reverted after its run:
  - Removing D3's `props.change === undefined` guard fails "a changed file waits
    for its changes to be listed before choosing Diff".
  - Showing `ModeTabs` regardless of `workbench` (D2) fails "with nothing
    selected, the header offers no Files or Changes tabs".
  - Making `openFile` reset the mode on the open file fails "a Markdown file's
    source view holds…".
  - Dropping `main`'s `flex-grow: var(--split)` fails the split tests.
  - Removing `setNarrowMode(undefined)` from `follow` passes (F2).
  - Reverting the shared minimum or `.center`'s minimum passes (F1).

## Verification 1

Checked the uncommitted tree on `bda7f00` after `/ox-work fix`, which changed
`cmd/ox/web/src/Changes.tsx` and `cmd/ox/web/e2e/workspace.spec.ts`. Ran
`make test` (passed) and `make e2e` (198 passed, 5 skipped). Each fix's test
was run with the guarding code reverted, then restored.

| ID | Status   | Evidence                                                                                                                                                                                              |
| -- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | The 960 px split test now asserts the tree `toBeInViewport`, and fails with the shared minimum reverted to `min(240px, 50cqi)`. The new test "the session list's sash leaves the transcript 240 px beside the tree" fails with `.center`'s minimum reverted to `240px`. |
| F2 | Resolved | "crossing the breakpoint closes the phone's workbench…" narrows the page again and checks that both tabs are unselected, the tree is gone, and `main` shows. It fails with `setNarrowMode(undefined)` removed from `follow`. |
| F3 | Resolved | `ChangesView` shows `BranchSummary` only while the listing has a value, and the body says why. The spec asserts that "not a git repository" appears once, and fails with the header's `Show` removed.                     |

No new findings. The `Changes.tsx` change alters only the header's error case.
Before the first answer, the header shows what it showed before.

## Verdict

`Ready to commit`. F1, F2, and F3 are resolved. F4 is rejected.
