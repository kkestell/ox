# Review of 2125f54 and a42b498

## Scope and coverage

Two commits that reached `main` without a plan or review, read as the code stands at HEAD `57bf1dd`, against `docs/functional-requirements.md` and `AGENTS.md`:

- `2125f54` "Keep workspace headers' height and fix ox-plan and doc drift": `nav > p { flex: none }` in `style.css`, `OX_RELAY_PORT` and `OX_VITE_PORT` in `dev.mjs` and `vite.config.ts`, the spare-port `make dev` invocation in `ox-plan`, `create_session`'s description in `internal/server/mcp.go`, the Source Map in `AGENTS.md`, and the `protocol.md` and Claude Code note corrections for Deslop F21 and F22.
- `a42b498` "Draw tree guide lines beside child sessions": the child-row guide lines in `style.css`. `539d62a` has since rewritten them for nested children, using `--depth`, `--rise`, `.first`, and `.last` from `rowsOf` in `Sidebar.tsx`; that version was reviewed.

The working tree held another session's uncommitted changes to `cmd/ox/main.go` and `cmd/ox/main_test.go`, outside this scope.

Gaps:

- The "workspace headers keep their height" e2e test, which checks a pixel height, and the guide-line test that `44cab72` deleted belong to the web test rewrite plan and get no finding here.

## Summary

| ID | Severity | Category      | Action   | Status   | Title                                                                  |
| -- | -------- | ------------- | -------- | -------- | ---------------------------------------------------------------------- |
| F1 | Medium   | Documentation | Fix      | Resolved | The spare-port `make dev` check fails when `tailscale_ip` is set       |
| F2 | Low      | Documentation | Fix      | Resolved | The Source Map omits two test files                                    |
| F3 | Low      | Documentation | Rejected | —        | `session/create` marks `effort` required while allowing it to be empty |

## F1. The spare-port `make dev` check fails when `tailscale_ip` is set

Medium · Documentation · Fix · Resolved · `.claude/skills/ox-plan/SKILL.md:68`

`ox-plan` tells plans to check the page in headless Chrome through a second stack started with `OX_VITE_PORT=9080 OX_RELAY_PORT=9081 XDG_STATE_HOME=$dir XDG_RUNTIME_DIR=$dir make dev`. That stack reads the user's configuration. Since `d79ffe6`, a configuration with `tailscale_ip` makes `ox` also bind that address at the fixed port 17840, whatever `-addr` says (`cmd/ox/main.go:29`, `webAddrs`). With the user's own stack running, the second `ox` cannot bind 17840 and exits, and `runStack` stops the whole stack. The user's configuration sets `tailscale_ip`, so every plan that needs this check gets a command that fails.

### Evidence

With the user's stack holding `100.110.111.81:17840`, `XDG_STATE_HOME=$d XDG_RUNTIME_DIR=$d bin/ox -addr localhost:9081` exited 1 with `listen tcp 100.110.111.81:17840: bind: address already in use`.

The same invocation with `XDG_CONFIG_HOME=$dir` added started all three services. Headless Chrome loaded http://localhost:9080, opened `/events` with status 200, and its `/rpc` posts through Vite's proxy and the relay on 9081 returned 204.

### Action

Add `XDG_CONFIG_HOME=$dir` to the invocation in `ox-plan/SKILL.md:68`, so the second stack runs on the built-in configuration and binds only its `-addr`. No test: the fix is the skill's text, checked by running the command.

## F2. The Source Map omits two test files

Low · Documentation · Fix · Resolved · `AGENTS.md:77`

`2125f54` says the Source Map now matches the code, but it leaves out `internal/server/mcp_test.go` and `internal/store/store_test.go`. Both existed before the commit.

### Evidence

Every tracked `.go`, `docs/*.md`, `internal/config/*.json`, and `scripts/*` file was checked against the map's entries. Only these two are missing.

### Action

Add `mcp_test.go` under `internal/server` and `store_test.go` under `internal/store`, in the map's order and comment style.

## F3. `session/create` marks `effort` required while allowing it to be empty

Low · Documentation · Rejected · `docs/protocol.md:226`

The table marks `effort` as required, and its description lets a model without effort levels take an empty or omitted value.

### Action

Rejected: the description states the exception, and `CheckModel` (`internal/config/config.go:149`) matches it. A Required column cannot hold a condition, and "Yes" is right for every model that has effort levels.

## Checks run

- `make test`: passed.
- `make e2e`: 47 passed, 3 skipped, including "workspace headers keep their height when archived sessions overflow the sidebar".
- `nav > p { flex: none }` checked with a static page using `style.css`, a 200 px tall viewport, and an overflowing sidebar in headless Chrome: headers hold 28 px, and drop to 16.5 px without `flex: none`. The Add workspace button, the other fixed-height child of `nav`, holds 28 px either way.
- The spare-port `make dev` stack in headless Chrome, as F1 describes.
- `protocol.md`'s `workspace/list_folder`, `session/send`, `session/interrupt`, and `parked` text, the Claude Code note on background tasks, and `create_session`'s description were traced against `methods.go`, `prompt.go`, `configure.go`, and `mcp.go` at HEAD, and match.
- The guide-line geometry at HEAD was traced against `rowsOf`: each guide starts at the bottom of its parent's dot for a first child, or at the previous sibling's branch, and the left offset lands on the parent's dot at every depth and at both row heights.

## Verification 1

HEAD `d8232e8`, with the uncommitted fixes to `.claude/skills/ox-plan/SKILL.md` and `AGENTS.md`. The uncommitted `cmd/ox/main.go` and `cmd/ox/main_test.go` changes belong to another review and were not checked.

| ID | Status   | Evidence                                                                                                                                                                                                                        |
| -- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `ox-plan/SKILL.md:68` adds `XDG_CONFIG_HOME=$dir`, which `paths.Config` reads. The same invocation, run in the first review, started the stack beside the user's and reached `oxd` from headless Chrome. No test: skill text. |
| F2 | Resolved | The Source Map lists `mcp_test.go` and `store_test.go`; every tracked `.go`, `docs/*.md`, `internal/config/*.json`, and `scripts/*` file now has an entry.                                                                     |

No new findings. Both fixes are text, so no suite covers them, and none was rerun.

## Verdict

Ready to commit: F1 and F2 are resolved, and F3 is rejected.
