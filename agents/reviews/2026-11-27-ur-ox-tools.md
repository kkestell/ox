# Review: Give Ur the Ox tools

## Scope and coverage

Reviewed the uncommitted changes in the `ur-ox-tools` worktree on base
`339e5ad` (`git merge-base origin/main HEAD`), with
`internal/ur/engine/mcp.go` and the plan untracked, against
`agents/plans/2026-11-27-ur-ox-tools.md` (issue #50). Settled by the user: Ur's
Ox tools never ask for approval in either mode; Ur uses `ox-mcp` rather than
dialing `oxd`'s socket; the plan's requirement texts.

Traced: `ur`'s startup of the forwarder and its handshake, dispatch in `next`
and `callOx`, the `calling` step against interrupts and late answers, `stop`
and the forwarder's end, `oxd`'s `serveConn` and `handleMCP` at a session stop
and at shutdown (`Server.Close` ends sessions while MCP connections stay open,
so a forwarder's stdin EOF always reaches a live `serveConn`), report
suppression through `_meta` in `session.report` and `told`, and the moved
`agent.Sessions` text against Claude's old prompt (same bytes).

The requirements diff matches the plan's Requirement changes word for word.
Docs in `docs/architecture.md` and `docs/protocol.md` match the code.

Gaps:

- `/ox-work`'s final response was not passed on, so its Tests proved and
  Checks lists were not compared. The checks below were run here instead.

## Summary

| ID | Severity | Category      | Action | Status | Title                                                     |
| -- | -------- | ------------- | ------ | ------ | --------------------------------------------------------- |
| F1 | Low      | Testing       | Fix    | Open   | No test covers the forwarder exiting while `ur` runs      |
| F2 | Low      | Documentation | Fix    | Open   | The source map in `AGENTS.md` omits `internal/ur/engine/mcp.go` |

## F1. No test covers the forwarder exiting while `ur` runs

Low · Testing · Fix · `internal/ur/engine/engine.go:219`

The plan's Decisions say that when the forwarder exits while `ur` runs, the
loop stops and `ur` exits 1 with `ox-mcp exited` on stderr. The
`forwarder fails` case in `TestOxTools` uses `Command: "false"`, which exits
before answering `initialize`, so it takes the startup path
(`start ox-mcp: initialize: ox-mcp exited`) and never reaches the
`<-forwarderExited` case in the loop.

### Evidence

With `serve` changed to leave `forwarderExited` nil (`e.ox = f`), so the loop
never sees the forwarder exit, `go test -race -count=1 ./internal/ur/...
./cmd/ox-mcp/` passes.

### Action

Keep the branch and add a case to `TestOxTools`: the stand-in forwarder exits
when an `echo` call's text is `exit`, after startup. The case calls `echo`
with `exit` and checks that the agent ends with an `exited` update whose error
contains `ox-mcp exited`. It fails with the mutation above.

## F2. The source map in `AGENTS.md` omits `internal/ur/engine/mcp.go`

Low · Documentation · Fix · `AGENTS.md:120`

The change creates `internal/ur/engine/mcp.go`, and the source map under
`internal/ur/engine` lists every other file but not it.

### Evidence

`AGENTS.md` lines 118–125 list `engine_test.go`, `engine.go`,
`instructions.go`, `shell_test.go`, `shell.go`, and `stream.go`.

### Action

Add `mcp.go` with a comment such as `# Ox's MCP forwarder and its tools`
between `instructions.go` and `shell_test.go`. `AGENTS.md` is workflow
work, which the Workflow section sends straight to `main` in a commit of its
own.

## Checks run

- `make test` (lint, `go test -race ./...`, Node suites): passed.
- `TestLiveUr` with `OX_LIVE=1` and `OPENROUTER_API_KEY` from the main
  checkout's `.env`: passed, including the new `list_sessions` turn in manual
  mode.
- Removals, one at a time, each reverted:
  - `_meta` dropped from `tools/call`: `TestUr` fails (the parent takes a
    report from the Ur child that told it).
  - The ID check dropped from the `answered` case: `TestOxTools` fails.
  - `e.ox.close()` dropped from `stop`: `TestOxTools/ended_with_ur` fails (the
    forwarder outlives `ur`).
  - `forwarderExited` left nil: every test passes (F1).
- `make e2e` and the headless-Chrome check were skipped: the page and its
  path to `oxd` are unchanged. Claude's `TestLive` was skipped: Claude's
  prompt is byte-identical and nothing else in how Ox drives Claude changed.

## Verification 1

Checked the uncommitted changes on base `339e5ad` after the fixes. Since the
review, only `internal/ur/ur_test.go` and `AGENTS.md` changed. `make test`
passed. `TestOxTools` passed five runs in a row with `-race -count=5`.

| ID | Status   | Evidence                                                                                                                                                 |
| -- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `TestOxTools/forwarder_exits` ends the stand-in after startup and checks for `ox-mcp exited`. With `forwarderExited` left nil, it fails ("no matching update") |
| F2 | Resolved | `AGENTS.md` lists `mcp.go # Ox's MCP forwarder and its tools` under `internal/ur/engine`                                                                  |

No new findings. The `AGENTS.md` line is workflow work, which goes to `main`
in a commit of its own.

## Verdict

`Ready to commit`: F1 and F2 are resolved.
