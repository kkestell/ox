# Review: tailnet listener rewrite, toggle placement, and web spec cleanup

## Scope and coverage

Reviewed three commits on `main`, with a clean working tree at `8690a3c`:

- `7446420` "Check tailscale_ip once at the boundary and simplify the tailnet
  listener", against `agents/plans/2026-11-20-001-tailnet-access.md`. It
  rewrites `d79ffe6`.
- `64d1e65` "Keep the panel toggles in place as they move between headers".
- `44cab72` "Test the web client as the user sees it, and prove tests by
  failing first", for whether each CSS assertion it removes from
  `session.spec.ts` and `workspace.spec.ts` checks only styling, under the
  spec rule it adds to `cmd/ox/web/AGENTS.md`. Its skill edits are out of
  scope.

Gaps:

- None of the three commits went through `/ox-work`, so no Tests proved or
  Checks report exists. I ran `make test` and `make e2e` at `8690a3c`, and
  proved the new tests of `7446420` and `64d1e65` by injected faults.
- The plan's headless Chrome check on the tailnet URL was not rerun. A probe
  of the `ox` already listening on this machine's tailnet address, whose build
  revision I did not confirm, accepts its own host and refuses a foreign one.

## Summary

| ID | Severity | Category | Action   | Status | Title                                                             |
| -- | -------- | -------- | -------- | ------ | ----------------------------------------------------------------- |
| F1 | Medium   | Testing  | Fix      | Resolved | No test sees the tailnet host reach the relay's accepted hosts    |
| F2 | Medium   | Testing  | Rejected | —      | No test checks that the file viewer colors text                   |
| F3 | Low      | Testing  | Rejected | —      | No test checks that a hidden panel's toggle is dimmed             |
| F4 | Low      | Intent   | Rejected | —      | `harness/list` and `list_models` return `tailscale_ip`            |
| F5 | Low      | Testing  | Rejected | —      | The other removed CSS assertions drop behavior checks             |
| F6 | Low      | Testing  | Fix      | Resolved | The F1 fix skipped the headless Chrome check through `make dev`   |

## F1. No test sees the tailnet host reach the relay's accepted hosts

Medium · Testing · Fix · `cmd/ox/main.go:65`

`main` builds the relay's accepted hosts inline, in the loop that binds each
address from `webAddrs`. The relay tests build a `relay` with a literal `hosts`
slice, and `TestWebAddrs` checks only the address strings. Nothing connects the
two, so if `main` stopped adding a host, the relay would refuse every `/events`
and `/rpc` request to the tailnet URL while the page itself still loaded. This
breaks **Web access**, and every test would still pass.

### Evidence

Replacing `r.hosts = append(r.hosts, host)` with `_ = host` in `main` leaves
`go test -count=1 ./cmd/ox` passing.

### Action

Move the hosts into a function that `main` calls, so the tests take the same
path. For example, `newRelay(socket string, addrs []string) (*relay, error)`
runs `serveHost` on each address and fills `hosts`. `main` then binds its
listeners from the same `addrs`. `TestRelayAcceptsItsPage` and
`TestRelayRefusesForeignPages` should build their relay with
`newRelay(…, webAddrs("localhost:8080", netip.MustParseAddr("100.64.1.2")))`
instead of a literal `hosts` slice. The existing "tailnet host" case then fails
when the tailnet host is dropped, and `TestRelayRefusesEveryInterface` can call
`newRelay` the same way. Prove the result with the injection above.

## F2. No test checks that the file viewer colors text

Medium · Testing · Rejected · `cmd/ox/web/e2e/workspace.spec.ts:745`

`44cab72` removed the keyword's light and dark color assertions. The spec still
checks that the text is split into tokens.

### Evidence

Setting `defaultColor: false` in `highlight()` gives tokens with no `color`.
With that change, "the file viewer shows each session's open file" still
passes.

### Action

Rejected: styling, which the user judges; ruled out of tests by the user, as
relayed by session `7708739b6e6e493084c38ffa00c42329`. The token checks of
**Syntax highlighting** move to `highlight.test.mjs` under
`agents/plans/2026-11-22-004-web-tests-best-practice.md`.

## F3. No test checks that a hidden panel's toggle is dimmed

Low · Testing · Rejected · `cmd/ox/web/e2e/workspace.spec.ts:423`

`64d1e65` and `44cab72` removed the toggles' color checks. The spec still
asserts `aria-pressed`, which the dimming rule at
`cmd/ox/web/src/style.css:365` is keyed on.

### Action

Rejected: styling, which the user judges; ruled out of tests by the user, as
relayed by session `7708739b6e6e493084c38ffa00c42329`.

## F4. `harness/list` and `list_models` return `tailscale_ip`

