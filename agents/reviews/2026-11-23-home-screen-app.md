# Review: Open Ox from the iPhone Home Screen as a standalone app

## Scope and coverage

Uncommitted changes on `home-screen-app` over base `a35d87d` (HEAD is the
base), reviewed against `agents/plans/2026-11-23-home-screen-app.md`. The
tree has edits to `session.spec.ts`, `index.html`, `App.tsx`, `connection.ts`,
`ox.tsx`, and `docs/functional-requirements.md`. The new files are
`cmd/ox/web/icon.svg`, `cmd/ox/web/public/manifest.json`, and
`cmd/ox/web/public/icon.png`. The review traced every reader and writer of the
page error and every `Disconnected` constructor. It also checked the
requirements diff word for word against the plan, and the icon's size and
opacity (`512x512 True`). It confirmed the dark `theme-color`: `--pane` mixes
L 0.145 and 0.205 to L 0.175, which is `#101010`.

Gaps:

- Standalone behavior on the iPhone (icon, name, no toolbars, status bar,
  insets) needs the physical device, as the plan's Test plan says. It was not
  checked.

## Summary

| ID | Severity | Category     | Action   | Status   | Title                                                      |
| -- | -------- | ------------ | -------- | -------- | ---------------------------------------------------------- |
| F1 | Low      | Testing      | Fix      | Resolved | No test keeps the Reload button off the reconnecting alert |
| F2 | Low      | Intent       | Rejected | —        | `<title>Ox</title>` added to `icon.svg`                    |
| F3 | Low      | Construction | Rejected | —        | `Disconnected`'s `closed` defaults to `false`              |
| F4 | Low      | Testing      | Rejected | —        | Headless Chrome check through `make dev` not run           |

## F1. No test keeps the Reload button off the reconnecting alert

Low · Testing · Fix · `cmd/ox/web/src/App.tsx:156`

The Reload button shows only when the page error is a `Disconnected` with
`closed`. Under *Lost connection*, a page that is still reconnecting by itself
shows no Reload button. No test protects the `closed` half of that guard. The
new test covers the closed case. The existing reconnecting tests match the page
alert with `filter({ hasText: "Lost the connection" })`, which still passes when
" Reload" is appended. The two exact-text assertions at `session.spec.ts:387`
and `:1048` read a session's alert in `main`, not the page alert.

### Evidence

Changing the condition to `error() instanceof Disconnected`, which shows Reload
on every lost connection, left the full e2e suite passing: 134 passed.

### Action

In the existing e2e test "a lost connection keeps the last listing"
(`workspace.spec.ts:396`), the replacement stream is aborted, so the page stays
in the reconnecting state. While the alert is up there, assert that it carries
no Reload button: `expect(alert.getByRole("button", { name: "Reload" })).toHaveCount(0)`.
An exact `toHaveText("Lost the connection to ox. Reconnecting.")` also works.
Confirm the assertion fails with the mutation above.

## F2. `<title>Ox</title>` added to `icon.svg`

Low · Intent · Rejected · `cmd/ox/web/icon.svg:2`

The plan gives the SVG without a `<title>`. Biome's recommended a11y rules
require one, and `npm run fix` would otherwise fail lint on the file.

### Action

Rejected. The SVG is source only and stays out of `dist`. The title does not
change what `rsvg-convert` renders. The committed PNG is 512×512 and fully
opaque, as the plan requires. Keeping the file lint-clean costs less than
excluding it from Biome.

## F3. `Disconnected`'s `closed` defaults to `false`

Low · Construction · Rejected · `cmd/ox/web/src/connection.ts:17`

The plan calls for a `readonly closed: boolean` constructor parameter. The
implementation defaults it to `false`.

### Action

Rejected. Only the stream's `onerror` can see `EventSource.CLOSED`, and it
passes `closed` explicitly. The same expression also picks the message, so
message and flag come from one source. The other five constructors all mean a
connection that can still recover: the 503 relay answer, a cut-off POST, "Not
connected", and `close()`. `false` is correct for each. Requiring the argument
would add five literal `false`s and rule out no wrong state.

## F4. Headless Chrome check through `make dev` not run

Low · Testing · Rejected

### Action

Rejected. `AGENTS.md` ("Verification") requires that check when a change alters
how the page reaches `oxd`. This change does not touch `/rpc`, `/events`, or the
relay. It adds two static files that the existing `GET /` handler serves, and a
button that reloads the page. The new e2e test fetches the manifest and icon
from a built `ox` and checks their content types. The Reload test drives the
closed-stream path through the real relay.

## Checks run

- `make test`: passed (Go packages, Node tests, Biome lint with two
  existing CSS warnings in an unchanged file).
- `make e2e`: 134 passed, 4 skipped.
- Mutation: Reload shown on every `Disconnected`: e2e still passes (F1).
- The new tests fail without the change, confirmed from the source. The base
  `index.html` has no `link[rel=manifest]`, and the base `App.tsx` renders no
  Reload button.
- Requirements diff: matches the plan's Requirement changes word for word.
- `make live`: not required. The change does not alter how Ox drives Claude
  Code.
- Standalone check on the iPhone: blocked on the physical device, as planned.

## Verification 1

Checked the uncommitted tree on base `a35d87d` after the fix for F1. The only
change since the review is in `cmd/ox/web/e2e/workspace.spec.ts`.

| ID | Status   | Evidence                                                                                                                                                       |
| -- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | "a lost connection keeps the last listing" asserts the reconnecting alert has no Reload button; it fails with the guard reduced to `error() instanceof Disconnected` (expected 0, received 1) and passes with it restored |

No new findings.

Checks run:

- `make test`: passed.
- `make e2e`: 134 passed, 4 skipped.
- The F1 mutation, rerun against the new assertion: the test fails, and passes
  once the mutation is reverted.

## Verdict

Ready to commit.
