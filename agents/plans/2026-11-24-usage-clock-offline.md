# Keep the page's clock running while disconnected

## Goal

The subscription usage under the composer stops counting down while the page is
disconnected: it keeps its last time left and never dims, even after its reset
time passes. The page's clock, which both paces repeated requests and tells
usage rows the time, advances only while the page is visible and connected.

After the change, the clock advances once a second whenever the page is visible,
and carries the current time. Repeated requests check the connection themselves
before each one. A reading on screen during a lost connection counts down, and
dims with its waiting message once its reset time passes.

Resolves [#86](https://github.com/kkestell/ox/issues/86), audit finding
[F3](../audits/2026-11-24-structural-audit-2026-11-24.md#f3-the-usage-countdown-freezes-when-the-page-disconnects).

## Related code

- `cmd/ox/web/src/ox.tsx:732` — `clock`, a counter that ticks once a second
  only while `document.visibilityState === "visible"` and `ready()`. Exported
  from `createOx` beside `ready`.
- `cmd/ox/web/src/ox.tsx:488` — `ready`, true while the page holds a live
  connection (`live()`), set after `workspace/list` answers on reconnect and
  cleared in `onError`.
- `cmd/ox/web/src/poll.ts` — `createPoll`, the only request path paced by the
  clock. Callers: `FileTree.tsx:54` (`workspace/list_folder`) and
  `FileView.tsx:26` (`workspace/read_file`).
- `cmd/ox/web/src/Usage.tsx:59` — `WindowRow` reads `Date.now()` in a memo that
  tracks `ox.clock()` to derive time left, the elapsed marker, and the reset
  state.
- `cmd/ox/web/e2e/session.spec.ts:388` — usage specs, using the fake harness's
  `usage` input, whose readings are relative to the daemon's real time.
- `cmd/ox/web/e2e/workspace.spec.ts:366`, `:489` — specs that the file tree
  sends nothing while hidden and nothing while disconnected until the page has
  reloaded.

## Decisions

- **Construction:** `clock` becomes the page's current time in milliseconds,
  a signal set to `Date.now()` once a second while the page is visible. It is
  the one source of visible time; `WindowRow` derives time left from it and the
  stored `resets_at`, with no memo around an impure `Date.now()` read. Whether a
  request may be issued is decided in one place, `createPoll`, which reads
  `ready` untracked on each tick and skips the tick when the page is not
  connected. The clock no longer knows about the connection, so no display
  derived from it can freeze on a lost connection.
- **Ownership:** no new state. The existing interval in `createOx` keeps its
  owner and cleanup; only what it writes and its gate change. Hidden pages still
  do not tick, so a page shown again updates within a second, as it does now.
- **Requirements:**
  - **Subscription usage** — "dimmed once its limit has reset" now holds while
    reconnecting and while showing Reload, since the reading kept on screen
    keeps counting against the page's time. Through a reconnect, the reading
    is replaced by the reloaded snapshot as before.
  - **Lost connection** — unchanged. Polls still send nothing while the page is
    disconnected or catching up, because `ready` is false until `reload` sets
    `live`; the gate moves from the clock to `createPoll` without changing when
    requests go out.
  - **Work outlives the page** and the file tree's behavior are untouched.
- **Connection loss handling:** this change reads `ready` and leaves
  `connection.ts`, `onError`, and `reload` alone. The sibling change for #84
  (`relay-503-reconnect`) changes how the stream ends and may change the
  disconnect path in `ox.tsx`; `createPoll` follows whatever `ready` means after
  it, so the two changes meet only at that accessor. If both edit the
  `onSettled` blocks near `ox.tsx:734`, the rebase keeps the clock block from
  this change and the connection block from #84.

## Check your assumptions

None.

## Test plan

- A visible usage reading counts down and dims while the page is disconnected
  (e2e, new test in `cmd/ox/web/e2e/session.spec.ts` beside the usage specs).
  The existing `clocked` fixture fixes the browser's time to 2026-01-01, while
  the fake harness reports `resets_at` from the daemon's real time, so the test
  installs Playwright's clock at the real current time before the page loads
  (a local `test.extend` overriding `page` with `page.clock.install()`). It
  sends `usage`, sees `2h 0m` or `1h 59m`, aborts `**/events` and restarts the
  relay, waits for the Reconnecting status, then `page.clock.fastForward` three
  hours and asserts the row shows `Waiting` and the `5h usage` meter's
  `aria-valuetext` matches `/Waiting for a new reading/`. It fails today because
  the clock does not tick while disconnected. No existing test can carry it:
  none pairs a usage reading with a lost connection under a controlled clock.
- Polls send nothing while disconnected: kept by the existing
  `"a lost connection keeps the tree and lists nothing until the page has
  reloaded"`, which asserts the first `workspace/list_folder` after the outage
  comes after reload. With the clock ticking through the outage, it fails unless
  `createPoll` gates on `ready`, so it protects the moved gate.
- Polls send nothing while hidden: kept by `"the tree lists nothing while the
  page is hidden"`.

## Implementation plan

1. `cmd/ox/web/src/ox.tsx`: replace the counter with
   `createSignal(Date.now())`; the interval sets `Date.now()` when the page is
   visible, with no `ready` check. Update the comment: the clock is the page's
   time, advancing once a second while the page is visible, and paces every
   request the page repeats.
2. `cmd/ox/web/src/poll.ts`: in the `createEffect` apply function, return early
   when `!untrack(ox.ready)` as well as when a request is in flight. Update the
   doc comment: it runs request now and on each tick while the page is
   connected.
3. `cmd/ox/web/src/Usage.tsx`: drop the `now` memo and the `createMemo` import;
   `left` reads `ox.clock()` in place of `now()`.
4. `cmd/ox/web/e2e/session.spec.ts`: add the test above.
5. `docs/architecture.md`: the `poll.ts` row of the module table.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`: the `poll.ts` row reads "Repeating a request on each
  tick of the provider's clock while the page is connected, at most one in
  flight at a time."