Low · Intent · Rejected · `internal/config/config.go:29`

The plan does not ask for this change. `Config` is now both the file's schema
and the `harness/list` response, so the configured address appears in both
replies when set, and `docs/protocol.md` says so.

### Evidence

`harness/list` replies with `s.config` (`internal/server/methods.go:47`), and
`list_models` returns the same JSON. The web client reads only `harnesses` and
`defaults` (`cmd/ox/web/src/ox.tsx:402`).

### Action

Rejected. No functional requirement covers the fields of `harness/list`, so
this changes the protocol, not what the user sees. Only the page and the user's
own Claude sessions read the reply, over the user's own socket, and they can
already read the address from the configuration file. One type for the file and
the response removes the shadow struct that kept two representations apart.
That is worth more than the few bytes `list_models` gains.

## F5. The other removed CSS assertions drop behavior checks

Low · Testing · Rejected · `cmd/ox/web/e2e/session.spec.ts:1088`, `cmd/ox/web/e2e/workspace.spec.ts:384`

Apart from F2 and F3, every CSS assertion `44cab72` removes checks styling:

- The list bullet styles. **Replies as Markdown** stays covered by the
  `ul li` and `ol li` text assertions.
- The tree guide's `::before` geometry. No requirement names the guide, and
  the order of children and siblings is still asserted.
- The resize cursor. The `resizing` class and the drag both come from `move()`
  in `cmd/ox/web/src/Panels.tsx:93`, and "a move over the sash leaves it in
  place" still checks that a drag ends with the button.
- The TSX tag color and Markdown italics. Their tokens are still asserted, and
  F2 covers highlighting colors.

### Action

Rejected: none of these removals drops a behavior check.

## F6. The F1 fix skipped the headless Chrome check through `make dev`

Low · Testing · Fix · `cmd/ox/main.go:110`

The F1 fix changes how the relay builds its accepted hosts, which is how the
page reaches `oxd`. `AGENTS.md` ("Verification") requires a check in headless
Chrome through the `make dev` stack for such a change. The implementer's report
says it was not run because the change is a refactor. Nothing outside the
implementer's control blocked it.

### Evidence

The `/ox-work fix` report lists `make test` and `make e2e` as run, and says the
headless Chrome check was skipped by choice.

### Action

Start the stack on spare ports with
`OX_VITE_PORT=9080 OX_RELAY_PORT=9081 XDG_CONFIG_HOME=$dir XDG_STATE_HOME=$dir XDG_RUNTIME_DIR=$dir make dev`
for an empty temporary `$dir`. In headless Chrome, open http://localhost:9080,
create a session with the fake harness, send a prompt, see its echo, and report
the result. No test is added.

## Checks run

- `make test`: passed at `8690a3c`.
- `make e2e`: 47 passed at `8690a3c`.
- F1 injection: dropping the tailnet host from `main` leaves
  `go test ./cmd/ox` passing.
- `64d1e65` proved: restoring the old `.files > header` padding and removing
  its border fails "panel toggles and drags change the layout" (toggle at
  x 1560, y 7.5 instead of x 1564, y 8).
- F2 injection: `defaultColor: false` leaves "the file viewer shows each
  session's open file" passing.
- Probe of the running `ox` on `100.110.111.81:17840`: `/events` answers 200 to
  its own host and 403 to `Host: evil.example`.
- All injections were reverted, and the tree is clean.
- `make live`: not required, since no commit changes how Ox drives Claude
  Code.

## Verification 1

Checked the uncommitted changes to `cmd/ox/main.go` and `cmd/ox/main_test.go`
on `c4d48e4`. `go test -count=1 ./cmd/ox` passes with the fix. Following
`/ox-review` step 9, I took `make test` and `make e2e` from the implementer's
report (both passed, e2e 47 passed and 3 skipped) and did not rerun them.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | `newRelay` builds the hosts for both `main` and the relay tests. Replacing `r.hosts = append(r.hosts, host)` with `_ = host` in `newRelay` fails `TestRelayAcceptsItsPage` ("tailnet host returned 403: unknown host"). |

New finding: F6.

## Verification 2

Checked the same uncommitted changes on `c4d48e4`. Their diff stat matches
verification 1, and the implementer reports no code change since then.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | Unchanged since verification 1. |
| F6 | Resolved | The implementer ran the spare-port `make dev` stack with an empty temporary `$dir`, holding only an `ox/config.json` that matches the e2e `fakeConfig` so `oxd` offers the fake harness. In headless Chrome at http://localhost:9080 they added a workspace, started a session, and sent "hello over the relay". The reply was "You said: hello over the relay", and the log had no race reports or relay errors. |

No new findings.

## Verdict

Ready to commit.
