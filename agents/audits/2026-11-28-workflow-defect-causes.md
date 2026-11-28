# Why the workflow lets defects through

## Scope and evidence

Research only. Read: every structural audit and sweep from November 24 to 27,
the plans and reviews behind the defects they trace, the 51 `bug` issues closed
since November 21, `.claude/skills/ox-plan` and `ox-review`, `AGENTS.md`,
`docs/architecture.md` ("State ownership and locking"), and
`internal/server/session.go`. PR sizes come from `gh pr view`.

## Summary

Most post-merge defects trace to a decision written in a plan, which no step
reviews, or to a second code path that broke an invariant written only in prose.
The instructions get applied. What fails is the mechanism: nothing executable
holds Ox's lifecycle invariants, and the review gate checks code against the
plan instead of checking the plan.

| Rank | Root cause | Defects |
| ---- | ---------- | ------- |
| 1 | No step reviews a plan's decisions | 11-27 F1, 11-27 F2, 11-24 F1, F2, F3 |
| 2 | Invariants live in prose, so a new path breaks them unseen | 11-27 F1, F2, F3; 11-26 F23; #147 |
| 3 | A fix covers the reported path, not the class | #71→#74, #85→#95, Ur review F2, #147→#167 |
| 4 | Review depth follows the layer, not the size | #144: 11-26 F5, F7, F13, F16, F25, F26; #150, #174 |
| 5 | Review findings rejected or left open without an issue | #175 review F3; Go/TS review F2, F3 |

## Root causes

### 1. No step reviews a plan's decisions

`ox-plan` step 16 forbids reviewing the plan. `ox-review` step 3 makes the
plan's Decisions "the requirements" the code is checked against. The top-level
agent's check is "a quick look ... without redoing the planning." So a wrong
decision in a plan passes plan, work, review, verify, and Coral by
construction, because each gate takes the plan as true.

