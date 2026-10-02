# Delete subagents

## Plan

`agents/plans/2026-10-01-001-delete-subagents.md`

## Summary

Subagents are removed. The model receives the seven workspace and shell tools,
sessions have no child sessions or subagent messages, shell processes belong to
the session without per-agent scoping, and the client holds at most one pending
permission request. The plan's goal is met.

## Departures from the plan

- The TUI's nameless-call rendering in `crates/ox/src/tui/transcript.rs` stays.
  Replayed turn errors (`convert::turn_error_update`) are also nameless tool
  calls, so the branches still have a caller. Their comments now name turn
  errors, and the tests use a nameless "Turn error" call in place of the
  subagent answer.
- The shell tool's "that you started" wording stays in its messages and schema
  descriptions; it is still accurate for the session's own processes.

## Decisions

- The OpenAI fixture's `calls_reply` and `wait_for_requests`, the OpenRouter
  fixture's `requests_for`, `wait_for_requests`, and `route_key`, and the
  `prompt.rs` harness's `stored_child` were used only by subagent tests and are
  deleted with the routing.
- The shell process test that checked one agent could not see another's process
  is deleted; the registry now belongs to one session.

## Automated checks

- `make check` — Passed.
- `make e2e` — Passed (12 tests).

## Manual verification

1. A live headless run receives only the workspace and shell tools.

   ```sh
   export OPENROUTER_API_KEY=$(grep -E '^OPENROUTER_API_KEY=' .env | cut -d= -f2-)
   python3 scripts/run.py "List the tools you have by name, one per line, then stop."
   ```

   The model listed `shell`, `shell_process`, `read_file`, `glob`, `grep`,
   `write_file`, and `edit_file`.
