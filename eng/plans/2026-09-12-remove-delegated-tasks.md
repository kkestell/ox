# Remove Delegated Tasks

## Goal

Remove the unused delegated-task queue and child-agent runtime.

## Implementation

- Delete the task queue tools, durable task records, child configuration, and
  child request loop. Keep the parent agent's ordinary tool loop unchanged.
- Remove queue and subagent behavior from the specification, architecture, todo
  list, README, review targets, and workspace map.
- Remove queue and child-agent tests. Update shared test fixtures and request
  accounting for the parent-only model loop.

## Tests

- Run the affected agent, tools, integration, and end-to-end tests.
- Run `make check` after the focused tests pass.
