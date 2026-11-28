# Why the workflow lets defects through

## Scope and evidence

Research only. Read: every structural audit and sweep from November 24 to 27,
the plans and reviews behind the defects they trace, the 54 `bug` issues since
November 21, `.claude/skills/ox-plan` and `ox-review`, `AGENTS.md`,
`docs/architecture.md`, `internal/server`, and the web client's state modules.
Blame data, per PR, comes from a parallel measurement in `/tmp/oxdesign`: for
each bug-fix PR, the commits whose lines it rewrote. Blame measures churn in
code a fix touched. In hot code it overstates who introduced a defect, so the
counts below are evidence and not proof.

## Summary

The defects come from one place more than any other: session lifecycle, the
rules for how sessions start, report, archive, and end, which interact across
sessions and across the page and `oxd`. A change there is checked by a plan
and a review that each reason about one path, and by example tests that cover
the sequences the plan names. Nothing checks the interactions between paths
until an audit does. That is why 41 of the 54 `bug` issues came from audits,
and why fixes to that code are rewritten twice as often as other changes. The
remedies that fit this cause are one test that explores those interactions,
and fewer interacting rules. More prose in `AGENTS.md` does not fit it: the
rate of defects per change stayed flat while the rules grew.

## What the data shows

- **Where the defects are.** 21 of 54 are session lifecycle: about 11 in
  `oxd` and the Claude driver (#5, #33, #34, #71, #85, #110, #127, #128, #147,
  #187, #188) and about 10 in the page (#28, #72, #84, #86, #107, #111, #148,
  #155, #162, #202). Child report delivery alone took five rounds (#71, #85,
  #110, #127, #187). Next come 9 layout defects, mostly from tabs and splits
  (#144).
- **Who catches them.** Audits and sweeps found 41 of the 54. The plan and
  review of the change that introduced a defect almost never found it.
- **Size does not predict defects.** Among PRs merged by November 25, the
  later fixes per thousand changed lines (tests and docs excluded) fall as PRs
  grow: 13 for PRs under 150 lines, about 7 for PRs from 150 to 900 lines.
  27% of the 55 smallest PRs were later rewritten by a fix. Defects come per
  change to the wrong code, not per line.
- **Fixes in that code get fixed again.** 11 of 17 bug-fix PRs were later
  rewritten by another fix, against 18 of 54 other PRs. The most-blamed
  commits are #66 (actions shown at once and session status, rewritten by 9
  fix PRs) and #64 (child reports, by 4). Both added lifecycle state that more
  than one path reads.
- **No defect was a data race.** None of the 54 issues reports one. The race
  detector already runs under `make test`.

## Root causes

### 1. Lifecycle rules interact, and nothing checks the interactions

Each lifecycle rule is sound alone. The defects appear where a new path meets
an old rule:

- #175 added sessions that report to their creator and own worktrees. That
  broke the report-delivery design from #130, which assumed no reporter had a
  worktree (11-27 F1, #187). It also added a second creation path that
  published before its first message was accepted, a defect #167 had fixed one
  day earlier in the first path (11-27 F2, #188).
- #168 sent Ur's Ox tools with bare names. The page matched a prefix that
  only Claude sends (#189).
- #140 made loading per-session and kept #38's global retry (#155).

Every one of these is a sequence across two features. The tests that exist
are examples: each fixes one sequence the plan named. A new path does not run
through the old examples, so they pass. The audits that found these defects
wrote probes that walked those sequences, confirmed them, and then deleted the
probes.

### 2. Reviews check the change against its plan, and the plan can be wrong

`ox-review` took a plan's Decisions as requirements, and `ox-plan` forbids
reviewing the plan. Two of the defects above were explicit plan decisions:
the #130 plan used a cleared `toParent` to stand for "being deleted," and the
#175 plan extended it and chose to publish first, with reasons. The #175
review did raise the publish-first symptom (its F3) and rejected it as not
reached in normal use.

### 3. Some rules couple sessions more than they need to

The requirement "Session reports" makes reports wait until the recipient is
idle "and none of the sessions it started is working," then delivers them
together. That makes every reporter's state an input to every sibling's
delivery. Every path that changes any of those states must re-check delivery.
The sibling condition is behind #85 (a report delivered while a sibling ran),
sweep F10 (a failed start split the batch), and part of #110.

"Actions show at once" makes the page keep its own projected state beside
`oxd`'s for every action in flight. The page's `Session` type still carries
`oxd`'s raw `archived` beside the projected `ox.archived()`, so each view must
remember to call the right one. #202 was three views that read the raw field.

## What does not fit the cause

These were in the first version of this audit and do not hold up:

- **Lock annotations (`checklocks`).** No defect was a data race. #187 was a
  delivery decision that did not consult `deleting`, not an unlocked access,
  and #192 fixed it. Annotating would have forced moving every `Server.mu`
  fact off `session`, which touches every server file to prevent a class that
  produced no defects.
- **Generating `protocol.ts` from Go.** The protocol's Go types are mostly
  anonymous, so generation needs a dispatch rewrite and a custom generator, to
  prevent cheap defects (#152, #193).
- **Review depth by diff size.** Defects do not grow with PR size. The
  concern reviewers check lifecycle, boundaries, and construction. None checks
  layout, so a deeper review of #144 would not have reached its layout defects.
- **Fix the class in prose, and record every open finding.** Both add rules
  of the kind whose growth left the defect rate flat. The first is better done
  by recommendation 1. The second widened "Record" past its definition in
  "Defects," and it rests on three cases.

## Recommendations, ranked by defects prevented per cost

1. **A stateful lifecycle test in `internal/server`.** Drive the test client
   with the fake harness through seeded random sequences: create (child,
   top-level, with and without a worktree, with a harness that fails to
   launch or refuses its first message), send, a long turn, interrupt,
   archive, unarchive, dirty a worktree, and delete. After each step, check:
   - every report is delivered once, or dropped because its sender or
     recipient was deleted;
   - the sessions in `workspace/list`, in the store, and announced by
     `session/created` agree, and git's worktrees and `ox/` branches match
     them;
   - every started session has its first message;
   - a refused delete changes nothing.

   It must fail with #191 or #192 reverted. Every later lifecycle path joins
   its generator, and an audit's lifecycle probe becomes a seed or a step in it
   instead of being deleted. Cost: one test file using existing helpers
   (`gitRepo`, `worktreeServer`, `sendGateAgent`, the fake's `wait`). It covers
   the server half of the lifecycle defects, which are also the most severe.
2. **Views read only the page's projected state.** In `ox.tsx`, the `Session`
   type that views receive drops `oxd`'s raw `archived`. The store keeps the
   raw value out of the views' reach, so `ox.archived()` is the only way to
   read it, and `tsc` rejects the mistake behind #202. Cost: a type change in
   `ox.tsx` and the views that read it.
3. **Product choice: deliver reports without waiting for siblings.** Change
   "Session reports" so reports arrive when the recipient is idle or parked,
   whether or not its other sessions are working. Readiness then depends only
   on the recipient, and the sibling coupling behind #85, sweep F10, and part
   of #110 goes away. What the user gives up: a session running several
   sessions at once wakes once per finished turn instead of once per batch, so
   it uses more turns and sees partial results. This is a product question.
4. **One layout reachability spec.** Walk phone and desktop widths across one,
   two, and nested groups, with panels open and closed. Assert that every tab
   control and panel toggle is in the viewport and passes a trial click. Some
   specs from #164 already check single layouts; this one covers combinations,
   where #150, #174, and 11-26 F7, F13, and F25 were found. Cost: one spec.

## Workflow rules from the first version

`ee8b8fd` put four rules on `main`. Keep the plan-review rule in `ox-review`
(cause 2). Revert the 400-line threshold, the fix-the-class step in `ox-plan`,
and the record-every-finding rule in "Defects," for the reasons under "What does
not fit the cause."
