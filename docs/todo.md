# Todo

Planned work, ordered by priority: unfixed audit findings, then gaps in features
Ox already supports. Each finding links its section in the audit, which holds
the evidence and the correction.

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

## Web client

- [ ] **Consistent client state.** Audit the state the web client keeps, such as
      panel sizes, collapsed sessions, and open files, and decide in one place
      which of it survives a reload and where it is stored.
