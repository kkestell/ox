# Remove the fake server

## Plan

`agents/plans/2026-09-29-006-remove-fake-server.md`

## Summary

The fake-server crate is gone. Client unit and tmux tests now exercise the Ox
server against the scripted OpenRouter fixture, so the plan's goal is met.

## Departures from the plan

- Production lines fell from 11,423 to 10,684, a reduction of 739 rather than
  about 825. The deleted crate contributed 856 production lines; the expanded
  integration-test harness contributes 114 additional lines that `rsloc`
  classifies as production.

## Decisions

- The existing client guarantees remain in their owning tests. Fake-only
  pagination, late-permission, and uncategorized-option guarantees were removed
  as planned. Simultaneous permissions now come from real subagents, rendering
  runs real tools, and server failure kills the bundled ACP child process.
- `Gate` implements `Default` because enabling the fixture through
  `test-support` exposes its public constructor to the all-features Clippy run.

## Automated checks

- `make check` — Passed. The first run found the `Gate` Clippy lint; the rerun
  passed after the fix.
- `make e2e` — Passed all 12 tmux tests. The sandboxed run could not bind the
  concurrent loopback fixture servers; the unrestricted rerun passed.

## Manual verification

1. Measured the production-line change.

   ```sh
   rsloc crates
   ```

   Observed 11,423 lines before the change and 10,684 afterward, a net reduction
   of 739.
