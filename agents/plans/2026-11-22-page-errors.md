# The page shows and recovers from its own failed requests

## Goal

Resolve [#28](https://github.com/kkestell/ox/issues/28), findings F2, F3, and F7 of `agents/audits/2026-11-22-deslop-audit.md`:

- **F2.** The page's alerts render inside `<main>`, which a narrow screen hides while a file is open, so a phone user reading a file sees no lost connection and no failed creation.
- **F3.** After a reconnect, a `session/get`, `harness/list`, or `workspace/list` that fails while the event stream stays healthy is never repeated. The session keeps its old transcript and a disabled Send, or the whole page stays held, until the next reconnect or reload.
- **F7.** A failed `fetch` of `/rpc` rejects as `Disconnected`, which the pollers drop, so a listing or file read stays blank or stale with no error. An empty error body becomes `Error("")`, which the views hide, so a failed send or creation loses its error and its **Reopen draft** link.

When done, page-level alerts show in every layout, the page's reload repeats a failed request until it is answered or the stream drops, and every failed request carries a non-empty error.

## Related code

- `cmd/ox/web/src/connection.ts:80-84` — turns a relay answer or a failed `fetch` into a request's error. `Disconnected` is the one class the page treats as the stream's failure.
- `cmd/ox/web/src/poll.ts:24` — drops `Disconnected`, keeping the last answer. Correct once `Disconnected` means only a lost stream.
- `cmd/ox/web/src/ox.tsx:362-428` — `load` and `reload`, the page's resync after each connection; `:462` loads a session announced by `session/created`; `:489` `fail`; `:491` the clock comment; `:503-517` the stream handlers.
- `cmd/ox/web/src/App.tsx:150-214` — `.center` holds `<main>`, with the page's alerts as its first children, then the file viewer's sash and `FileView`.
- `cmd/ox/web/src/style.css:224-230,450` — `.center`, `main`, `main > [role="alert"]`, and `.narrow main:has(~ .viewer) { display: none; }`.
- `cmd/ox/web/src/Panels.tsx:97-98` — a sash measures its previous and next siblings, so `<main>`, the viewer's sash, and `.viewer` stay siblings.
- `cmd/ox/web/e2e/ui.ts` `hold` — holds one request, then passes it on or answers it as a `502` with a body.

## Decisions

- **`Disconnected` means only a lost stream.** It stays for the stream's own error, the requests `failPending` rejects, a request made with no connection, and the relay's `503` (the relay's `oxd` connection is gone, so the stream drops with it). A rejected `fetch` is the request's own failure: `new Error("Could not reach ox")`. The page reports every `Disconnected` through the stream's alert, so the pollers and the reload drop it, as `poll.ts` already does.
- **No empty error.** `connection.ts` builds a relay error's message once, before choosing its class: the trimmed body, or else `` `${response.status} ${response.statusText}`.trim() ``. The check lives at the boundary, so no view tests for an empty error.
- **The reload retries; the page alert shows the failure.** `ox.tsx` gains `retry(request)`: it calls the request, and on a failure other than `Disconnected` it sets the page error to the failure's message, waits one second with `setTimeout`, and calls it again. It returns the answer, clearing the page error if that error is still the message it set, or returns `undefined` once a request fails with `Disconnected`. `reload` wraps `harness/list` and `workspace/list` in it and stops on `undefined`. `load` wraps `session/get`, mapping a `notFound` `RpcError` to `undefined` inside the retried request, and stops on `undefined`. `onConnect` and the `session/created` branch call `void reload()` and `void load(id)` with no `.catch`. `onError` calls `setError(error.message)` directly, and `fail` goes, since no other caller is left. The retry is paced by its own timer, not the clock, because the clock stops until `reload` sets `ready`.
- **Page alerts sit above both panes.** `.center` becomes a column: the page error and the failed-draft alerts first, then a new `<div class="panes">` holding `<main>`, the viewer's sash, and `FileView`, which keeps them siblings for the sash and for `.narrow main:has(~ .viewer)`. The alerts take `main > [role="alert"]`'s padding and size, with the pane background. In the wide layout they span the transcript and an open file; on a narrow screen they stay above the file viewer.
- **Requirements:**
  - *Lost connection.* The stream's alert stays the page error while the stream is down: a retry that wakes after the stream dropped gets `Disconnected` at once and returns without setting the error, and a request already in flight is failed by `failPending` before its `fetch` can fail it. The alert now shows with a file open on a phone. Send and settings stay held until each session's snapshot loads, as before, and that load now finishes.
  - *Failed creation.* Its alert and **Reopen draft** show in every layout, and an empty relay answer gives it a message, so the alert renders.
  - *Every page in sync* and *One transcript.* A session whose snapshot failed keeps retrying until it goes live, after which `receive` applies its updates. A reconnect during the one-second wait starts a fresh `reload` while the old retry may still answer on the new connection. Each answer is consistent with the stream at its reply, so both orders are valid and no guard is needed.
  - *Every request settles.* A failed listing or file read shows its error in place of the answer. A send, creation, or reload failure always has text. A reload failure shows in the page alert until the retry is answered.
- **Ownership:** the retry belongs to the provider in `ox.tsx`, which owns the reload. Its only state is the loop inside one `retry` call and its one-shot timer, which ends when the request is answered or the stream drops; nothing outlives it. The user-visible behavior that needs it is a session catching up after a failed snapshot. Its race is the reconnect overlap above. The commit body states both.

## Check your assumptions

1. **A session that fails to load retries by itself.** (Kept)
   - **Plan:** When a session, or the session list, fails to load after a reconnect while the connection is healthy, the page shows the error at the top and tries again every second. The error goes away and the session catches up on its own once a try succeeds.
   - **Alternative:** The session shows its own error with a Retry button and waits for the user. It needs a new control in the session view, and the user has to notice and press it before the session can send again.
2. **The page's alerts move above the file viewer too.** (Kept)
   - **Plan:** The lost-connection alert and a failed draft's alert with **Reopen draft** show at the top of the page's center, across both the transcript and an open file, so a phone reading a file still sees them.
   - **Alternative:** Keep them above the transcript alone on a wide screen and add a second copy over the file viewer on a phone. Two places show the same alerts, and the layout differs between screen widths.

## Test plan

- **Empty relay answer on a failed creation (e2e, new).** `session.spec.ts`, "a creation failed by an empty relay answer shows its error with Reopen draft": hold `session/create`, move to another session, `fail("")`, expect a page alert containing `502` with a **Reopen draft** button that returns to the draft. No existing test fails a creation at the relay while the user is away; "a rejected creation keeps the draft" covers an `oxd` refusal and stays.
- **A failed snapshot and a failed listing after a reconnect are retried (e2e, new).** `session.spec.ts`, "a reconnect repeats a failed request until the page catches up": two sessions and a second page on the first. Hold `workspace/list` and the first session's `session/get`, restart the relay, `fail` the listing, expect its page alert, then `fail` the snapshot and expect its page alert. Send a prompt from the second page, then expect the first page to show it in that session, Send enabled, and no page alert. "on reconnect each session waits for its own settings" holds and releases a snapshot, so it cannot carry a failure.
- **Phone with a file open sees the lost connection (e2e, new).** `workspace.spec.ts`, inside "on a phone": open `a.txt`, abort `**/events` and restart the relay, expect the viewer visible and the page alert "Lost the connection" visible. No phone test cuts the connection.
- **A failed listing shows its error (e2e, extended).** `workspace.spec.ts`, "a relay error shows in place of a folder's listing until the next listing": loop over three answers to the `src` listing, each routed once and then unrouted: a `502` with a body (expect its text), an aborted request (expect `Could not reach ox`), and an empty `502` (expect text containing `502`). After each, the next tick lists `a.txt` and the error goes.
- **A lost oxd connection keeps the listing (e2e, replaced).** "an unreachable relay keeps the last listing" keeps only the `503` case, renamed "a relay that lost its oxd connection keeps the last listing". The aborted case moves to the test above, with the new behavior.
- **Locators.** Specs that find the page alert under `main(page)` find it from the page: `session.spec.ts:246-250` (the rejected creation's **Reopen draft**) and `:649`. Specs that assert `main`'s alerts after a reconnect (`:340`, `:951`) keep checking `main`, which now holds only the view's error.

## Implementation plan

1. `cmd/ox/web/src/connection.ts`: build the relay error's message with the status fallback; reject a failed `fetch` with `new Error("Could not reach ox")`; reword the `Disconnected` comment to say it is a request's failure when the page's event stream, or the relay's `oxd` connection behind it, is gone.
2. `cmd/ox/web/src/poll.ts`: reword the comment to say `Disconnected` means the stream is lost, which the page reports. No code change.
3. `cmd/ox/web/src/ox.tsx`: import `Disconnected`; add `retry`; route `reload`'s two lists and `load`'s `session/get` through it; drop `fail` and the `.catch` calls; have `onError` set the error directly; change the clock comment to "paces every request the page polls".
4. `cmd/ox/web/src/App.tsx`: move the page error and failed-draft alerts out of `<main>` to the start of `.center`, and wrap `<main>` and the file viewer's `<Show>` in `<div class="panes">`.
5. `cmd/ox/web/src/style.css`: make `.center` a column; add `.panes { flex: 1 1 0; min-height: 0; display: flex; }`; replace `main > [role="alert"]` with `.center > [role="alert"]`, adding `flex: none` and `background: var(--pane)`; drop `height: 100%` from `main` and `.viewer`, which the row now stretches.
6. The e2e changes in the test plan.
7. `docs/architecture.md`: one sentence after "The page shows stream errors while reconnecting…", stating that the provider repeats a reload request that fails while the stream is connected, each second, showing the failure as the page error until it is answered.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md` — the web client's reload repeats a failed request.
