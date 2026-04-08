# Event list wheel scroll

## Goal

Add mouse-wheel scrolling to the conversation/event list and show a one-column scrollbar on the far right of the conversation area whenever the rendered rows overflow the available height.

## Desired outcome

- The mouse wheel scrolls the conversation by whole rows.
- The rightmost conversation column is reserved for a scrollbar only when overflow exists.
- The scrollbar track is drawn with `│`.
- The scrollbar handle is drawn with `█`, shrinks as the content grows, and never becomes shorter than one row.
- When the conversation fits in the viewport, there is no scrollbar and the content keeps the full width.
- When the user is at the bottom, new events keep auto-following the tail; when the user has scrolled up, new events do not yank the viewport back to the bottom.

## How we got here

The requested behavior is concrete enough that brainstorming was not needed. Repo exploration showed that `EventList` is currently a pure row producer, while `Viewport` owns the terminal geometry and tail-clipping policy. The terminal stack already parses wheel input, but Ox does not currently turn those mouse events into viewport state changes. That makes `Viewport` the clean place to own scroll state and scrollbar rendering, with mouse-wheel events wired in at the Ox entry point.

## Approaches considered

### Option 1

- Summary: Keep `EventList` as a pure renderable, add conversation scroll state and scrollbar rendering to `Viewport`, and route wheel events directly from `TerminalInputSource` to a viewport mouse handler.
- Pros: Keeps terminal-size concerns in `Viewport`; preserves `EventList` as content-only; allows wheel scrolling during streaming turns because it does not depend on `InputCoordinator.ProcessPendingInput`.
- Cons: Adds a bit more state and thread-safety responsibility to `Viewport`.
- Failure modes: If clamping and redraw rules are sloppy, resize and append behavior can jump unexpectedly.

### Option 2

- Summary: Put scroll state in `Viewport`, but subscribe through `InputCoordinator.MouseReceived`.
- Pros: Reuses the existing serialized input abstraction.
- Cons: `InputCoordinator` is only drained while `InputReader.ReadLineInViewport` is polling; wheel scrolling would freeze during turns unless input pumping is re-architected.
- Failure modes: A partial implementation would look correct while idle but fail as soon as the assistant starts streaming.

### Option 3

- Summary: Move scroll state and scrollbar rendering into `EventList`.
- Pros: Gives `Viewport` a smaller API surface.
- Cons: Pushes terminal geometry, overflow policy, and input-driven state into a content tree that currently only knows how to render rows for a width.
- Failure modes: `EventList` becomes responsible for viewport height and input semantics, which is the wrong abstraction boundary.

## Recommended approach

- Why this approach: Option 1 fits the existing architecture. `Viewport` already decides how many rows fit on screen, and it is the only layer that knows when one column can be traded for a scrollbar. Keeping wheel handling close to the terminal input source also avoids the current coordinator-drain limitation during turns.
- Key tradeoffs: `Viewport` becomes slightly more stateful, so the implementation should extract the scroll-window and scrollbar-geometry math into small helpers instead of growing `BuildFrame` inline.

## Related code

- `src/Ox/Rendering/Viewport.cs` — current tail-clipping logic lives here; this file should own manual scroll state and scrollbar drawing.
- `src/Ox/Rendering/EventList.cs` — produces the full list of rendered rows; it should stay width-driven and unaware of viewport height or mouse input.
- `src/Ox/Program.cs` — wires the input stack, viewport, and REPL lifecycle; this is where Ox can subscribe mouse-wheel events to viewport scrolling.
- `src/Te/Input/TerminalInputSource.cs` — proves wheel events already reach the app from the terminal layer.
- `src/Te/Input/InputCoordinator.cs` — important constraint: queued mouse events are only dispatched when `ProcessPendingInput` is called.
- `src/Te/Input/MouseFlags.cs` — defines `WheeledUp` and `WheeledDown`.
- `tests/Ur.Tests/TuiRenderingTests.cs` — existing viewport rendering tests; the new scroll and scrollbar coverage belongs here.
- `docs/boo.md` — PTY-based verification workflow for confirming the final behavior in a real terminal session.

## Current state

