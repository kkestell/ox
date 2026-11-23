# A POST that cannot reach the relay drops the page's connection

## Goal

Fix the regression from #38 (`e5b32ec`). A POST that cannot reach the relay rejects with `Error("Could not reach ox")`. When the relay restarts, a file-tree poll's POST can fail before the event stream reports the drop, so the poller shows "Could not reach ox" in place of the tree's listing, and keeps it until the page reconnects. This makes "a lost connection keeps the tree and lists nothing until the page has reloaded" (`cmd/ox/web/e2e/workspace.spec.ts:408`) fail on CI.

When done, `connection.ts` alone decides that a POST which cannot reach the relay means the page's connection is lost. It handles that exactly as a stream error: the page says the connection dropped, pending requests fail as `Disconnected`, and the page reconnects. F7 of `agents/audits/2026-11-22-deslop-audit.md` still holds: a failed POST under a healthy stream shows an error, the lost-connection alert, and the page recovers by reconnecting.

## Related code

- `cmd/ox/web/src/connection.ts:25-99` — `connect` opens the one `EventSource`, holds `connection` (the relay's ID for the page's `oxd` connection), and rejects a failed `fetch` with `Error("Could not reach ox")` at `:85`. `source.onerror` clears `connection`, fails pending requests, and reports the drop.
- `cmd/ox/web/src/poll.ts:24` — keeps the last answer on `Disconnected`, showing any other error in place of the answer.
- `cmd/ox/web/src/ox.tsx:362-376` — `retry` returns `undefined` on `Disconnected`, retrying any other error each second.
- `cmd/ox/web/src/ox.tsx:523-541` — `onConnect` reloads; `onError` holds every session and sets the page error.
- `cmd/ox/web/e2e/workspace.spec.ts:364-390` — "a relay error shows in place of a folder's listing until the next listing" asserts an aborted POST shows `Could not reach ox`.

## Decisions

- **The connection decides.** A rejected `fetch` means the page cannot reach the relay, which also carries its event stream, so it is a lost connection, not the request's own failure. `connect` gets one function, `lose(error)`, that both `source.onerror` and a rejected `fetch` call: it clears `connection`, fails every pending request with `error`, and calls `handlers.onError(error)`. The rejected `fetch` additionally replaces the stream: it closes `source` and opens a new `EventSource` with the same listeners, since a stream the browser still holds open would never reconnect and never call `onConnect`. The error is `new Disconnected("Lost the connection to ox. Reconnecting.")`, the stream error's own text.
- **Only the connection the POST was sent on is dropped.** Each `rpc` captures `connection` when it sends. A rejected `fetch` calls `lose` only if `connection` still equals that ID; otherwise the stream already reported the drop, or the page has reconnected, and the request is failed with `Disconnected` alone. So a POST that fails after the stream's own error does not churn the stream, and a stale POST cannot drop a newer connection. No timer.
- **Consumers stay as they are.** `poll.ts` and `retry` already treat `Disconnected` as the page's own report, so the poller keeps the tree and the reload stops. "Could not reach ox" goes away.
- **Requirements:**
  - *Lost connection.* The page says the connection dropped whichever of the stream or a POST notices first, fails pending requests, and holds Send and settings until each session reloads, through the existing `onError` and `onConnect`. If `oxd` or the relay is still down, the replacement stream fails as any reconnect does, and a refused stream still asks for a reload.
  - *Every request settles.* A POST that cannot reach the relay settles with the lost-connection error; the page alert shows it, and the reconnect's reload repeats the page's state.
  - *Send cut off.* A send whose POST fails shows the lost-connection error, as one failed by the stream does.
  - *Every page in sync* and *One transcript.* A healthy stream replaced after a failed POST reconnects through `onConnect`, which reloads everything, as after any reconnect.
- **Construction:** `connect` is the one place that maps a transport failure to `Disconnected`, and `lose` is the one path that reports it; no consumer tells a lost connection from a failed request.
- **Ownership:** no new state. The replaced `EventSource` belongs to `connect`, as the first one does; closing the old one stops its events, and `close()` closes whichever is current.

## Check your assumptions

None.

## Test plan

- **A POST that cannot reach the relay is a lost connection (e2e, replaced).** In "a relay error shows in place of a folder's listing until the next listing", drop the aborted case from the loop. Add the case to "a relay that lost its oxd connection keeps the last listing", renamed "a lost connection keeps the last listing": after its `503` case, abort the next `src` listing POST while the stream is healthy, then expect the page alert "Lost the connection", `a.txt` still listed, and no `Could not reach ox`; unroute, and expect Send enabled and the alert gone once the page reconnects. It fails before the change, which shows `Could not reach ox` in place of the listing.
- **The flaky test.** "a lost connection keeps the tree and lists nothing until the page has reloaded" needs no change; with a failed POST now a lost connection, its outcome no longer depends on whether the stream or a poll notices the restart first.

## Implementation plan

1. `cmd/ox/web/src/connection.ts`: move the `EventSource` creation and its three listeners into an `open()` that assigns `source`; add `lose(error)` used by `onerror`; in `rpc`, capture the connection ID sent with, and on a rejected `fetch` either replace the stream and `lose(...)` when it is still current, or fail the request with `Disconnected`. Update the `Disconnected` comment: a request's failure when the page's connection to `oxd`, through the relay and its event stream, is gone.
2. `cmd/ox/web/src/poll.ts`: reword the comment so `Disconnected` means a lost connection, not only a lost stream.
3. The e2e changes in the test plan.
4. `docs/architecture.md`: in the paragraph beginning "The page shows stream errors while reconnecting", state that a request that cannot reach the relay drops the connection as a stream error does, and the page reconnects.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md` — how the page treats a request that cannot reach the relay.
