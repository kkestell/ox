# Lifecycle guards

## Goal

Recommendations 1–4 of
[Why the workflow lets defects through](../audits/2026-11-28-workflow-defect-causes.md#recommendations-ranked-by-defects-prevented-per-cost):

1. A lifecycle test in `internal/server` that runs every creation path against
   every creation outcome, and every session role against every destructive
   action in every state, as tables, and checks the lifecycle invariants after
   every step. It covers the paths of #191 and #192.
2. Views read only the page's projected session list. `tsc` rejects a view that
   reads `oxd`'s raw `archived` (the #202 mistake) or a workspace's raw session
   membership.
3. Reports arrive as soon as their recipient is idle or parked, without waiting
   for the recipient's other reporters. Readiness depends only on the
   recipient.
4. One Playwright spec that walks phone and desktop widths across one, two, and
   nested tab groups, with panels open and closed, and checks that every tab
   control and panel toggle is reachable.

## Related code

- `internal/server/report.go` — `readyLocked`, `reportersLocked`,
  `familyChangedLocked`, `takeReports`, `reportsInput`. `readyLocked` holds the
  sibling condition this change removes.
- `internal/server/session.go` — `update` and `destroy` call
  `familyChangedLocked` for the reporter's recipient on a reporter's state
  change and delete start; `finishDelete` calls it on a refused delete.
- `internal/server/archive.go` — `setArchivedLocked` calls
  `familyChangedLocked` for each member.
- `internal/server/create.go` — `createTopLevel`, `addChild`, `launch`,
  `discardUnpublished`: #191's publish-after-first-message path.
- `internal/server/mcp_test.go` — `TestChildReports`,
  `TestRefusedDeleteKeepsReports`, `reportsTaken`, `sendGateAgent`.
- `internal/server/worktree_test.go` — `gitRepo`, `worktreeServer`,
  `addWorkspace`, `createParams`, `allListed`, `registered`, `oxBranches`.
- `internal/server/client_test.go` — the test client.
- `internal/fake/fake.go` — the fake harness: `wait <ms>` keeps a turn running,
  `Interrupt` ends it with `Turn failed: Interrupted`, `Stop` of a running turn
  publishes `exited`.
- `cmd/ox/web/src/ox.tsx` — the store, `Session`, `Workspace`, `archived`,
  `sessionsOf`, `workspaces`, `workspaceOf`, `busy`, and the `state` it returns.
- `cmd/ox/web/src/Sidebar.tsx` — `childrenOf` reads `workspaceOf(id).sessions`
  (raw); the workspace menu reads `workspace.sessions` (raw) for its busy check.
- `cmd/ox/web/src/App.tsx` — the draft-dropping effect reads
  `ox.state.workspaces` (raw).
- `cmd/ox/web/e2e/workspace.spec.ts` — the "on a wide screen" and "on a phone"
  describes from #144 and #164, with `groups`, `dropOn`, `fileTab`, `files`,
  `openFile`, `showFiles`, `togglePanel`, `closeButton`.
- `cmd/ox/web/src/Panels.tsx` — narrow is `(max-width: 767px)` by
  `matchMedia`, so `setViewportSize` switches layouts.

## Decisions

- **Report readiness.** `readyLocked(sess)` holds when `oxd` is not shutting
  down, `sess` is not archived or being deleted, its state is `idle`,
  `background`, or `parked`, and some reporter not being deleted holds a
  report. The "none of its reporters is running" condition goes. A reporter's
  state is no longer an input to its recipient's delivery, so:
  - `update` stops calling `familyChangedLocked(s.reportsToLocked(sess))` on a
    state or parked update; it keeps `familyChangedLocked(sess)`.
  - `destroy` stops calling `familyChangedLocked` for the recipient: a delete
    starting only hides the reporter's reports, which makes nothing ready.
  - `finishDelete`'s refused path keeps both calls: the reporter's reports
    become visible again, and the session itself may now take reports.
  - `report` and `setArchivedLocked` keep theirs.
  - `takeReports` still takes every waiting report, oldest reporter first, as
    one input; reports that waited together still arrive together.
  - `reportsInput`'s opening becomes "Reports from sessions you started, sent
    now that you are idle." with the rest unchanged.
- **Requirements.** "Session reports" changes as under Requirement changes.
  "Working together without approval", "Archive", and "Delete" are unchanged
  and covered by the lifecycle test's invariants. A refused delete still keeps
  the reports it held and the reports waiting for it (#192). The page's
  changes keep "Actions show at once": a session being deleted is gone from
  every view, including from its parent's children, as soon as the user
  confirms.
- **Construction (server).** The invariant "a report's delivery depends only on
  its recipient" is enforced by `readyLocked` reading only the recipient's
  facts and its reporters' reports; no reporter state is read. No new state,
  timer, lock, or stored field.
- **Construction (page).** The store holds `StoredSession` and
  `StoredWorkspace`, private to `ox.tsx`. Views receive `Session`, which is
  `StoredSession` without `archived`, and `Workspace`, which is `{ id; path }`
  without `sessions`. Every read of `oxd`'s raw list goes through `ox.tsx`:
  - `ox.archived(session)` is the only way to read archived.
  - `ox.sessionsOf(workspace)` is the only way to read membership; it looks the
    workspace up in the store by ID.
  - `ox.workspaceOf(id)` returns a `Workspace`.
  - `ox.workspaceBusy(id)`, new, is whether any of the workspace's stored
    sessions has a list request in flight, including one being deleted. The
    "Remove workspace" item uses it, keeping "Remove workspace waits for its
    last session's delete to be answered".
  - `ox.listsWorkspace(id)`, new, is whether `oxd`'s list has the workspace,
    whatever the page has in flight. The draft-dropping effect in `App.tsx`
    uses it, so a refused removal keeps the draft as now.
  - `ox.state` is returned typed as the catalog only (`harnesses`,
    `permission_modes`, `defaults`), so `ox.state.workspaces` does not compile
    in a view.
- **Session state.** `session.state` has no page projection: it is `oxd`'s
  published state, and actions in flight show through each session control's
  request slot, not by projecting `state`. It stays on `Session`, read
  directly.
- **Children on the page.** `Sidebar`'s `childrenOf` reads projected
  membership, so a child whose delete is in flight no longer counts toward its
  parent's "Delete its child sessions first" or the archive wait. If the user
  confirms the parent's delete before `oxd` answers the child's, `oxd` refuses
  with "session has child sessions", which the sidebar shows through its
  `createAttempt`. Reaching that takes opening the menu and confirming a
  dialog, so it is left at that.
- **Lifecycle test design.** `TestLifecycle` in
  `internal/server/lifecycle_test.go` is a deterministic table of plain
  subtests built from setup steps, with no randomness. A new creation path or
  session role adds a row, and runs against every column; the test's doc
  comment says so.
  - Each case runs on its own git repository workspace and server, with
    `MaxChildDepth: 2`, as a parallel subtest. One client applies every
    notification to the test's model in arrival order. The harness is
    `fake.Start`, wrapped by a start function that, while the case has armed a
    failure, fails the start of a session ID the model does not know yet, or
    returns a `sendGateAgent` that refuses its first `Send`.
  - The model tracks, from notifications, the live sessions with their
    worktree, parent, recipient, archived flag, state, and open inputs, and the
    reports made and not yet delivered, by sender. A delivery the model does
    not expect fails at once; a report waiting for a recipient that can take it
    keeps the case from settling. After each step the case settles and checks
    the invariants: the list holds the model's sessions with their archived
    flags and worktrees, a child of an archived session is archived, an
    archived session is parked, git registers exactly the sessions' worktrees,
    and every worktree session published keeps its branch.
  - The model predicts whether `oxd` accepts or refuses each archive,
    unarchive, and delete, with the refusal's message: a delete is refused for
    child sessions, then for a dirty worktree; an archive while the session or
    one beneath it runs a turn; an unarchive while the parent is archived. A
    wrong refusal fails as well as a wrong success. A refused request changes
    nothing: list, store, git, the worktree directory, and the reports waiting.
  - Creation table: {`session/create`, `session/create` with worktree,
    `create_session`, `create_top_level_session`, `create_top_level_session`
    with worktree} × {succeeds, launch fails, first message refused}, from a
    session the user created. `session/create` launches nothing, so it has
    only the success column. A success is listed, stored, and broadcast, with
    the creator's message first for a tool's session, and its worktree and
    branch. A failure returns the refusal and leaves no row, broadcast,
    worktree, or branch.
  - Destructive table: each case builds a session the user created in a
    worktree, its child, and a session it started with and without a
    worktree. Rows: child, started session, started session with worktree,
    and the session they report to. Columns: delete clean, delete with a dirty
    worktree, archive, unarchive (after an archive), each in three states: a
    report held because the recipient runs a turn (for the recipient's row,
    the started session's report held for it, or, for unarchive, held because
    it is archived), a turn running, and idle. Combinations that cannot exist
    are skipped: a dirty delete without a worktree, and an unarchive with a
    turn running. After the action, every running turn is interrupted, and
    every report must have been delivered once, unless its sender or recipient
    was deleted. A refused delete's held reports arrive once the recipient is
    idle.
  - The review's F1 (a refusal was accepted without checking that it should
    happen) and F2 (seeds picked to catch known mutations) are addressed by
    this design: every outcome is predicted, and nothing is random.
- **Layout reachability spec.** `cmd/ox/web/e2e/layout.spec.ts`:
  - Layouts, each built at 1600×900 with `src/a.txt` and `src/b.txt` in the
    workspace: one group (chat and `a.txt`); two groups (`a.txt` dropped on
    the first group's right edge); nested (two groups, then `b.txt` opened in
    the right group and dropped on that group's bottom edge).
  - Desktop context: after building, the test walks widths 1600×900, 960×900,
    and 390×844 with `setViewportSize`. At each width wider than the narrow
    breakpoint it walks sessions list shown and hidden × panel shown and
    hidden; at 390 it walks no drawer, the sessions drawer, and the panel
    drawer.
  - Phone context (`viewport 390×844, hasTouch, isMobile`, as the "on a phone"
    describes use): the layout is built after `setViewportSize(1600×900)`,
    then the viewport returns to 390×844 and the drawers are walked as above.
  - Checked controls: wide, every `tab` in every "Tab group" region, each
    tab's "Close …" button, and every "Toggle sessions" and "Toggle panel"
    button shown. Narrow with no drawer open, every button in the "Tabs"
    toolbar, then, with its tab menu open, every `menuitemradio` and its
    "Close …" button. Narrow with a drawer open, the toggle buttons shown.
    Each check is `toBeInViewport()` and a trial click (`click({ trial: true
    })`, or `tap({ trial: true })` in the phone context).
  - One test per context and layout, six in all, each named for its layout and
    context: one behavior, "every control is reachable in this layout".
  - Helpers the spec needs (`groups`, `dropOn`, `fileTab`, `files`, `entry`,
    `openFile`, `showFiles`, `togglePanel`, `closeButton`) move from
    `workspace.spec.ts` to `e2e/ui.ts`, and `workspace.spec.ts` imports them.
  - A control the spec finds unreachable is a confirmed defect: fix it at its
    cause in this change. A fix that changes the layout the user sees beyond
    making the control reachable goes back to the session that started the
    work before it is built.

## Check your assumptions

1. **Reports no longer wait for the other sessions a session started.**
   Kept: the user approved the new text.
   - **Plan:** When a session has started several sessions, each one's report
     reaches it as soon as it is idle or parked, even while the others are
     still working. It wakes once per finished turn and sees results as they
     come; reports that pile up while it is busy still arrive together.
     Requirement "Session reports", old text: "Reports wait until the
     receiving session is idle or parked and none of the sessions it started
     is working, then arrive together as one message, after any message
     already waiting, each headed with its sender's label and rendered as
     Markdown; a session waiting on the user does not hold them back." New
     text: "A report arrives as soon as the receiving session is idle or
     parked, even while other sessions it started are still working; reports
     waiting together arrive as one message, after any message already
     waiting, each headed with its sender's label and rendered as Markdown."
     The rest of the requirement is unchanged.
   - **Alternative:** Keep waiting for every started session to stop. A
     session running several sessions then wakes once per batch and uses
     fewer turns, at the cost of the coupling behind #85, sweep F10, and part
     of #110, which every new lifecycle path must keep re-checking.

## Test plan

- Report readiness, server: `TestChildReports` replaces its sibling-coupling
  cases.
  - "a running child holds its sibling's report until its interrupted turn
    ends" becomes "a running child does not hold back its sibling's report":
    `one` runs `wait 60000`, `two` replies, the parent takes `{two: 1}` while
    `one` still runs, then the interrupt delivers `{one: 1}`.
  - "a child that starts after the parent's check holds a sibling's later
    report" is removed: it guards a sibling check that no longer exists.
  - "deleting a running child sends its parent no report, and releases its
    sibling's" becomes "deleting a running child sends its parent no report":
    the sibling's report arrives before the delete.
  - "a started session's report arrives with a child's, and a running one
    holds the child's back" becomes "a started session reports beside a child,
    and a running one holds back neither", with the wrapper assertion on the
    kid's report alone.
  - "a running parent holds reports until it is idle, and takes a waiting
    message first" gains a second reporter, so two reports that wait for a
    running parent arrive as one message.
  - The `wrapper` constant takes the new opening; `reportsTaken`'s comment
    says reports may arrive across several messages.
- Refused delete, server: in `TestRefusedDeleteKeepsReports`, "a started
  session whose delete is refused keeps its held report" holds `one`'s report
  by running the creator (`wait 60000`) instead of running `two`, then
  interrupts the creator and expects `{one: 1}`.
- Lifecycle, server: `TestLifecycle`, new, the tables under Decisions, since
  no existing test runs every creation path and session role against every
  outcome and action.
- Page projection, typecheck: `make test` builds the web client (`make web`,
  whose `npm run build` runs `tsc`); a view that reads `session.archived`,
  `workspace.sessions`, or `ox.state.workspaces` does not compile. Shown by
  adding such a read and seeing `npm run build` fail.
- Children on the page, e2e: in `session.spec.ts`, beside "a session with
  children cannot be deleted", a new test "a session whose last child's delete
  is in flight offers Delete": hold `session/destroy` for the child, confirm
  its delete, and check the parent's Delete item is enabled with no
  description. Fails before the change, where it says "Delete its child
  sessions first".
- Existing e2e that guard the page change: "Remove workspace waits for its
  last session's delete to be answered", "Remove workspace gives its reason
  while sessions remain, archived ones included", "removing a workspace
  discards its draft".
- Layout reachability, e2e: `layout.spec.ts`, new, since the existing specs
  each check one layout.
- `make test` and `make e2e` run. `make live` does not observe these changes.

## Implementation plan

1. `internal/server/report.go`: drop the running-reporter condition from
   `readyLocked`, update its comment and `takeReports`'s, and change
   `reportsInput`'s opening.
2. `internal/server/session.go`: remove the recipient's `familyChangedLocked`
   call from `update`'s state branch and from `destroy`; update the comments on
   `destroy`, `finishDelete`, and `update` that describe the sibling rule.
3. `internal/server/mcp_test.go`: rewrite the report cases as the Test plan
   says.
4. `internal/server/lifecycle_test.go`: write `TestLifecycle`'s tables; fix at
   its cause any defect it finds. `internal/server/worktree_test.go`: split
   `gitRepo` into `gitEnv` and `newGitRepo`, so parallel cases make their own
   repositories.
5. `cmd/ox/web/src/ox.tsx`: split the stored and view types, add
   `workspaceBusy` and `listsWorkspace`, make `sessionsOf` and `workspaceOf`
   take and return view types, narrow the returned `state`'s type.
6. `cmd/ox/web/src/Sidebar.tsx`: `childrenOf` from `ox.sessionsOf`, the
   workspace menu's busy check from `ox.workspaceBusy`.
   `cmd/ox/web/src/App.tsx`: the draft-dropping effect from
   `ox.listsWorkspace`. Fix any other view `tsc` now rejects.
7. `cmd/ox/web/e2e/session.spec.ts`: the children test.
8. `cmd/ox/web/e2e/ui.ts`, `cmd/ox/web/e2e/workspace.spec.ts`: move the shared
   layout helpers. `cmd/ox/web/e2e/layout.spec.ts`: the reachability spec; fix
   any control it finds unreachable.
9. Docs, as below. `make docs`.

## Requirement changes

- **Changed:** **Session reports.**
  - Old: "When the turn of a child session, or of a top-level session another
    session started, ends, the session that started it receives its final
    reply, or why the turn failed, as a report, without either session sending
    a message. Reports wait until the receiving session is idle or parked and
    none of the sessions it started is working, then arrive together as one
    message, after any message already waiting, each headed with its sender's
    label and rendered as Markdown; a session waiting on the user does not
    hold them back. A report carries only the first 400 characters of a longer
    reply, none is sent when the sender's last action was a message to the
    session that started it, and reports not yet delivered are dropped when
    `oxd` stops or that session is deleted."
  - New: "When the turn of a child session, or of a top-level session another
    session started, ends, the session that started it receives its final
    reply, or why the turn failed, as a report, without either session sending
    a message. A report arrives as soon as the receiving session is idle or
    parked, even while other sessions it started are still working; reports
    waiting together arrive as one message, after any message already waiting,
    each headed with its sender's label and rendered as Markdown. A report
    carries only the first 400 characters of a longer reply, none is sent when
    the sender's last action was a message to the session that started it, and
    reports not yet delivered are dropped when `oxd` stops or that session is
    deleted."

## Documentation updates

- `docs/protocol.md`, "Session reports": the delivery rule drops the
  reporters-running condition and the `requires_action` clause, the list of
  changes that trigger a try drops "a reporting session's state" and "the set
  of sessions that report to it, as when a reporting session's delete starts",
  "its state holds back no other session's" goes, and the model-facing text
  takes the new opening.
- `docs/architecture.md`, "Turn reports": the Delivery bullet's `readyLocked`
  conditions and the writers that call `familyChangedLocked` match the new
  rule; the deleting bullet drops what a deleting session's state holds back.
- `AGENTS.md`, Source Map: `lifecycle_test.go # stateful lifecycle test` under
  `internal/server`.
