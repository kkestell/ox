# Review: serve the web UI on the user's tailnet

## Scope and coverage

Reviewed the uncommitted changes on base `f1350cd` against
`agents/plans/2026-11-20-001-tailnet-access.md` as revised at the user's direction: the added
`tailscale_ip` setting with no change to the built-in catalog's fallback. Files:
`internal/config/config.go`, `internal/config/config_test.go`, `cmd/ox/main.go`,
`cmd/ox/main_test.go`, `cmd/ox/web/e2e/services.ts`, `docs/architecture.md`,
`docs/functional-requirements.md`, `docs/protocol.md`, and the code that reads
the shared `config.Config`: `harness/list` (`internal/server/methods.go`) and
`list_models` (`internal/server/mcp.go`). The user's own changes to
`cmd/ox/web/e2e/workspace.spec.ts`, `cmd/ox/web/src/style.css`, and
`docs/todo.md` are excluded.

The requirements diff replaces **Local access only.** with **Web access.** in
the plan's words. `make dev` builds `web/dist` before `bin/ox`, and `dev.mjs`
runs `ox` without `-config`, so the tailnet listener serves the embedded build
from the isolated XDG configuration, as the plan's Decisions say.

Gaps:

- `make e2e` and the headless Chrome check on the tailnet address were taken
  from the implementer's report, not rerun.

## Summary

| ID | Severity | Category | Action | Status   | Title                                                          |
| -- | -------- | -------- | -------- | -------- | -------------------------------------------------------------- |
| F1 | Low      | Intent   | Fix    | Resolved | `harness/list` and `list_models` now serve `tailscale_ip`      |
| F2 | Low      | Testing  | Fix    | Resolved | `listen` tests leave the tailnet host and URL unprotected      |

## F1. `harness/list` and `list_models` now serve `tailscale_ip`

Low · Intent · Fix · `internal/server/methods.go:47`, `internal/server/mcp.go:217`

`harness/list` replies with the whole `s.config`, and the `list_models` MCP tool
marshals it too. Adding `TailscaleIP` to the JSON representation of
`config.Config` puts `"tailscale_ip"` in both replies: an empty string normally,
and the machine's tailnet address when set, which every Claude session can read
through `list_models`. The protocol documents `harness/list` as returning the
harnesses, the new-session defaults, and `max_child_depth`, and the plan scopes
the change to a setting `ox` reads for its listener. The catalog reply widening
is outside that scope and contradicts `docs/protocol.md`.

### Evidence

Traced: `methods.go:47` replies `jsonrpc.Success(id, s.config)`, and
`mcp.go:217` calls `json.Marshal(s.config)`. `Config.TailscaleIP` has the tag
`json:"tailscale_ip"`, so `encoding/json` always emits it.

### Action

Keep `tailscale_ip` on the file input and out of the JSON emitted by
`config.Config`. That keeps the same catalog fields in both replies. Extend
the configuration test to check that an address loaded from the file is
available to Ox and absent from the emitted JSON.

## F2. `listen` tests leave the tailnet host and URL unprotected

Low · Testing · Fix · `cmd/ox/main_test.go:85`

`TestListenBindsTailnetListener` uses `127.0.0.1` as both the local and tailnet
host, so `hosts` contains `127.0.0.1` whether or not `listen` adds the tailnet
address. It checks only that the URL is nonempty, so a wrong URL passes.
`TestTailnetPortIs17840` restates the constant and cannot show that the tailnet
listener binds 17840 regardless of `-addr`'s port, which the plan's Test plan
asks for. The relay tests build `relay.hosts` by hand, so nothing ties the
allowed hosts to what `listen` bound.

### Evidence

With `listen` returning `hosts` instead of `append(hosts, tailscaleIP)`,
`go test -count=1 ./cmd/ox/` passes. With the URL built from `addr` instead of
`tailnetAddr`, it passes too. Both edits were reverted.

### Action

Drop the `tailnetPort` parameter so `listen` uses the constant, and remove
`TestTailnetPortIs17840`. In `TestListenBindsTailnetListener`, call
`listen("localhost:0", "127.0.0.1")` and check that the second listener's
address is `127.0.0.1:17840`, the URL is `http://127.0.0.1:17840`, and `hosts`
is `["localhost", "127.0.0.1"]`. Make `TestListenFailsOnBoundTailnetPort` block
`127.0.0.1:17840`. Then each of the two edits above fails a test.

## Checks run

- `make test`: passed.
- Mutation, `listen` drops the tailnet host from `hosts`: `cmd/ox` tests pass
  (F2).
- Mutation, `listen` builds the tailnet URL from `addr`: `cmd/ox` tests pass
  (F2).
- `make e2e` (43 passed, 3 skipped) and headless Chrome over the tailnet
  address and local Vite on the `make dev` stack: from the implementer's report.
- `make live`: not required. The change does not alter how Ox drives Claude
  Code.

## Verification

Verified the final uncommitted tailnet change on base `f1350cd`. Both
findings are resolved. The fixes introduced no new findings.

- **F1.** The file decoder reads `tailscale_ip` into `Config.TailscaleIP`.
  Both model-list replies serialize `Config`, whose JSON tag excludes that
  field. `TestTailscaleIP` passed. Restoring the outgoing field in a Go
  overlay made the test fail.
- **F2.** The listener test checks the address and URL against literal port
  17840, with an ephemeral local port and distinct allowed host strings.
  The bind-failure test occupies that same fixed address. The tests passed.
  Go overlays that dropped the allowed host or built the tailnet URL from
  the local address failed the listener test. Changing the production port
  to 17841 or 0 failed its address and URL checks.

`make test` passed after the final test correction. The parent reported
43 passed and 3 skipped in `make e2e` on the final production code and
confirmed in Chrome on the isolated `make dev` stack that a prompt
submitted through the Tailscale address echoed successfully and the local
Vite URL showed the same session.

## Verdict

Ready to commit. Both findings are resolved.
