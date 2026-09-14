# Browser client foundation review

## Scope and mode

General review of the unstaged browser-client foundation: the new `client/`
package, its foundation plan, and the corresponding `eng/todo.md` update.

No Go production code changed. The review applied the selected topics to the new
TypeScript, React, Bun, documentation, and test boundaries.

## Selected topics

- **Correctness** — the host's same-origin WebSocket boundary and its
  snapshot/ping protocol are the change's primary behavior.
- **Security** — an untrusted browser crosses the WebSocket boundary.
- **Testing** — this slice introduces unit and real-process browser coverage.
- **Architecture** — the new package establishes the first durable browser
  client boundary described in `eng/client-architecture.md`.
- **Dependencies** — the new Bun package adds its own runtime and test
  dependencies.
- **Readability** — the host and shared protocol are small foundational modules
  that later work will extend.
- **Documentation** — the roadmap and plan describe the completed slice.

## Findings

### Medium — roadmap overstates what the browser smoke test proves

`eng/todo.md:111` says the Playwright smoke test drives a real Ox binary. The
browser test only renders the shell and observes the host's snapshot. The same
test file separately drives Ox through the official SDK, without any path from
the browser or Bun host to that SDK connection. This makes the roadmap claim a
browser-to-Ox integration that the current foundation intentionally does not
implement, and it leaves the accepted ping result unproved by an observable
browser assertion.

Suggested fix: describe the independent SDK smoke path accurately, and make the
browser's connected state depend on the correlated ping result so the Playwright
assertion proves the complete foundation protocol exchange.

## Topic verdicts

- **Correctness:** one protocol-observability gap above; no other confirmed
  defect in the traced snapshot, ping, asset, or rejected-origin paths.
- **Security:** the loopback default and exact `Origin` comparison keep a
  cross-origin page from upgrading into host state; no confirmed finding.
- **Testing:** the gap above is the only unmapped protocol behavior found.
- **Architecture:** host, browser, and browser-safe protocol remain separated as
  the client architecture requires; no confirmed finding.
- **Dependencies:** package-local, exact-version Bun dependencies have focused
  uses; no confirmed finding.
- **Readability:** the small modules use direct, locally legible control flow;
  no confirmed finding.
- **Documentation:** the roadmap wording above needs correction; the plan
  accurately calls the SDK Ox path independent.

## Checks run

- `git diff --check` — passed.
- `bun run check` — passed.
- `bun run test` — 10 tests passed.
- `bun run test:e2e` — the real Chromium, Bun host, fake provider, and Ox
  process smoke test passed.

## Follow-up

The finding is addressed in this implementation commit. Process supervision and
browser-to-Ox session behavior remain deliberately outside this foundation slice
and belong to the next browser-client task in `eng/todo.md`.