- **11-27 F1** (#187). The report-delivery plan
  (`2026-11-25-child-report-delivery.md`) chose to clear `toParent` as a stand-in
  for "being deleted," and restore it when the delete failed. That encoded one
  fact in two places: `deleting` on the goroutine, `reporting == nil` under
  `Server.mu`. It held only because no reporter had a worktree. The top-level
  sessions plan (`2026-11-27-agent-top-level-sessions.md`, lines 122–124)
  extended the same sentinel to sessions that own worktrees, which made a
  failed delete reachable and drop reports.
- **11-27 F2** (#188). The same plan chose publish-first on purpose (lines
  60–67), one day after #167 fixed exactly that defect for `create_session`.
- **11-24 F1, F2, F3.** The audit traced each one to an explicit plan
  decision: the page-errors plan's assumption about rejected fetches, the
  child-reports plan's "either outcome is valid," and the usage plan's reuse of
  the clock gate.

### 2. Invariants live in prose, so a new path breaks them unseen

Ox's lifecycle rules are written in `docs/architecture.md` and in field
comments ("Guarded by Server.mu", "Written only by run", "its builder until
run starts"). Eight fields carry ownership that only a comment states. Nothing
fails when a new path breaks one of these rules, and the race detector sees only
the interleavings some test happens to run. Every High finding in the last
three structural audits came in when a change added a second path beside a
sound first one:

- **#147 → 11-27 F2.** #167 made `addChild` own an unpublished child until its
  first message was accepted. `createTopLevel` (#175) was a new creation path,
  and it published first.
- **11-27 F3** (#189). #14 fixed tool labels by matching the `mcp__ox__` prefix.
  Ur's Ox tools (#168) arrive with bare names, and `toolInput.ts` was not a
  caller of anything #168 changed, so the review never reached it.
- **11-26 F23** (#155). #38 added global snapshot retries. #140 made loading
  per-session and kept the global retry.
- **11-26 F2, F9** (#147). The Ur process split (#133) added a second stop
  route and a second first-input route beside the existing ones.

### 3. A fix covers the reported path, not the class

`ox-plan` step 1 tells a plan for an audit finding to replace the structure "at
every place the finding names," which is a closed list. Report delivery took
five rounds: #71, #85, #110, #127, #187. The 11-25 structural audit described
each earlier fix: #74 "guarded the symptom," and #95 "changed the structure,
for the take only." The Ur process review's F2 fix added a kill on the exit
event, which made a second owner of command cleanup. The next day's sweep found
Delete racing that second owner (11-26 F2).

### 4. Review depth follows the layer, not the size

A change to the web client alone counts as "contained": Sonnet at medium
effort, no concern reviewers, a short plan. Tabs and splits (#144) added 3,630
lines and removed 1,413 across 39 files. Its review is 53 lines long and has one
finding. The next day's sweep found six defects in it (11-26 F5, F7, F13, F16,
F25, F26), and #174 followed. The Changes view (#100, #112) shows the same
pattern, with 11-26 F6, F14, F15, and F21. By count, the web client is the
largest cluster: about 19 of the 51 closed issues.

### 5. Review findings rejected or left open without an issue

The #175 review rejected F3 ("a session deleted before its first message
reports a shutdown"). That was a symptom of the publish-first structure that
became 11-27 F2. The Go and TypeScript review left F2 and F3 "Open. No issue,"
and they came back as #128 and #127. `AGENTS.md` ("Defects") offers broad
grounds for rejection, and nothing tracks a finding marked Open.

## Clusters

- **By structure.** The most severe and most repeated cluster is a fact that
  two owners need, `Server.mu` and the session goroutine: `deleting`, the `told`
  mark (#71), configuring in flight (#128), and report eligibility (#85, #110).
  The rule "a session's state is owned by its goroutine" pushes facts onto the
  goroutine that cross-session decisions under `Server.mu` must read. The
  architecture table already lists `deleting` under `Server.mu` with the note
  "written only by the session goroutine." That is the seam.
- **By layer.** Severity concentrates in `internal/server` lifecycle (create,
  delete, archive, reports). Volume concentrates in the web client's layout.
  Next come Ur's process lifecycle and the Claude driver's settings and exit
  reasons.
- **By kind of change.** Large features that add a second path or surface: #133,
  #144, #168, #175, and #100/#112. Second come fixes scoped to one path.

Blame data from the parallel design-quality measurement (`/tmp/oxdesign`)
agrees:

- 41 of 54 `bug` issues came from audits.
- 21 of 54 are session lifecycle, and 8 are one fact kept in two places.
- 5 of 39 bug-fix commits needed a later fix themselves.
- The share of commits that a fix rewrote within 48 hours is flat from Nov 22
  to 25, at 20–40%.
- The most-blamed commits are #66 (optimistic actions: rewritten by 9 fix PRs,
  including #95, #117, #126, #130, #161, #167, and #211) and #64 (child reports:
  #74, #95, #130, #167). Both introduced lifecycle state that more than one
  owner reads.
- `internal/agent/agent.go` changed together with `server/session.go` 19
  times. `protocol.ts` and `ox.tsx` changed together with `server/methods.go`
  15–16 times: the protocol is written twice, in Go and TypeScript.

## Instructions

The plans cite and apply the principles in `AGENTS.md`. They fail where
principles conflict at a concrete decision. The #175 plan satisfied "decide
each rule in one place" (one shared `createTopLevel`) and broke the builder
rule (own the session until its first message is accepted). The abstract text
gives no way to choose between them, and the concrete invariant was not written
anywhere a planner reads before deciding. More prose would not have changed
either decision. A test would have caught both.

## Mechanical versus process

| Defects | Mechanical guard |
| ------- | ---------------- |
| 11-27 F1, F2; #147, #127, #110, #85, #71, #128 | Stateful property test of server lifecycle (below) |
| `deleting`, reporting, and other comment-owned fields | Lock annotations checked by `checklocks` (gVisor) |
| 11-26 F24, #109, #27 (F12, F13) | Closed outcome type at the boundary, held exhaustive by the `exhaustive` / `gochecksumtype` linters already enabled |
| #189, #14 | One table of Ox tool names; a test that every harness's form renders |
| #150, #174, 11-26 F7, F13, F25 | One reachability spec over viewports and split layouts |
| 11-26 F11 (#153) | Open a fixture database from each older schema |

Process alone has to cover plan decisions (cause 1), review depth (4), and
open findings (5).

## Recommendations, ranked by effect per cost

1. **A stateful property test for server lifecycle.** Drive the JSON-RPC test
   client with the fake harness through random sequences of create (child and
   top-level, with and without a worktree, with a launch that fails), message,
   finish, archive, unarchive, and delete (clean and dirty worktree). After each
   step, check that:
   - every report is delivered once, or dropped only because its session was
     deleted;
   - a failed create leaves no row, broadcast, worktree, or branch;
   - no published session lacks its first message;
   - a refused delete changes nothing.

   This reproduces 11-27 F1 and F2 and the five report-delivery rounds. It also
   covers any future creation or deletion path, because each new path joins the
   generator. Cost: one test file in `internal/server`, using the existing test
   client and fake harness.
2. **Make review check the plan.** In `ox-review`, the plan's Goal and the
   user's answers stay requirements. Its Decisions become claims that the
   Construction reviewer re-derives on its own: name the owner of each state,
   and list every other path that creates or ends the same state. Cost: one
   paragraph in the skill.
3. **Size review depth by diff, not layer.** A change over a few hundred
   non-test lines gets concern reviewers and Opus, whatever its layer. Cost: one
   rule in `AGENTS.md` and `ox-review`.
4. **Fix the class, not the path.** A plan for a defect lists every path that
   produces the same state (`grep` for the sibling constructors and stops),
   routes them through one function, and adds the case to the property test
   from (1). Replaces "every place the finding names" in `ox-plan` step 1.
5. **Lock annotations on `session` and `Server`.** Add `checklocks`
   annotations and run it in `make test` and CI. Turns the eight ownership
   comments into compile-time checks. Cost: one analyzer in the build and
   annotations on eight fields. The "written by run under the lock, read by run
   without it" fields may not fit its model and may need splitting, which is
   the point.
6. **A layout reachability spec.** One Playwright spec that walks phone and
   desktop widths across one, two, and nested split layouts, and asserts that
   every tab control and panel toggle is in the viewport and not covered.
7. **Every Open or Rejected-then-reproduced review finding becomes an issue.**
   One line in `ox-review`.
8. **Generate `protocol.ts` from the Go protocol types.** Removes the second
   copy of every message shape and setting, which accounts for the 15–16
   co-changes and part of the "one fact in two places" cluster (#152, #193).
   Cost: a generator dependency, a build step, and replacing the hand-written
   types. Ranked last because the defects it prevents are cheaper ones.

Leave `AGENTS.md` at its length or shorter. Items 2, 3, 4, and 7 replace
existing sentences rather than adding sections.
