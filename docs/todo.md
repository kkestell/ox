# Todo

Planned work, ordered by priority: unfixed audit findings, then gaps in features
Ox already supports. Each finding links its section in the audit, which holds
the evidence and the correction.

## Defects

- [ ] **Tests that pass when their behavior breaks.** Six tests, from the
      settings passed to the next start to `TestLive`'s ordering.
      [Deslop F8](../agents/audits/2026-11-20-002-deslop.md#f8-tests-that-pass-when-the-behavior-they-name-breaks)

## Sidebar

- [ ] **Collapse a session's children.** A session with children gets a
      disclosure control that hides and shows them. Decide whether the collapsed
      state survives a reload.
- [ ] **Newest sessions first.** List each workspace's top-level sessions newest
      first. Decide whether children follow, or stay oldest first so a plan,
      work, and review read in order. Changes **Session order**.

## Child sessions

- [ ] **Consistent child titles.** Agents title children inconsistently
      ("Implement" and "Work" for the same stage, "Plan F1: …" beside "Plan:
      …"), and the sidebar truncates the topic that tells siblings apart. Give
      `create_session`'s description in `internal/server/mcp.go` a title
      convention.

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
