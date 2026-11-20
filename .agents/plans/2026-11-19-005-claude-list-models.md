# Claude calls `list_models`

## Goal

Task 2 of Phase 1 in `.agents/scratch/agent-tools.md`. Every Claude process a
session starts, resumes included, registers `ox-mcp` as its `ox` MCP server,
so Claude can call `mcp__ox__list_models` and get the catalog Ox offers. The
call runs without an approval prompt in every permission mode Ox offers. The
user's own MCP servers keep loading. `internal/agent` hands each harness the
forwarder's command, its environment, and the tools that run without
approval. The fake harness ignores them.

## Related code

- `internal/agent/agent.go` — `Options`, what every harness receives at
  `Start`.
- `internal/server/session.go:102` — `session.start` builds `agent.Options`
  on the session goroutine; the only place a harness is started.
- `internal/server/server.go:44` — `New`, which the forwarder and socket
  paths reach the session through.
- `internal/server/mcp.go` — the `list_models` tool definition
  (`listModelsTool`) served by `tools/list`.
- `internal/claude/claude.go:47` — `startProcess` builds Claude's flags for
  both a new conversation and a resume; `internal/claude/agent.go:50` `Start`
  calls it.
- `cmd/oxd/main.go` — resolves flags and builds the server; nothing resolves
  `ox-mcp` yet.
- `cmd/oxd/main_test.go` — `TestSecondInterruptStopsShutdown` re-executes the
  test binary as `oxd` from Go's build directory, which has no `ox-mcp`.
- `internal/server/live_test.go` — `TestLive` serves the page client over
  `net.Pipe`, with no listener for a forwarder to reach.
- `internal/claude/claude_test.go` — `standIn` and
  `TestClaudeStartsWithoutAnAPIKey`, the pattern for observing what Claude is
  started with.
- `cmd/ox-mcp/main_test.go` — `TestForwarder` checks `tools/list`.
- `Makefile`, `cmd/ox/web/e2e/global-setup.ts`, `cmd/ox/web/dev.mjs` — every
  build already puts `ox-mcp` beside `oxd`.

## Decisions

- **What a harness receives.** `agent.Options` gains `MCP agent.MCPServer`:

  ```go
  // MCPServer is Ox's MCP server for one session: a stdio command the
  // harness registers for its agent.
  type MCPServer struct {
  	Command  string            // absolute path of the forwarder
  	Env      map[string]string // the forwarder's environment
  	Approved []string          // tools that only read, which run without an approval prompt
  }
  ```

  Ox decides which of its tools skip approval; each harness maps that to its
  own mechanism. The fake harness ignores the field.
- **Where the paths come from.** `cmd/oxd` resolves `ox-mcp` as the file
  beside its own executable (`os.Executable`, then `filepath.EvalSymlinks`),
  checked with `exec.LookPath` on that path, and refuses to start without it:
  `log.Fatalf("ox-mcp not found beside oxd: %v", err)`, before opening the
  database. Without it Claude would run with the `ox` server silently
  `failed`. `cmd/oxd` makes the socket path absolute with `filepath.Abs`,
  since the forwarder runs in the workspace directory, and uses that path for
  both `listen` and the server. `server.New` gains a last parameter
  `mcp MCP`, `type MCP struct{ Forwarder, Socket string }`; tests that start
  no forwarder pass `MCP{}`.
- **Built per start.** `session.start` sets
  `MCP: agent.MCPServer{Command: s.mcp.Forwarder, Env: {"OX_SOCKET": s.mcp.Socket, "OX_SESSION_ID": sess.id}, Approved: approvedTools}`.
  `approvedTools` lives in `mcp.go` beside the tool it names:
  `[]string{"list_models"}`. The environment goes in the MCP config, not
  Claude's own environment, so Claude's tools do not see it.
- **Claude's flags.** `startProcess` takes `mcp agent.MCPServer` and, on every
  launch, adds
  `--mcp-config <{"mcpServers":{"ox":{"command":…,"args":[],"env":{…}}}}>`
  and `--allowedTools=<mcp__ox__ + each Approved tool, comma-separated>`. The
  `=` form keeps the variadic flag from taking later arguments. No
  `--strict-mcp-config`, so the user's servers still load.
- **Plan mode.** Claude ignores `--allowedTools` for an MCP tool in `plan`
  mode and asks with `decision_reason_type: "mode"`, unless the tool's
  `tools/list` entry carries `annotations: {"readOnlyHint": true}`.
  `listModelsTool` gains that annotation. `readOnlyHint` alone does not skip
  the prompt in `manual`, so both are needed.