- Relevant existing behavior: `Viewport.RenderConversation` always shows the tail of `EventList.Render(width)` and provides no manual scrollback.
- Existing patterns to follow: render-time layout decisions live in `Viewport`, while renderables like `EventList` stay focused on row generation and raise `Changed` when their content mutates.
- Constraints from the current implementation: `InputReader` drains `InputCoordinator` only while reading the composer input, so a coordinator-only mouse solution would not remain responsive during active turns.

## Structural considerations

Hierarchy: the terminal interaction stays in Ox. Te continues to expose low-level mouse flags, but it should not learn conversation semantics. `EventList` remains a content tree, and `Viewport` remains the display policy layer.

Abstraction: the change should introduce a small scroll model inside `Viewport` rather than scattering row-count arithmetic through `BuildFrame`. The viewport should reason in terms of "first visible row", "is overflowing", and "scrollbar geometry", not raw index math everywhere.

Modularization: if the geometry math becomes noisy, extract it into private or internal helper methods in `Viewport` rather than creating a new top-level type prematurely.

Encapsulation: avoid exposing mutable scroll state publicly. Prefer a narrow viewport API such as `HandleMouse(MouseEventArgs args)` or `ScrollConversation(int deltaRows)` and keep the rest private/internal for tests.

## Refactoring

- Extract conversation window computation from `RenderConversation` into helper methods before adding wheel behavior. This keeps the feature code from being bolted directly into the current tail-clipping branch.
- Extract scrollbar geometry calculation into a dedicated helper so the proportional handle-size and handle-position rules are easy to test independently.

## Research

### Repo findings

- Finding: `TerminalInputSource` already enables SGR mouse reporting by default, so no terminal-protocol work is needed for wheel input.
- Finding: `Viewport` is the only layer that knows the conversation width and height after footer/sidebar layout is applied.
- Finding: `EventList.Render(width)` already returns the full rendered conversation, which gives the viewport enough information to compute overflow and scroll windows without changing the event model.
- Finding: current unit tests already inspect `Viewport._buffer`, which is a good fit for asserting scrollbar glyphs and visible-row selection.

### External research

- Source: none.
- Why it matters: local repo patterns were sufficient; no external dependency or protocol uncertainty remains.

## Implementation plan

- [ ] Add viewport-owned conversation scroll state and clamping rules. Track whether the user is following the bottom or reading older rows, and preserve the visible window when new events append above the footer.
- [ ] Refactor conversation rendering in `Viewport` so it computes `(contentWidth, scrollbarVisible, firstVisibleRow)` from the total rendered row count and viewport height before writing rows into the buffer.
- [ ] Render the scrollbar inside `Viewport` when overflow exists: reserve the rightmost conversation column for the track, draw `│` for the full viewport height, compute a proportional `█` handle with minimum height `1`, and place it according to the current scroll position.
- [ ] Add a narrow viewport mouse API and wire `TuiService` to send wheel events to it from `TerminalInputSource`. Ignore wheel input outside the conversation pane and keep the handler safe to call from the background input thread.
- [ ] Add unit tests in `tests/Ur.Tests/TuiRenderingTests.cs` for: no-overflow/no-scrollbar, overflow showing the scrollbar, handle shrinking with more rows, minimum one-row handle, top/middle/bottom handle placement, wheel scrolling clamped at bounds, and append behavior while scrolled up versus at bottom.
- [ ] Add or update code comments around scroll ownership and the direct mouse-event wiring so the architecture stays clear.

## Impact assessment

- Code paths affected: conversation rendering, mouse-input wiring, and viewport redraw invalidation.
- Data or schema impact: none.
- Dependency or API impact: likely an additional small viewport method and one Ox-level event subscription; no external API or persistence changes.

## Validation

- Tests: extend `tests/Ur.Tests/TuiRenderingTests.cs` with rendering and scroll-window assertions; add focused input tests only if a new helper outside `Viewport` warrants them.
- Lint/format/typecheck: run `dotnet test`.
- Manual verification: use the Boo PTY workflow from `docs/boo.md` to confirm that wheel scrolling works in a real terminal, the scrollbar only appears on overflow, and the handle tracks top/middle/bottom positions correctly while events stream.
