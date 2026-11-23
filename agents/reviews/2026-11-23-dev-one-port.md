# Review: Serve make dev from one port

## Scope and coverage

Reviewed the uncommitted changes in the `dev-one-port` worktree against base `a35d87d` (`git merge-base origin/main HEAD`, also `HEAD`), with the working tree holding the nine modified files the plan names and the untracked plan. Checked against `agents/plans/2026-11-23-dev-one-port.md`, `AGENTS.md`, `cmd/ox/web/AGENTS.md`, the "Web UI" section of `docs/architecture.md`, and the open `bug` issues (#5, #6, #33, #34), none of which this change touches.

Gaps:

- The request passed on no `/ox-work` final response, so there were no Tests proved or Checks to take. The reviewer ran `make test`, `make e2e`, the headless Chrome check through `make dev`, and the test mutations below.
- The main checkout's path (no `-addr`, port 8080) was traced, not run, because the main checkout's own stack holds its ports.

## Summary

| ID | Severity | Category     | Action | Status | Title                                                  |
| -- | -------- | ------------ | ------ | ------ | ------------------------------------------------------ |
| F1 | Low      | Construction | Fix    | Open   | `dev.mjs` restates `ox`'s default port as the page URL |

## F1. `dev.mjs` restates `ox`'s default port as the page URL

Low · Construction · Fix · `cmd/ox/web/dev.mjs:27`

In the main checkout, `dev.mjs` passes `ox` no `-addr` and prints `http://localhost:8080/` from its own `port = "8080"`. The port `ox` serves on comes from the `-addr` default in `cmd/ox/main.go:32`. The printed URL and the served port are one fact kept in two files; a change to either default leaves `make dev` printing a URL nothing serves. The linked-worktree branch already derives both from one value.

### Evidence

`cmd/ox/web/dev.mjs:27` sets `let port = "8080"`; only the linked-worktree branch pushes `-addr localhost:${port}` (`dev.mjs:35`). `cmd/ox/main.go:32` defaults `-addr` to `localhost:8080` independently, and `ox` prints only the tailnet URLs (`main.go:68-70`), so nothing ties the printed URL to the listener in the main checkout.

### Action

Pass `-addr localhost:${port}` to `ox` in both cases: keep `port = "8080"` as the main checkout's value, overwrite it with `freePort()` in a linked worktree, and push `-addr` once after the `if`. The main checkout still serves on 8080, as the plan's Ports decision requires, and the printed URL and the listener read one value. No test: the stack's port choice is observed only by running `make dev`, which the headless check already does.

## Checks run

- `make build` twice with no source change: `bin/ox` and `bin/oxd` hashes identical across builds, so an unchanged source restarts nothing.
- `make test`: passed.
- `make e2e`: 132 passed, 4 skipped.
- Headless Chrome through `make dev` in this worktree: the stack printed `http://localhost:52172/`; the page loaded from that port and rendered the session list (empty `.state`, no workspaces) with no page errors. Enter with no change logged `nothing changed`. A temporary edit of the "Add workspace" title in `src/Sidebar.tsx`, then Enter, logged `restarted ox`, and a reload showed the new title. Enter again logged `nothing changed`. The edit was reverted, the stack stopped on SIGINT, and `make build` restored `dist` and `bin`.
- Mutation: restoring the `!record.spec.restart ||` guard in `rebuild` fails "a changed binary restarts alone" (timed out waiting for `restarted ox`).
- Mutation: hashing only services with `restart` set in `start` fails "a changed binary restarts alone" and "an unchanged binary is not restarted".
- `make live`: not required; the change does not alter how Ox drives Claude Code.

## Verification 1

Checked the uncommitted tree on base `a35d87d` after the fix for F1, which changed only `cmd/ox/web/dev.mjs`.

| ID | Status   | Evidence                                                                                                                                                             |
| -- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `dev.mjs` sets `port`, replaces it with `freePort()` in a linked worktree, and passes `-addr localhost:${port}` once for both cases; the printed URL reads the same value |

No new findings.

Checks run:

- `make test`: passed. Its Biome step reports two `noDescendingSpecificity` warnings, both in `cmd/ox/web/src/style.css` (lines 172 and 330). The change leaves `src/`, `biome.json`, and `package-lock.json` as they are at base, so the same warnings appear at base. They come from earlier commits and are outside this change.
- `make dev` in this worktree: printed `http://localhost:53889/`, ran `bin/ox ... -addr localhost:53889`, and the page answered 200 there. The stack stopped on SIGINT.
- `make e2e`: not re-run. Its last pass was on this tree before the fix, and `dev.mjs` is outside what it runs.

## Verdict

Ready to commit.
