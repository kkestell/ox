# Restart changed binaries in place under `make dev`

## Goal

Under `make dev`, applying a Go change to Ox means stopping the whole stack and running `make dev` again. That restarts `oxd`, which stops every Claude process, even when only the `ox` relay changed. It also restarts Vite, so the page has to be reloaded.

When the change is done, pressing Enter in the `make dev` terminal rebuilds the binaries and restarts only the processes whose binary changed:

- An `ox`-only change restarts the relay. Sessions keep running, and the page reconnects.
- An `oxd` change restarts `oxd` alone. Vite and `ox` stay up, so the page reconnects instead of losing its server.
- An unchanged binary is not restarted.
- A failed build leaves the running stack untouched.

`ox-mcp` is rebuilt but never restarted. Each Claude process launches its own copy and picks up the new binary when it next starts.

## Related code

- `cmd/ox/web/dev.mjs`: runs `bin/oxd`, `bin/ox -addr localhost:8081`, and Vite. It stops the stack when any of them exits. All three inherit the terminal's stdin, so Vite binds its own keyboard shortcuts.
- `Makefile`: `dev` builds the web client and the three binaries into `bin/`, then runs `dev.mjs` with `GORACE=halt_on_error=1`.
- `cmd/oxd/main.go` `run`: on SIGTERM, `oxd` keeps holding its socket until every session has stopped and every Claude process has exited, then exits. A new `oxd` can start only after the old one has exited.
- `internal/claude/claude.go:330`: kills a Claude process that outlives `killAfter`, which bounds `oxd`'s shutdown.
- `cmd/ox/web/src/connection.ts:36`: the page's event stream reconnects by itself, or asks for a reload once the browser closes it.

## Decisions

- **Trigger.** A restart happens only when the user presses Enter in the `make dev` terminal, never because a file in `bin/` changed. Agents build Ox too, and an `oxd` restart ends every running turn, including the turn of the agent that built it. A build into `bin/` by anyone else only changes files on disk, and the next Enter picks it up.
- **What changed.** `dev.mjs` hashes each binary (SHA-256 of the file) when it starts that binary. On Enter, it restarts a binary only if the file's hash differs. A rebuild of unchanged source leaves `bin/oxd` untouched (same inode), and a fresh build of the same source is byte-identical, so an unchanged binary is never restarted. Both were checked at HEAD with `go build -race`.
- **Build.** A new `make build` target runs `go build -race -o bin/ ./cmd/ox ./cmd/ox-mcp ./cmd/oxd`. `dev` depends on `web build`. On Enter, `dev.mjs` runs `make build` with inherited stdout and stderr. A nonzero exit prints that the build failed and restarts nothing. `build` does not depend on `web`: Vite serves the page from source under `make dev`, and `ox` only needs the `dist` that `dev`'s `web` step already produced.
- **Restart order.** The rebuilt binaries restart one at a time, `ox` first and then `oxd`. Each restart sends SIGTERM to the old process, waits for it to exit, hashes the binary, and spawns the new process with the same arguments and environment. Waiting for exit keeps the new `oxd` from finding its socket still held.
- **Unexpected exits.** An exit the stack did not cause still stops the whole stack with that exit's code, including a race report from a restarted binary. Each child's `exit` listener compares the process that exited with the service's current process. An exit counts as expected exactly when the service has already moved off that process. This one comparison is the only place that decides it, with no flag beside it.
- **Busy.** While a build or restart is running, Enter prints that one is in progress and does nothing else. If the stack stops during a restart, no new process is spawned.
- **Stdin.** Children are spawned with stdin set to `ignore`, so only `dev.mjs` reads the terminal. As a result, Vite's own keyboard shortcuts go away. A stdin that is not a TTY, or that ends, simply never triggers a restart. Stopping the stack closes the stdin reader so Node can exit.
- **Testability.** The stack logic moves to `cmd/ox/web/stack.mjs`, which exports `runStack({ services, build, input })`. `services` is a list of `{ name, command, args, cwd, restart }`, where `restart` is false for Vite. `build` is a command line. `input` is a readable stream. `runStack` returns a promise of the stack's exit code. `dev.mjs` calls it with the real commands and `process.stdin`, and handles SIGINT and SIGTERM through it.
- **Requirements:** *Lost connection* holds unchanged. An `ox` restart is brief, and the page reconnects, or asks for a reload if the stream closed meanwhile. *Surviving restarts* and *Resuming the conversation* cover an `oxd` restart: sessions come back parked and resume on their next send.
- **Settling:** Waiting for an old `ox` to exit ends when it exits on SIGTERM. Waiting for an old `oxd` to exit ends when its Claude processes exit, which `killAfter` bounds. A second Ctrl-C reaches `oxd` directly from the terminal and kills its Claude processes, as it does today. Waiting for the build ends when `make` exits.
- **Ownership:** Each service's current process and running hash belong to `runStack`'s service record. Only a restart replaces them. Their cleanup is the existing `stop`, which kills each service's current process.

