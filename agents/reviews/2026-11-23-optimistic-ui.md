# Review: show actions at once, and session status on first load

## Scope and coverage

Reviewed the uncommitted changes on branch `optimistic-ui` against base
`360bf19` (`git merge-base origin/main HEAD`), with the working tree as the
implementer left it: 21 modified files and the untracked plan. Checked against
`agents/plans/2026-11-23-optimistic-ui.md`, its approved Check your assumptions
answers and Requirement changes, `AGENTS.md` and `cmd/ox/web/AGENTS.md` as
edited by this change, and the open `bug` issues (#5, #6, #33, #34, #55).

The requirements diff matches the plan's Requirement changes word for word. The
server's published `state` is written only under `Server.mu` by the session's
owner, and `workspace/list` and `session/created` read it under the same lock.
`message_id` is checked by `isID` at the boundary, and `oxd` never relied on
generating it: the Claude, Ur, and fake harnesses and history only carry it.
Claude already took a non-UUID `uuid` (`rand.Text()`), and `make live` passes
with the page-chosen one.

The implementer's choices hold:

- `reconcilePrompt` counts a prompt as dropped only in `sent`. Removing that
  guard fails three creation specs, because the session's `session/get`, sent
  on `session/created`, routinely answers while the first send is in flight.
- The menu's "Wait until Ox answers" applies the same `busy()` that holds
  `free()`, as the plan's Decisions require.
- The draft log's `role="log"` makes the Pending region reachable, and the
  biome suppression it replaced no longer applies.
- `.pending[hidden], .approval[hidden]` is needed because `.approval` sets
  `display: flex`.
- `state` without `omitempty` is right: every session has one.

Gaps:

- Two-page behavior was reviewed by trace, not run.

## Summary

| ID | Severity | Category      | Action   | Status   | Title                                                        |
| -- | -------- | ------------- | -------- | -------- | ------------------------------------------------------------ |
| F1 | Medium   | Testing       | Fix      | Resolved | No test shows Stop again after a stopped turn                |
| F2 | Low      | Testing       | Fix      | Resolved | No test holds a session's menu items while its request flies |
| F3 | Low      | Documentation | Fix      | Resolved | `session.state` comment names one writer of two              |
| F4 | Low      | Simplicity    | Fix      | Resolved | Inline IIFE picks the archived composer's text               |
| F5 | Low      | Testing       | Rejected | —        | ExitPlanMode live spec fails intermittently                  |

## F1. No test shows Stop again after a stopped turn

Medium · Testing · Fix · Resolved · `cmd/ox/web/src/ox.tsx:552`

`stopping` ends only when `setState` writes a state other than `running` or
`requires_action`. If that call is lost, Stop never shows again in that session
for the life of the page, and no test notices.

### Evidence

Replaced `controls.get(id)?.stopped()` with `void 0` and ran the full `make e2e`
suite: 159 passed, 4 skipped.

### Action

Extend "Stop appears once the page's send is accepted, and stops it" in
`session.spec.ts`: after the stopped turn ends, send another long prompt and
assert Stop is visible again.

## F2. No test holds a session's menu items while its request flies

Low · Testing · Fix · Resolved · `cmd/ox/web/src/Sidebar.tsx:338`

The plan's Decisions give each session at most one list request in flight, with
its menu items unavailable meanwhile. Without that, a row shown with archived
sessions visible offers Unarchive while its archive is in flight. Choosing it
overwrites the `listing` entry, and the first request to settle deletes the
entry that belongs to the other. No spec covers the hold.

### Evidence

`grep` finds no spec that reads "Wait until Ox answers" or checks a menu item
while a `session/archive`, `session/unarchive`, or `session/destroy` is held.
With `waiting` removed from `sessionItems`, the full `make e2e` suite
passes: 159 passed, 4 skipped.

### Action

Extend "a failed archive shows its error in the session list" in
`session.spec.ts`: with archived sessions shown and `session/archive` held,
open the row's menu and assert Unarchive and Delete are disabled with the
description "Wait until Ox answers".

## F3. `session.state` comment names one writer of two

Low · Documentation · Fix · Resolved · `internal/server/session.go:39`

The comment says `state` is "written only by run, and by publishLocked before
run starts". `addChild` calls `sess.acceptMessage` after `publishLocked` and
before `go sess.run()`. The child's agent publishes `running` through
`sess.update`, which writes `state` on the parent's goroutine. The write is
safe, because the builder still owns the session and holds `Server.mu`, but the
comment misstates who writes it.

### Evidence

`internal/server/mcp.go:418-424`; `TestCreateSession` observes that `running`
update.

### Action

Say that whoever owns the session writes `state` under `Server.mu`: its builder
until `run` starts, then `run`. No test.

## F4. Inline IIFE picks the archived composer's text

Low · Simplicity · Fix · Resolved · `cmd/ox/web/src/SessionView.tsx:81`

The archived composer picks its sentence with an immediately invoked function
inside JSX. It is the only one in the views, and it is harder to read than a
named accessor.

### Evidence

`SessionView.tsx:81-87`.

### Action

Add `const parentArchived = () => { const p = parent(); return p !== undefined && ox.archived(p); };`
beside `parent` and use it in the sentence. The Unarchive button keeps reading
the store's `parent()?.archived`, as the plan's Decisions require. No new test.
"a child cannot be unarchived while its parent is archived" already covers it.

## F5. ExitPlanMode live spec fails intermittently

Low · Testing · Rejected · `cmd/ox/web/e2e/approval.spec.ts:66`

The implementer saw "an ExitPlanMode plan renders as Markdown" fail once. It
failed 2 of 4 runs here, each time at line 66. In both failures Haiku wrote the
plan to the plan file first and then called `ExitPlanMode` with `{}`. The
approval card showed both list items, but the transcript's tool call had no plan
to render.

### Evidence

`OX_LIVE=1 npx playwright test approval -g "ExitPlanMode plan renders"
--repeat-each=4`: 2 passed, 2 failed. The failures' page snapshots show
`ExitPlanMode {} Denied by user` after a `Write` call.

### Action

Rejected: this is open issue #6, and nothing in this change reaches the tool
call's input or Claude's choice to write the plan file. The runs above are the
live reproduction #6 waits on, so whoever owns #6 should add them to the issue.

## Checks run

- Reviewed the implementer's reported checks: `make test`, `make e2e` (159
  passed), `make live`, and headless Chrome through `make dev` with Haiku.
