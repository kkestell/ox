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