- **Requirements:** **Permission requests** and **Claude's workspace
  access** hold: `list_models` reads only Ox's catalog, so it raises no card
  in any mode. **Tool calls**: the call shows as `mcp__ox__list_models`, often
  after a `ToolSearch` call that loads it, like any other tool. **Resuming
  the conversation**: the flags are passed again on resume, so a parked
  session's next process has the tool. **Settings shown are settings used**
  holds: the new flags change no model, effort, mode, or account. **Local
  access only** holds: no new listener. One requirement is added (below).
- **Settling:** no new wait in Ox. Claude answers `initialize` without
  waiting for MCP servers, and holds its first turn until they connect; the
  forwarder attaches in milliseconds, and `oxd` answers MCP on the connection
  goroutine (Task 1), never on the session goroutine blocked in
  `claude.Start`. A forwarder that cannot attach leaves `ox` `failed` and the
  turn runs without the tool.
- **Process I/O:** `ox-mcp` is Claude's child on Claude's own pipes. When
  Claude exits or is killed, the forwarder's stdin closes and it exits. Ox's
  existing read deadline already covers a child holding Claude's output.
- **Ownership:** no new state, timer, retry, lock, or stored field. `s.mcp`
  is set once in `New` and only read.
- **Claude Code:** settled by live probes on Haiku with 2.1.329 against this
  tree's `oxd` and `ox-mcp`; recorded under Documentation updates.
  - `--allowedTools=mcp__ox__list_models` (and a comma-separated list in one
    `=` flag) pre-approves the one tool in `manual`, `acceptEdits`, and
    `auto`; without it, `manual` raises `can_use_tool` with `mcp_server` and
    `display_name`.
  - `plan` needs `readOnlyHint` as above.
  - Inline `--mcp-config` adds `ox` with `source: "dynamic"`; user, plugin,
    and claude.ai servers still load. `init.mcp_servers` reports `ox`
    `connected`, or `failed` for a missing command or a refused attach.
  - The `initialize` response does not wait for MCP servers; the first turn
    waits for them (a forwarder delayed 6 s delayed the first `init` to 7 s).
  - MCP tools are deferred: Haiku usually calls `ToolSearch`
    `select:mcp__ox__list_models` first, with no approval; a resumed
    conversation that already loaded it calls it directly.
  - A resume with the flags passed again reconnects `ox` and the call works.
- **Trust boundaries:** the socket path and session ID appear in Claude's
  argv; neither is a credential, and the socket's directory stays 0700.
  `ox-mcp` is resolved only beside the running `oxd`, not from `PATH`.

## Check your assumptions

1. **Claude can look up Ox's models on its own, without asking.**
   - **Plan:** Claude can call an Ox tool that lists the harnesses, models,
     effort levels, and permission modes Ox offers. It runs in every
     permission mode, plan mode included, with no approval card; the
     transcript shows the call like any other tool call. This is recorded as
     a new requirement (text under Requirement changes).
   - **Alternative:** the call raises an approval card like other tools in
     prompting modes. The user would have to approve a harmless read each
     time a later agent tool, such as creating a child session, needs the
     list.

## Test plan

- **Claude is started with the `ox` server and its pre-approval, new and
  resumed** — Claude harness, new `TestClaudeRegistersOxMCP` in
  `internal/claude/claude_test.go`. No existing test records Claude's
  arguments. A `standIn` script writes `"$@"` one per line to a file;
  `startProcess` runs with `resume` false and then true, with an
  `MCPServer{Command: "/x/ox-mcp", Env: {"OX_SOCKET": "/s", "OX_SESSION_ID": "id"}, Approved: ["list_models"]}`.
  Each run's arguments hold `--mcp-config` followed by JSON whose
  `mcpServers.ox` has that command and environment, and
  `--allowedTools=mcp__ox__list_models`. Fails before the change.
- **`list_models` is annotated read-only** — server, `TestForwarder` in
  `cmd/ox-mcp/main_test.go`: the `tools/list` assertion also checks
  `annotations.readOnlyHint` is `true`.
- **`oxd` refuses to start without `ox-mcp` beside it** — new
  `TestRefusesToStartWithoutForwarder` in `cmd/oxd/main_test.go`: runs
  `os.Args[0]` with `OXD_TEST_MAIN=1` and temporary `-socket`, `-db`, and
  `-config` paths from Go's build directory, which holds no `ox-mcp`; it
  exits nonzero with `ox-mcp` on stderr, and no socket appears. No existing
  test starts `oxd` without one. `TestSecondInterruptStopsShutdown` copies
  `os.Args[0]` to `bin/oxd` beside an empty executable `bin/ox-mcp` and runs
  that copy, since its stand-in Claude never starts the forwarder.
