# Ox: Pi as a second agent

Notes, November 13, 2026.

## Objective

Ox runs Pi sessions as well as Claude Code sessions. The app offers whichever
of the two harnesses the user has installed:

- Neither installed: the New Session button is disabled.
- One installed: it is the only agent, and the New Session menu shows no agent
  select.
- Both installed: the agent select offers Claude and Pi.

Pi reaches `oxd` through a new ACP v2 bridge, `crates/pi-acp`, built in the
manner of `crates/claude-code-acp`.

## Research

- [Pi harness notes](../../docs/research/harnesses/pi.md), especially:
  - section 12, control from outside: RPC mode and commands;
  - section 13, status from outside: `agent_end` versus `agent_settled`;
  - section 18, skills: `/skill:name` and `get_commands`;
  - section 19, ACP adapters: the survey of existing Pi adapters, and how Pi's
    RPC maps onto Ox's v2 turn model.
- Pi's own docs:
  [rpc.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc.md),
  [rpc-commands.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc-commands.md),
  [json.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/json.md),
  [session-format.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/session-format.md),
  [extensions.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md).
- [ACP v2 support](../../docs/acp-v2.md) and [architecture](../../docs/architecture.md):
  what `oxd` requires of an agent, and how `claude-code-acp` meets it.

Most of the harness notes were recorded against Pi 1.4.0. The installed Pi is
1.5.2, a managed install at `~/.local/bin/pi`. Recheck anything a task relies on
against 1.5.2.

## Decisions

- **Our own bridge.** No existing adapter speaks ACP v2. What `oxd` needs of a
  v2 agent is the turn model:
  - the prompt is answered with its `messageId` when it enters the
    conversation;
  - `user_message` and `state_update` updates drive the turn;
  - `session/resume` with `replayFrom: start` replays the conversation.

  The v1 adapters answer prompts at turn end, so a fork would rewrite its core
  and jump several major versions of the ACP crate. Two adapters serve as
  reference code, not forks:
  - [pi-acpinator](https://github.com/ahmadaccino/pi-acpinator) (Rust, MIT):
    its Pi RPC client and event types, and its permission-gate extension;
  - [svkozak/pi-acp](https://github.com/svkozak/pi-acp) (TypeScript, MIT): its
    tool-call and diff translation.

  Both use fixed timeouts, which `AGENTS.md` rules out.
- **Drive `pi --mode rpc`**, one Pi process per session, launched with
  `--approve` so project settings, skills, and extensions load.
- **No permission requests yet.** They can come later as a bundled Pi
  extension, whose `tool_call` handler calls `ctx.ui.confirm`, which reaches
  the bridge as an `extension_ui_request`.
- **Extension prompts become form elicitations now.** With `--approve`, project
  extensions can send `extension_ui_request` (confirm, select, input, editor).
  The bridge maps them to ACP form elicitations, which `oxd` already supports,
  and cancels the elicitation when Pi withdraws the request. A turn never waits
  on a request nobody can answer. Permissions will later reuse this path.
- **Smoke model:** `openai/gpt-6-luna`.

## Tasks

Task 1 depends only on the binary's name, `pi-acp`, and can run alongside the
others. Tasks 2 through 6 run in order, since 3, 4, and 5 build on 2 and share
its session and update code.

### 1. Harness detection and agent choice

Files: `crates/ox/src/lib.rs`, `ui/src/SessionList.tsx`, `ui/e2e`,
`docs/architecture.md`.

- Detect `claude` and `pi` on the `PATH` of the environment the app passes to
  sessions (`utf8_env()`), since the bridges spawn them from that `PATH`.
- New Session is disabled with neither, has no agent select with one, and
  offers both otherwise.
- Add `Agent::Pi` to `new_session`, launching `pi-acp` from beside the app's
  executable as `Agent::Claude` launches `claude-code-acp`.
- Installing or removing a harness while the app runs shows up without a
  restart, by re-detecting on an event such as window focus, not by polling.
- e2e tests cover none, one, and both. They control what is installed through
  the `PATH` they launch the app with, for example a directory of fake
  executables.
- Checks: `make check-rust`, `make check-ui`, `make e2e`.

Unknown: what `PATH` the app sees when launched from Finder rather than a
terminal. Detection reports what the sessions would actually find, so a narrow
`PATH` shows up as "not installed".

### 2. pi-acp core

Files: new `crates/pi-acp`, the workspace `Cargo.toml`, and the Makefile's
`dev` build.

- A Pi RPC client over the child's stdin and stdout (JSONL, split on LF only).
- `initialize`, advertising `sessionCapabilities`.
- Session new starts `pi --mode rpc --approve` with a Pi session ID the bridge
  records.
- `session/prompt`: the bridge sends Pi's `prompt`. Once Pi accepts it
  (`started` or `queued`), the bridge answers with the user message's
  `messageId` and sends `user_message`.
- `state_update`: running on `agent_start`, idle with a stop reason on
  `agent_settled`. `agent_end` comes before retries, so it does not end the
  turn.
- Streamed text and thinking; tool calls with kinds and locations, and diffs
  for `edit` and `write`.
- `session/cancel` sends Pi's `abort`; `session/close` stops Pi.
- A Pi that exits fails the open turn, and the next prompt starts Pi again.
- The bridge reports `background: []`, so idle sessions park.
- Protocol tests in `crates/pi-acp/tests` run the bridge against a fake `pi`
  script, as `crates/claude-code-acp/tests/protocol.rs` does with a fake
  `claude`.

### 3. Resume and replay

- Map each ACP session to its Pi session, persisted as `claude-code-acp` does
  with `acp-sessions.json`.
- `session/resume` with `replayFrom: start` replays the conversation from Pi's
  session file, then sends the state, then answers.
- Restarting Pi after an exit or a park continues the same Pi session.

### 4. Models, thinking levels, and commands

- Pi's models (`get_available_models`, `set_model`) and thinking levels
  (`get_available_thinking_levels`, `set_thinking_level`) become session config
  options.
- `get_commands` becomes `available_commands_update`. Skills are listed and
  invoked as `/skill:name`.

### 5. Extension prompts as form elicitations

- Map Pi's `extension_ui_request` confirm, select, input, and editor prompts to
  form elicitations, and send the answer back as `extension_ui_response`.
- Pi withdrawing a request cancels its elicitation; a cancelled or declined
  elicitation answers Pi accordingly.

### 6. Smoke check and docs

- Parameterize `scripts/smoke-acp.py` by agent, and add `make smoke-pi`
  (`openai/gpt-6-luna`) beside `make smoke-claude`.
- Add a `pi-acp` column to `docs/acp-v2.md`.
- Describe the bridge in `docs/architecture.md` and `README.md`.
- Add `smoke-pi` to the commands that `AGENTS.md` names for changes to the
  bridges.

### Checks for tasks 2 through 6

`make check-rust`, then `make check-scripts` once the smoke script changes,
`make smoke-claude` whenever `oxd`, `ox-protocol`, `oxctl` or the smoke script
changes, and `make smoke-pi` from task 6 on.
