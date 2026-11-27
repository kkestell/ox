# Keep session order across a restart, and let a second Ctrl-C reach a restarting oxd

## Goal

Two findings from `agents/audits/2026-11-25-ox-sweep.md`, part of #104:

- **F14.** A top-level session's creation time is set before its worktree is made, but the session joins its workspace's list when creation finishes. A local session created during that checkout lists before the worktree session while `oxd` runs and after it once `oxd` restarts. After the change, the creation time is set where the session joins the list, so the live list and the store hold one order.
- **F20.** When `make dev` restarts a changed `oxd`, the retiring process leaves the stack's records before it exits, so a second Ctrl-C does not reach it and the stack waits out its graceful stop. After the change, a second Ctrl-C reaches every process the stack has signaled that has not exited.

## Related code

- `internal/server/methods.go:400` — `createSession` builds the record with `CreatedAt: time.Now()` before `createTopLevel`.
- `internal/server/methods.go:419` — `createTopLevel` makes the worktree without `s.mu`, then calls `addSession` under `s.mu`.
- `internal/server/methods.go:515` — `addSession` stores the record and publishes it, under `s.mu`.
- `internal/server/mcp.go:637` — `startedRecord` sets `CreatedAt` for both the MCP `create_session` (child, `mcp.go:422`) and `create_top_level_session` (`mcp.go:590`) paths.
- `internal/server/mcp.go:449` — `addChild` stores the child before launch and publishes it after, on the parent's goroutine.
- `internal/store/store.go:250` — sessions load `ORDER BY s.created_at, s.rowid`.
- `cmd/ox/web/src/Sidebar.tsx:109` — `rowsOf` orders by `oxd`'s list order: top-level sessions reversed, each session's children in list order beneath it.
- `cmd/ox/web/stack.mjs` — `restart` clears `record.proc` before awaiting `retire(old)`; the second-SIGINT handler signals only `record.proc`; `stops` holds the exit promises of stopped processes.
- `cmd/oxd/main.go:96` — `oxd` takes its first SIGINT or SIGTERM as a graceful stop and a second as a forced exit with status 1.

## Decisions

- **Requirements:** **Session order.** Top-level sessions keep the same place in the list across a restart. Nothing the user sees changes otherwise.
- **F14, where the time is set.** `addSession` sets `record.CreatedAt = time.Now()` under `s.mu`, before its store write. Both top-level paths reach it, so `createSession` and `startedRecord` stop setting it. The store write, the append to `w.sessions`, and the time all happen in one `s.mu` section, so time order, `rowid` order, and list order agree; equal times tie on `rowid`.
- **F14, children.** `addChild` sets `record.CreatedAt = time.Now()` just before its store write, since `startedRecord` no longer does. Its order needs nothing more: only the parent's goroutine creates its children and `addChild` returns after publication, so siblings are stored and published in one order; the page orders children only within their parent, and top-level sessions only among themselves, so a child's place relative to sessions outside its family is never shown.
- **F20, who gets the second Ctrl-C.** Each entry in `stops` becomes `{ proc, stopped }`, so `stops` is the one record of every process the stack has signaled that has not exited cleanly. The second-SIGINT handler signals each entry's `proc` that has not exited, instead of each `record.proc`. After the first Ctrl-C, `stop` has retired every current process, so `stops` covers them and any process a restart is retiring. `stop` awaits `stops.map(({ stopped }) => stopped)`.
- **F20, the first Ctrl-C.** `stop` still signals only current processes. A retiring `oxd` already took its graceful signal; sending it another would force it, which is the second Ctrl-C's job.
- **F20, exit judgment.** A retiring process stays judged against the SIGTERM the restart sent. Forced by the second Ctrl-C, `oxd` exits 1, which fails the stack with code 1, as the second Ctrl-C does today for current processes.

## Test plan

- F14, server: extend `TestWorktreeAfterRestart` in `internal/server/worktree_test.go`. With `gitGate`, send a worktree `session/create`, wait for its held checkout, create a local session (`worktree: false`) with `c.call`, open the gate, and read the worktree creation's response. Record the session IDs in `allListed` order, restart over the same store, and assert the same order. Keep the existing checks on the worktree session. Fails before the change: the restart lists the worktree session first.
- F20, Node: add "a second Ctrl-C reaches an oxd a restart is retiring" to `cmd/ox/web/stack.test.mjs`. Start with `changed: ["oxd"]` and an `oxd` trap that logs `TERM`, touches a marker, and exits 0 after a two-second graceful stop, and on `INT` logs `INT` and exits 1. Write a line to rebuild, wait for the marker, emit SIGINT twice, and assert the stack resolves 1, the `oxd` log is `TERM\nINT\n`, and `restarted oxd` was not logged. Fails before the change: the log is `TERM\n` and the stack resolves 130. The existing "a second Ctrl-C forwards SIGINT" test keeps covering current processes.

## Implementation plan

1. `internal/server/methods.go`: drop `CreatedAt` from the record in `createSession`; set `record.CreatedAt = time.Now()` at the top of `addSession`, and say in its comment that the time is set where the session joins the list.
2. `internal/server/mcp.go`: drop `CreatedAt` from `startedRecord`'s record; set `record.CreatedAt = time.Now()` in `addChild` before `s.store.AddSession`.
3. `internal/server/worktree_test.go`: extend `TestWorktreeAfterRestart` as above.
4. `cmd/ox/web/stack.mjs`: make `stops` entries `{ proc, stopped }` in `retire` and its cleanup; have `stop` await their `stopped`; have the second-SIGINT handler signal each entry's unexited `proc`. Update the `runStack` and `stops` comments to say a second SIGINT reaches every stopped process that has not exited, including one a restart is retiring.
5. `cmd/ox/web/stack.test.mjs`: add the test above.

## Requirement changes

None.

## Check your assumptions

None.