- Removed the `sent` phase guard in `reconcilePrompt`: "edits typed while a new
  session is created and sent stay in the composer", "text retyped equal to the
  prompt stays after the creation", and "a lost creation reply is recovered
  after leaving the draft" fail.
- Removed `busy()` from `free()`: "Unarchive shows the composer before oxd
  answers" fails.
- Applied `state` and `parked` updates only to loaded sessions: three
  archive-family specs fail.
- Removed the end of `stopping` on a state change: the full e2e suite passes
  (F1).
- Removed the menu's hold while a list request is in flight: the full e2e suite
  passes (F2).
- Ran "pending approvals leave the composer above the keyboard" twice on the
  unchanged tree after it failed once during a mutation run: it passed both
  times.
- `npm --prefix cmd/ox/web run lint` passes, with two CSS specificity warnings
  in lines this change does not touch. `go vet ./internal/server/` is clean.
- The ExitPlanMode live spec, four runs (F5).
- Every mutation was reverted, and `src/ox.tsx` and `src/Sidebar.tsx` compared
  equal to their saved copies afterward. The test artifacts the runs left were removed.

## Verification 1

Checked the uncommitted tree on `360bf19` after the fixes, which changed
`cmd/ox/web/e2e/session.spec.ts`, `cmd/ox/web/src/SessionView.tsx`, and
`internal/server/session.go`. `make test` passed, and `make e2e` passed with
159 passed and 4 skipped. `make live` and the `make dev` check in headless
Chrome were not rerun. The fixes change no part of how Ox drives Claude Code or
how the page reaches `oxd`, and both passed on the change before the fixes.

| ID | Status   | Evidence                                                                                                                                                                                 |
| -- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | "Stop appears once the page's send is accepted, and stops it" now sends again and expects Stop. It fails with `stopped()` removed from `setState` and passes with it.                    |
| F2 | Resolved | "a failed archive shows its error in the session list" shows archived sessions and expects Unarchive and Delete disabled with "Wait until Ox answers". It fails with `waiting` removed and passes with it. |
| F3 | Resolved | The comment names the builder until `run` starts, then `run`. `update`'s doc comment states the lock.                                                                                    |
| F4 | Resolved | `parentArchived` replaces the IIFE. The Unarchive button still reads the store's `parent()?.archived`.                                                                                   |

No new findings.

## Verification 2

Checked rebased commit `c9665e5` on base `0ac289f`, which brings in #64 (child
reports) and #65. `a93acad` has the same tree. The rebase resolved code
conflicts, so the changes since Verification 1 were reviewed:

- `deliverReports` reads each child's published `state` under `Server.mu`, in
  the same critical section as `children`, instead of asking each child's
  goroutine for its agent's state. The agent's tracker publishes a state change
  through `OnUpdate` in the same call that makes it, on the child's goroutine.
  So the published state matches the agent's at every boundary between that
  goroutine's operations, which is all the old `ask` could observe. The two
  differ only while that call is still running. A delivery that reads in that
  window takes the child's earlier reports, and the child's next non-`running`
  update pings the family again. No report is lost or delivered twice, because
  reports are still taken on each child's goroutine. The old code already
  allowed the same interleaving between asking one child and taking reports.
  This is a race between independent events in which every outcome is valid,
  as the implementer judged. A child's first `running` update is published on
  the parent's goroutine, which is the one that runs the parent's delivery, so
  no delivery sees a new child before that update. The architecture
  paragraph matches the code.
- `PromptBubble` keeps child reports' `reports` branch and passes `files` to
  `MessageBubble`, which renders local images through `LocalImage`. The
  `Prompt` comment in `protocol.ts` merges both changes. The union is #64's.
- The Stop specs use the fake's `wait 60000` input in place of the removed
  `words()`.

Checks: `go test -race -count=1 ./...` passed, and the full e2e suite passed
with 160 passed and 4 skipped. With `stopped()` removed from `setState`, the
rebased "Stop appears once the page's send is accepted, and stops it" fails, so
F1 stays resolved. `make live` was not rerun. The rebase changes no part of how
Ox drives Claude Code, and `make live` passed on the change before it.

| ID | Status   | Evidence                                             |
| -- | -------- | ---------------------------------------------------- |
| F1 | Resolved | The rebased spec fails without `stopped()`           |
| F2 | Resolved | The spec is unchanged by the rebase and passes       |
| F3 | Resolved | The comment is unchanged by the rebase               |
| F4 | Resolved | `parentArchived` is unchanged by the rebase          |

No new findings.

## Verdict

Ready to commit. F1 to F4 are resolved, and F5 is rejected, through Verification 2.
