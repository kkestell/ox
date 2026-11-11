# ACP v2, part 1: claude-code-acp

## Goal

`claude-code-acp` speaks draft ACP protocol v2. It reports when Claude
starts and stops working with v2's standard `state_update` session update in
place of the `_ox/agent_state` extension, answers a prompt when the prompt
enters the conversation, and replays a saved session on `session/resume`.
`docs/acp-v2.md` charts ACP v2 and Ox's support for it.

This is the first of three plans, each implemented in its own session and
landed as its own commit:

1. `claude-code-acp` (this plan).
2. `oxd`, `ox-protocol`, `oxctl`, `oxd-fake-agent`, and the smoke script
   (`2026-11-11-002-acp-v2-oxd.md`).
3. The app, `crates/ox` and `ui/` (`2026-11-11-003-acp-v2-app.md`).

Between commits the stack is partly on v1 and partly on v2, and the checks
that span both halves fail until the plan that completes them lands. Each
plan names which checks it runs.

## Sequencing

Part 1 starts after the workspaces and saved sessions plan
(`.agents/plans/2026-11-11-004-workspaces-and-saved-sessions.md`) lands on `main`.

## Library

- `agent-client-protocol` stays pinned at `=5.2.0`, with the
  `unstable_protocol_v2` feature. Everything under `unstable_protocol_v2`
  may change in a minor release, so a version bump is its own change, made
  with the changelogs of `agent-client-protocol` and
  `agent-client-protocol-schema` open.
- Connections start with `Client.v2()` and `Agent.v2()`, and handlers take
  `v2::ConnectionTo`. Session updates and requests arrive through typed
  handlers on the connection.
- Every ACP type comes from `agent_client_protocol::v2::schema`. A v2
  connection accepts v1 message types without a compile error, so each part
  leaves no `agent_client_protocol::v1` path in the crates it migrates.
- The SDK's `simple_agent_v2` and `v2_one_shot_client` examples are the
  reference for the v2 lifecycle on each side.

## Protocol changes that shape the work

- `session/prompt` answers once the prompt is inserted into the
  conversation, with its `messageId`. Output and the end of the work arrive
  as `session/update` notifications, the end as a `state_update` of `idle`
  with an optional `stopReason`.
- `StateUpdate` is `running`, `idle`, `requires_action`, or another state
  the client does not know. A new session is idle until the agent says
  otherwise. `session/resume` sends the current state after any replay and
  before its response.
- v2 has no `session/load`. `session/resume` with `replayFrom: {type:
  "start"}` replays the retained conversation before answering. Supplying
  `sessionCapabilities` advertises the baseline methods `session/new`,
  `session/list`, `session/resume`, `session/close`, `session/prompt`,
  `session/cancel`, and `session/update`.
- The agent echoes each inserted prompt as a user message carrying the
  prompt's `messageId`.
- `StopReason` gains `error`, with optional details, and an open `other`
  form.
- Tool calls report through `tool_call_update` alone, with
  `tool_call_content_chunk` for streamed content. Messages and thoughts can
  arrive whole, as `agent_message` and `agent_thought` with a `messageId`
  that later updates patch, besides the chunk forms.
- Prompt capabilities move to `agentCapabilities.session.prompt`.

## Compatibility chart

The first step writes `docs/acp-v2.md`, a chart of ACP v2 and Ox's support
for it. Every later step, in all three parts, updates the rows it
implements in the same commit.

- Rows come from the v2 schema in the pinned
  `agent-client-protocol-schema`: every method and notification in each
  direction, every `session/update` kind, every capability in `initialize`,
  every content block type, and every `StopReason` and `StateUpdate`
  variant. Rows behind an `unstable_*` feature are marked unstable.
- Columns are the feature, `oxd` as the client, `claude-code-acp` as the
  agent, and caveats. Each cell is supported, partial, or unsupported, and
  a partial cell says in the caveats what works and what does not. Where
  `oxd` records an update the app does not show, the caveats say so.
- The chart starts with every cell unsupported. This part fills the
  `claude-code-acp` column; every capability it advertises is supported.
- The chart names the schema crate version it was built from. Bumping
  `agent-client-protocol` updates the chart's rows in the same change.
- `oxd-fake-agent` is a test fixture and has no column.

## claude-code-acp

- `Cargo.toml` enables `unstable_protocol_v2` and drops the `ox-protocol`
  dependency, which it uses only for `AgentState`.
- `main.rs` serves `Agent.v2()`. The `session/load` handler goes, and
  `session/resume` replays history from `history.rs` when `replayFrom` is
  start, then sends the current state, then answers. Any other cursor is
  rejected with invalid params.
- `session/close` is implemented, since `sessionCapabilities` advertises
  it: it stops the session's `claude` process and forgets its state.
- `session/prompt` answers when `claude` echoes the prompt under
  `--replay-user-messages`, which is the prompt's insertion. The bridge
  sends that echo as a `user_message` with a new `messageId` and answers
  with the same ID. The runner's `responding`/`responded` channel, which
  holds a turn's result until the prompt's response is sent, goes: the
  response and the end of the turn are independent.
- `runner.rs` reports `running` and `idle { stopReason }` as
  `SessionUpdate::StateUpdate` through `updates.rs`. A failed turn ends with
  `stopReason: error` and the failure in its details, where today it fails
  the prompt's response.
- Permission requests that block the turn report `requires_action`, and
  `running` once answered.
- Tool calls are sent as `tool_call_update` alone; the `tool_call` forms in
  `updates.rs` become the first `tool_call_update` for their ID. A tool
  call's result text and images, and the activity lines of a subagent it
  started, stay its content, sent as the whole content list on each update
  as today, live and on replay.
- `initialize` advertises `sessionCapabilities` with prompt capabilities
  under it.

## Tests

`tests/protocol.rs` reads `state_update` updates in place of
`_ox/agent_state`, and covers: the prompt answered with the echoed user
message's `messageId` before the turn's output ends; `idle` with
`end_turn`, `cancelled`, `max_turn_requests`, and `error`;
`requires_action` while a permission request is open; resume with replay
sending state before its answer; resume without `replayFrom` replaying
nothing; an unsupported cursor rejected; `session/close`.

## Docs

- `crates/claude-code-acp/README.md` describes the v2 mechanisms and links
  to `docs/acp-v2.md`.
- The `AGENTS.md` examples that name `loadSession` and `session/load` name
  `session/resume` and its replay.

## Verification

`make check` passes: `oxd` and the app still build against v1 and no longer
share a type with the bridge. `make smoke-claude` fails from this commit
until part 2 lands, because `oxd` still speaks v1; the report says so.
