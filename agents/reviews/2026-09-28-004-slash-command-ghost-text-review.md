# Slash command ghost text review

## Scope and coverage

Reviewed commit `31c9dd6`, its approved plan and work log, and the client,
server, and fake-server paths that supply and consume available commands. Used
correctness, testing, readability, simplicity, Rust ownership, and Rust idioms
lenses. The automated checks cover the client and fake server; this review did
not run an interactive session against the bundled ACP server.

## Findings

### Fixed

- **A complete command could suggest a longer command**
  (`crates/ox/src/tui/input.rs:55`, correctness): With both `model` and
  `model-fast` available, typing `/model` showed `-fast`, and Tab changed a
  complete command. The approved plan says a complete command shows no ghost
  text. Select the first matching command before checking whether it has a
  suffix, and cover overlapping names in the input test.
- **The load test could consume the creation update**
  (`crates/ox/src/acp.rs:441`, testing): The test created another session but
  left its command update queued. Its post-load assertion could pass without
  seeing the loaded session's update. Consume and identify the creation update
  before loading, then assert the next update belongs to the loaded session.

No open findings.

## Checks run

- `make check` — passed.
- `make e2e` — passed, including all ten tmux tests.
- `git diff --check` — passed.

The existing input test gained an overlapping-name case. The existing resume
test now distinguishes the creation and load updates. No test guarantee was lost
or moved.

## Verdict

Two findings fixed; no open findings.