## Check your assumptions

1. **Restart on Enter, not on rebuild.**
   - **Plan:** You press Enter in the `make dev` terminal to rebuild and restart what changed. An agent can build Ox at any time without disturbing running sessions.
   - **Alternative:** The stack restarts whatever changed as soon as a new binary lands in `bin/`. You save the keystroke, but any build into `bin/`, including an agent's mid-turn verification build, restarts `oxd` and ends every running turn, the building agent's own included.
2. **Vite's keyboard shortcuts go away.**
   - **Plan:** Only the Enter key does anything in the `make dev` terminal. Vite's `r`, `o`, `q`, and other shortcuts no longer work there.
   - **Alternative:** Keep Vite on the terminal and use a different key for restarts. Two readers would then share one terminal, and a keystroke meant for one could reach the other.

## Test plan

- New `cmd/ox/web/stack.test.mjs`, run with `node --test`. No existing test runs `dev.mjs`. Stand-in services are `#!/bin/sh` scripts in a temp directory. Each script writes `$$` to its own pid file and then `exec sleep 1000`. The stand-in build appends a comment line to the scripts named in an environment variable. `input` is a `PassThrough`.
  - Enter after a build that changes only `ox` restarts `ox`, giving a new pid. `oxd` keeps its pid, and the stack keeps running.
  - Enter after a build that changes nothing restarts nothing.
  - Enter while the build exits nonzero restarts nothing, and the stack keeps running.
  - Killing a service's process from outside resolves `runStack` with a nonzero code and stops the other services. This case guards the identity comparison that decides an expected exit.
  - A service with `restart: false` is not restarted even when its file changes.
- Add `node --test cmd/ox/web/stack.test.mjs` to the `test` target in `Makefile`.
- Manual check: run `make dev`, make a trivial change in `cmd/ox`, and press Enter. Only `ox` restarts, and an open page reconnects. Then do the same in `internal/server`: only `oxd` restarts, and Vite and `ox` stay up.

## Implementation plan

1. `Makefile`: add `build` (and add it to `.PHONY`) with the `go build` line. Change `dev` to `dev: web build` with only the `node` line in its recipe. Add the `node --test` line to `test`.
2. `cmd/ox/web/stack.mjs`: implement `runStack` as described under Decisions. Spawn services with `stdio: ["ignore", "inherit", "inherit"]` and hash with `node:crypto`. Read Enter presses with `node:readline` on `input`. Print one line per action: rebuilding, restarted `<name>`, nothing changed, build failed, busy.
3. `cmd/ox/web/dev.mjs`: keep the existing comment about one dev session. Build the three services: `bin/oxd`, `bin/ox -addr localhost:8081`, and Vite with `restart: false` and `cwd: import.meta.dirname`. Set the build to `make build`, pass `process.stdin`, and print `Press Enter to rebuild and restart what changed.` once. Map SIGINT and SIGTERM to the stack's stop with codes 130 and 143, and set `process.exitCode` from the result.
4. `cmd/ox/web/stack.test.mjs`: write the tests above.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`, Web UI section: one sentence saying that Enter in the `make dev` terminal rebuilds the binaries and restarts `ox` or `oxd` if its binary changed.
