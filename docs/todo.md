# Todo

Planned work, ordered by priority: unfixed audit findings, then gaps in features
Ox already supports. Each finding links its section in the audit, which holds
the evidence and the correction.

## Defects

- [ ] **A slow child start stalls unrelated sessions.** Starting the harness and
      handing it the first message hold the lock other sessions need to receive
      requests.
      [Audit F2](../agents/audits/2026-11-22-001-conversation-launch-and-layout.md#f2-a-childs-first-send-holds-the-daemons-membership-lock)
- [ ] **Dragging the file split can collapse the transcript.** When the side
      panels leave less than 480 px for the center, dragging in either direction
      chooses the same split and can shrink the transcript to a sliver.
      [Audit F3](../agents/audits/2026-11-22-001-conversation-launch-and-layout.md#f3-the-file-split-assumes-space-the-side-panels-can-take-away)
- [ ] **Dev shutdown masks daemon exit failures.** The dev stack reports an
      ordinary Ctrl-C even when `oxd` terminates with a race detector failure.
      [Audit F4](../agents/audits/2026-11-22-001-conversation-launch-and-layout.md#f4-the-dev-stack-decides-its-exit-status-before-services-stop)
- [ ] **Rewrite the web client tests to Playwright best practice.** Specs find
      elements by CSS class and structure, assert styling and pixel arithmetic,
      sleep for fixed times, share one daemon per file, and pack many behaviors
      into one test; the page lacks the roles and names role-based locators
      need. [Audit](../agents/audits/2026-11-22-003-frontend-tests.md)

## Workflow

- [ ] **Code reaches main without review.** 7446420, 64d1e65, and 44cab72 were
      committed unreviewed on 2026-11-22, and 2125f54 and a42b498 before them.
      Have the pre-commit hook refuse a commit that changes `cmd/` or
      `internal/` unless it adds or updates a review in `agents/reviews/` whose
      verdict is Ready to commit.
- [ ] **The top-level agent works from prose alone.** No skill carries the
      workflow, so the coordinating agent decides for itself how to land work.
      On 2026-11-22 it cut worktrees around the main checkout's uncommitted
      work, stashed that work to fast-forward two branches past it, and removed
      a worktree holding an uncommitted rewrite of the phone test, which was
      lost. Write a skill that carries the Workflow steps of `AGENTS.md`: check
      `git status` first, keep one change in the checkout, land only clean
      worktrees, and sanity-check each result against "Correct by construction"
      and the test rules.
- [ ] **Reviews prescribe fixes.** The Codex tailnet review dictated a shadow
      struct and fixed-port socket tests, and the implementer built them word
      for word. Have each `/ox-review` finding state the defect and the rule it
      breaks, and leave the fix's design to `/ox-work`.
- [ ] **Reviewers share the implementer's blind spots.** Sol 6.2 planned,
      implemented, and reviewed the tailnet change, and the review passed its
      own prescriptions. The mobile review passed a phone test asserting
      padding, font sizes, and CSS transitions. Run `/ox-review` on a different
      model from `/ox-work`.
- [ ] **A session went quiet with work pending.** The mobile implementer started
      background test runs and resumed only after the user prompted it twice.
      Find whether Ox lost the background task's completion or the session never
      waited for it.
- [ ] **Agents leave processes and files behind.** An `oxd` probe ran for four
      days and a Vite server for a week after their sessions ended, and `/tmp`
      held about 140 agent leftovers. Have each skill keep its scratch in one
      temporary directory it removes, and stop every process it starts before
      its final response.

## Sidebar

- [ ] **Collapse a session's children.** A session with children gets a
      disclosure control that hides and shows them. Decide whether the collapsed
      state survives a reload.
- [ ] **Newest sessions first.** List each workspace's top-level sessions newest
      first. Decide whether children follow, or stay oldest first so a plan,
      work, and review read in order. Changes **Session order**.

- [ ] **Clip session titles at the panel edge.** Show each title in full and let
      the sidebar's right edge clip it, instead of ending it with an ellipsis.
      Behind the hover menu icon, a gradient from transparent on its left to the
      row's background on its right fades the title, so the icon does not blend
      into the text beneath it.

- [ ] **Idle dot for completed tool calls.** The transcript marks a completed
      tool call with a check icon (`.marker.done` in
      `cmd/ox/web/src/style.css`). Use the sidebar's idle status dot instead.

## Child sessions

- [ ] **Consistent child titles.** Agents title children inconsistently
      ("Implement" and "Work" for the same stage, "Plan F1: …" beside "Plan:
      …"), and the sidebar truncates the topic that tells siblings apart. Give
      `create_session`'s description in `internal/server/mcp.go` a title
      convention.

## Usage

- [ ] **Subscription usage under the composer.** Extend the harness interface
      (`internal/agent/agent.go`) so a harness can report subscription usage.
      The Claude agent reports the 5-hour and 7-day windows, each with its
      utilization and reset time, from
      [`rate_limit_event`](harnesses/claude-code.md#rate_limit_event). The web
      client shows them under the composer on Claude sessions. Decide the
      protocol shape in `docs/protocol.md`, and what a session shows before its
      first turn, when Claude has sent no event. Adds a requirement.

## Questions

- [ ] **One question per page.** A form with several questions shows them all in
      one scrolling container. Show one question at a time with Previous and
      Next buttons, and submit from the last. Changes **Questions from Claude**.

## Approvals

- [ ] **Allow always.** Decide whether to persist grants in the workspace or in
      Ox before exposing
      [Claude's persistent permission option](harnesses/claude-code.md#can_use_tool).

## Configuration

- [ ] **Write the default config on first start.** When `oxd` starts and finds
      no config file, it copies the built-in default into the config directory
      (`internal/paths` resolves it) instead of only using it in memory, so the
      user can begin editing it.