- **A session in a prompting mode calls `list_models` with no approval** —
  live, in `TestLive`. Setup: build `./cmd/ox-mcp` into a directory from
  `os.MkdirTemp("", "oxlive")` (short enough for a socket path), listen on a
  Unix socket there, `go s.Serve(listener)`, and pass
  `MCP{Forwarder, Socket}` to `New`; the page client keeps `connect`. The
  direct `claude.Start` in "a failed start reports Claude's reason" passes
  the same `MCPServer` with any session ID, so the missing conversation is
  still the reason. New step before "pending schedules": for `manual` and
  `plan`, create a session and run `turn` with "Call the
  mcp__ox__list_models tool now, then reply with just ok." and an answer
  that fails the test on any approval. The tools include
  `mcp__ox__list_models`, and the session's received updates hold a
  `tool_result` that is not an error and contains `claude-haiku-5-5`.
- **A parked session calls `list_models` after resuming** — live, in
  `TestLive`, after the existing `time.Sleep(parkAfter + 5*time.Second)`: the
  `manual` session has parked, and the same turn on it passes the same
  checks.
- `make e2e` and `approval.spec.ts` need no change: their `oxd` already has
  `ox-mcp` beside it.

## Implementation plan

1. `internal/agent/agent.go`: `MCPServer` and `Options.MCP`.
2. `internal/server/mcp.go`: `approvedTools`, and `annotations` on
   `listModelsTool`.
3. `internal/server/server.go`: `MCP`, the `mcp` parameter of `New`, and the
   `s.mcp` field. `internal/server/session.go`: set `Options.MCP` in `start`.
   Update every `New` caller: `internal/server/methods_test.go`,
   `internal/server/live_test.go`, `cmd/ox-mcp/main_test.go`.
4. `internal/claude/claude.go`: `startProcess` takes `mcp agent.MCPServer`
   and adds the two flags; `internal/claude/agent.go` `Start` passes
   `options.MCP`. Existing `startProcess` callers in tests pass
   `agent.MCPServer{}`.
5. `cmd/oxd/main.go`: resolve `ox-mcp`, make the socket absolute, pass
   `server.MCP`.
6. Tests above.
7. Documentation and requirement updates below.

## Requirement changes

- **Added**, after **Questions from Claude**:
  **Claude can list Ox's models.** Claude can look up the harnesses, models,
  effort levels, and permission modes Ox offers, in any permission mode,
  without asking for approval.

## Documentation updates

- `docs/claude-code.md`:
  - Header: observed through 2.1.329.
  - Persistent session: replace the `--allowedTools mcp__<server>` bullet
    with per-tool `--allowedTools=mcp__<server>__<tool>` (comma-separated in
    one flag), which `plan` mode ignores for an MCP tool unless its
    `tools/list` entry has `annotations.readOnlyHint: true`, and
    `readOnlyHint` alone does not skip the prompt in `default`. Inline
    `--mcp-config` adds to the user's servers, which keep loading without
    `--strict-mcp-config`; an added server reports `source: "dynamic"`.
  - Initialize first: the response does not wait for MCP servers; the first
    turn waits for them to connect.
  - `system`: `init.mcp_servers` entries are `{name, status, source}`, with
    `status` `connected`, `failed`, or `needs-auth`; a server whose command
    is missing or whose `initialize` fails is `failed`, and Claude runs
    without its tools.
  - Section 5, `can_use_tool`: an MCP tool's request carries `mcp_server`
    `{name, source}` and `display_name`; a refusal by mode carries
    `decision_reason_type: "mode"`.
  - Persistent session: MCP tools are deferred, and the model loads one with
    `ToolSearch` `select:<name>`, which needs no approval, unless the resumed
    conversation already loaded it.
- `docs/protocol.md`: Connection and messages — `oxd` and `ox-mcp` are built
  into one directory, as `make dev` does into `bin/`, and `oxd` refuses to
  start without `ox-mcp` beside it (replacing `go run ./cmd/oxd`). Tools —
  `list_models` is annotated `readOnlyHint`.
- `docs/architecture.md`: Agents — each harness receives Ox's MCP server
  (`ox-mcp`, its environment naming the socket and session, and the tools
  that run without approval); The Claude agent — registers it as `ox` on
  every launch and pre-approves those tools.
