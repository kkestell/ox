# Track Changed Files and Publish Tool Locations

## Sources

- `eng/roadmap.md#changed-file-accounting` — owns the build scope and restart,
  replay, deduplication, failure, and ACP location gates.
- `eng/architecture.md#session-and-turn-state` and
  `eng/architecture.md#workspace-boundary` — own durable session state,
  checkpoint recovery, canonical roots, and confined paths.
- `internal/agent/state.go`, `internal/agent/loop.go`, and
  `internal/agent/adapter.go` — current tool-result records, fold and checkpoint
  projections, primary and delegated tool execution, live updates, and replay.
- `internal/acp/types.go` and
  `~/src/references/repos/third-party/protocol/agent-client-protocol/schema/v1/schema.json:$defs.ToolCallLocation`
  — current ACP wire types and the absolute-path `locations` contract for tool
  starts and updates.
- `~/src/references/repos/personal/beta/src/workspace.rs:ChangeTracker` and
  `src/tools.rs:write_file_records_the_workspace_relative_path`,
  `write_file_does_not_record_a_failed_write`, and
  `edit_file_records_the_edited_path` — prior art for canonical relative keys,
  successful-mutation recording, sorting, and deduplication.

## Goal

Keep a durable, deduplicated set of files successfully changed by a session and
identify each confined write or edit target through standard ACP tool-call
locations, both live and during `session/load` replay.

## Implementation

- `internal/acp/types.go` and `internal/acp/types_test.go` — add
  `ToolCallLocation` and `locations` to tool starts and updates with the ACP v1
  JSON shape. Preserve omission when no confined target exists.
- `internal/agent/loop.go`, `internal/agent/event.go`, and
  `internal/agent/adapter.go` — normalize the `path` argument of edit-kind tools
  once through the session workspace. Carry the workspace-relative target
  through pending, permission, completion, and delegated-child paths. Publish
  its canonical absolute form on the initial `tool_call` and permission update;
  rejected and execution-failed calls still retain a valid target location but
  do not become changed files.
- `internal/agent/state.go` — persist the normalized target for suspended calls,
  completed primary results, and delegated child results so recovery and replay
  do not reinterpret raw paths. Fold successful edit-kind results into a sorted,
  deduplicated changed-file set. Add that set to cloning, checkpoint projection
  and validation, and restore it beside the existing usage, cost, and
  context-capacity state. Replay the stored target as an absolute ACP location
  for primary and nested tool calls.
- `eng/roadmap.md` — remove the completed changed-file-accounting slice while
  leaving the remaining context-and-diagnostics work in order.

## Tests

- `internal/agent/state_test.go` — prove repeated writes deduplicate, create and
  edit targets survive checkpoints and tail folding, failed and rejected calls
  are excluded, delegated edits count toward the parent session, and replay
  preserves the stored locations.
- `internal/agent/agent_test.go` and `integration/agent_loop_test.go` — cover
  confined location derivation for primary and subagent calls, omission for an
  invalid escape, successful versus failed accounting, and exact live/replay
  location parity after reactivation.
- `internal/e2e/filesystem_test.go` and `internal/e2e/session_test.go` —
  exercise local and delegated create, write, edit, rejection, and failure
  through the real process; restart it and verify the durable set and absolute
  locations without duplicate paths.
- `internal/e2e/browser/lifecycle.spec.ts` — keep the pinned client's existing
  visible edit workflow and assert in the traffic monitor that the edit
  `tool_call` carries the canonical absolute workspace path.

## Decisions

- Persist slash-separated workspace-relative targets for stable durability and
  construct ACP's required absolute locations from the canonical session root.
- Port Beta's successful native-write accounting and set behavior. Do not port
  its shell completeness flag or final trace envelope: this slice makes no claim
  that arbitrary shell mutations are attributable, and Ox exposes the requested
  file identity through ACP tool locations.
