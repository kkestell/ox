# Serve make dev from one port

## Goal

`make dev` runs three services: `oxd`, `ox` on 8081, and Vite's dev server on 8080, which serves the web client from source and proxies `/events` and `/rpc` to `ox`. `ox` also serves the `web/dist` bundle it embeds at build time, and Enter in `make dev` runs `make build`, which rebuilds the Go binaries but not `web/dist`. The two ports serve different clients: 8081, and the tailnet listener on 17840, served a bundle from before PR #19, so the fixed wide-table scroll still showed there while 8080 had the fix.

When the change is done, `make dev` runs `oxd` and `ox` only. `ox` serves the web client built from the current source on one port: 8080 in the main checkout, a free port in a linked worktree. Enter rebuilds the web bundle and the binaries and restarts whichever binary changed. There is no live reload; the page is reloaded by hand.

## Related code

- `Makefile` — `build` runs `go build` alone; `dev` depends on `web build` and runs `dev.mjs`.
- `cmd/ox/web/dev.mjs` — picks the ports, runs the stack with `vite` as a third service, and passes `make build` as the rebuild command.
- `cmd/ox/web/stack.mjs` — `runStack`; the `restart` and `cwd` fields of a service exist only for Vite.
- `cmd/ox/web/stack.test.mjs` — runs a three-service stack named `oxd`, `ox`, `vite`, and tests `restart: false`.
- `cmd/ox/web/vite.config.ts` — the `server` block sets Vite's port and proxy from `OX_VITE_PORT` and `OX_RELAY_PORT`.
- `cmd/ox/web/package.json` — the `dev` script runs Vite's dev server.
- `cmd/ox/main.go:24` — `//go:embed web/dist`; `-addr` defaults to `localhost:8080`.
- `cmd/ox/main_test.go:44` — `TestRelayAcceptsItsPage` has a "through Vite" case for the proxy's Host and Origin.
- `cmd/ox/web/e2e/global-setup.ts`, `e2e/services.ts` — build the bundle and binaries themselves and run `ox` directly; they do not use Vite's dev server and need no change.

## Decisions

- **`ox` keeps the embed.** `make build` depends on `web`, so every `bin/ox` embeds the bundle built from the current source. The stale bundle came from `make build` producing an `ox` that embeds whatever `web/dist` held; the dependency removes that at its cause, for `make dev`, Enter, and a bare `make build` alike. Serving `web/dist` from disk under dev would add a dev-only flag to `ox` and let the served files change under a running `ox` while Vite empties and rewrites `dist`.
- **Restart follows the binary hash.** `go build` reads the embedded files, so a changed bundle changes `bin/ox`'s hash and the stack's existing hash check restarts `ox`; an unchanged source leaves `ox` running. A failed `tsc` or `vite build` fails `make build`, and the stack logs "build failed; nothing restarted" as it does for a Go failure.
- **Ports.** The main checkout runs `ox` with no `-addr`, so it serves on `ox`'s default `localhost:8080`, the URL the user already uses. A linked worktree passes `-addr localhost:<free port>` from the existing `freePort`, now one port instead of two. `OX_VITE_PORT` and `OX_RELAY_PORT` go. `dev.mjs` prints the page's URL in both cases, as now.
- **Every service restarts on a changed binary.** With Vite gone, the `restart` field and the `cwd` field of a service have no use. `runStack` hashes every service's command at start and checks every one on rebuild.
- **Vite is a bundler only.** `vite.config.ts` keeps `plugins: [solid()]` and drops the `server` block and its comment. `package.json` drops the `dev` script. `vite` stays a dev dependency for `vite build`, and `tsconfig.json` keeps `vite/client`.
- **Requirements:** none touched. `make dev` is development tooling; the tailnet listener and Lost connection behave as before, and an `ox` restart on Enter drops the page's connection as it does now.
- **Trust boundaries:** `ox` listens on the same loopback address and tailnet address as before; the relay's Host and Origin checks are unchanged. The only listener that goes is Vite's on 8080, which `ox` now takes.

## Check your assumptions

None.

## Test plan

- `cmd/ox/web/stack.test.mjs`: the stack runs two services, `oxd` and `ox`. Remove "a service with restart false is not restarted". "a changed binary restarts alone" drops its `vite` assertion. "an exit the stack did not cause stops the stack" sets its `USR1` trap on `ox` and asserts `oxd` stopped. `signalStack` and "a second Ctrl-C forwards SIGINT" use `["oxd", "ox"]`. `start` loses its `restart` option.
- `cmd/ox/main_test.go`: `TestRelayAcceptsItsPage` drops the "through Vite" case, and its comment says the page reaches the relay directly or through the tailnet address.
- Headless Chrome through `make dev` in this worktree: the page at the printed URL loads and lists sessions. Change a visible string in `cmd/ox/web/src`, press Enter, and check that the stack logs `restarted ox` and a reload shows the new string. Press Enter again with no change and check it logs `nothing changed`. The Makefile dependency has no cheaper layer that observes it without testing the Makefile's text.

## Implementation plan

1. `Makefile`: `build: web`; `dev: build`.
2. `cmd/ox/web/stack.mjs`: remove the `restart` and `cwd` fields from services; hash every command at start and check every one on rebuild. Update the doc comment.
3. `cmd/ox/web/dev.mjs`: run `oxd` and `ox` only. In a linked worktree, take one free port and pass `-addr localhost:<port>` to `ox`; in the main checkout pass no `-addr` and print `http://localhost:8080/`. Drop the env variables and update the comments.
4. `cmd/ox/web/stack.test.mjs`: the changes in the test plan.
5. `cmd/ox/web/vite.config.ts`: drop the `server` block, the relay constant, and the comment.
6. `cmd/ox/web/package.json`: drop the `dev` script.
7. `cmd/ox/main_test.go`: drop the "through Vite" case and correct the comment.
8. `docs/architecture.md`, `.claude/skills/ox-plan/SKILL.md`: the updates below.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md` ("Web UI"): `make build` builds the web client into `cmd/ox/web/dist` before the binaries, and `ox` embeds it. `make dev` runs `oxd` and `ox`, which serves the UI at http://localhost:8080. Enter rebuilds and restarts `ox` or `oxd` if its binary changed. A linked worktree serves on a free port. Remove the last sentence of the tailnet paragraph, about the embedded build and live reload.
- `.claude/skills/ox-plan/SKILL.md` step 9: the page's path to `oxd` runs through the relay alone, not Vite's proxy.
