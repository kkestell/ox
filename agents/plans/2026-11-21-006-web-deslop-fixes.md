# Web client deslop fixes

## Goal

Fix seven web-client findings from the deslop audit as one change:

- [F5](../audits/2026-11-20-002-deslop.md#f5-enter-on-the-new-session-effort-slider-creates-the-session-and-sends-the-draft): Enter on the new-session effort slider submits the draft form, which creates the session and sends the prompt.
- [F10](../audits/2026-11-20-002-deslop.md#f10-a-successful-send-clears-retyped-text-equal-to-the-sent-prompt): a successful send clears the box whenever its text equals the sent prompt, including text retyped while the send was in flight.
- [F13](../audits/2026-11-20-002-deslop.md#f13-tool-calls-with-only-structured-input-show-a-blank-summary): a tool call whose input has no top-level string, such as `AskUserQuestion`, shows a blank summary.
- [F14](../audits/2026-11-20-002-deslop.md#f14-a-failed-file-post-leaves-the-pane-blank-or-stale-with-no-error): a polled request that the relay answers with an HTTP error is dropped silently, so the file pane stays blank or stale and a folder keeps its old listing.
- [F15](../audits/2026-11-20-002-deslop.md#f15-an-open-sidebar-menu-grows-past-the-viewport): a sidebar menu is clamped to the viewport once, so a reason that appears while it is open pushes it past the edge.
- [F16](../audits/2026-11-20-002-deslop.md#f16-same-page-hash-navigation-leaves-the-old-session-selected): changing the URL's hash in the same document leaves the old session selected.
- [F17](../audits/2026-11-20-002-deslop.md#f17-highlighting-alters-carriage-returns-colors-substitutions-as-strings-and-its-test-misses-recomputation): the file pane's HTML turns carriage returns into newlines, substitutions inside strings take the string color, and the viewer test claims to cover recomputation it cannot see.

All seven were rechecked against `83dd2e6` and still hold.

## Related code

- `cmd/ox/web/src/NewSessionView.tsx:46-84` — `<form class="composer" onSubmit={create}>` wraps both the prompt box and the settings, including the `ModelPicker` popover with its effort `<input type="range">` (`SettingsFields.tsx:153-168`).
- `cmd/ox/web/src/SessionView.tsx:61-94` — the session view's structure: `<div class="composer">` holding `<form class="box" onSubmit={send}>` and the settings beside it. The CSS (`style.css:236-257`) selects `.composer .box` and `.composer .settings`, so it fits either element.
- `cmd/ox/web/src/ox.tsx:157-275` — `createSessionControl`. `post` (`:209-221`) clears the box with `setText((draft) => (draft === prompt ? "" : draft))`. `create` (`:243-262`) reads `text()` at submit and calls `post(prompt)` only after `session/create` replies. `finishPrompt` (`:186-191`) also writes the text.
- `cmd/ox/web/src/toolInput.ts:5-8` — `toolSubject` returns the first top-level string, or `""`. `toolInputJSON` (`:41`) serializes `snapshot(input)` so raw JSON numbers reach `stringify` outside the store proxy.
- `cmd/ox/web/src/SessionView.tsx:144` and `ApprovalRequest.tsx:70` — the two readers of `toolSubject`; the approval card falls back to "Input" for an empty subject.
- `cmd/ox/web/src/connection.ts:36-44`, `:61-79` — the event stream's `onerror` rejects pending requests; `rpc` rejects when not connected, on a non-OK HTTP response, and when `fetch` itself fails, all with a plain `Error`.
- `cmd/ox/web/src/poll.ts:9-33` — `createPoll` keeps only `RpcError` failures and drops all others, under a comment saying the page already reports them.
- `cmd/ox/web/src/FileView.tsx`, `FileTree.tsx:32-36` — the two polls; each shows a `Polled` error in place of its value.
- `cmd/ox/web/src/Sidebar.tsx:113-152` — `Menu` places itself once in `onSettled`; its items' `disabledBecause` text is reactive (`:163-218`).
- `cmd/ox/web/src/App.tsx:11-20`, `:30-42` — `selectionFromHash` initializes `selected`; `select` writes the signal and replaces the hash. Nothing reads the hash after load.
- `cmd/ox/web/src/highlight.ts:124-129` — `highlight` returns highlight.js's HTML, which escapes `&<>"'` and leaves `\r` raw. `FileView.tsx:47` renders it with `innerHTML`.
- `cmd/ox/web/src/style.css:371-386` — the highlight.js colors; no `.hljs-subst` rule.
- `cmd/ox/web/e2e/workspace.spec.ts:228-239` — "A relay failure keeps the last listing" asserts the F14 behavior this change replaces.
- `cmd/ox/web/e2e/workspace.spec.ts:346-370` — the viewer test's highlighting step and its "re-highlighted only when it changes" claim.

## Decisions

- **Draft composer (F5).** `NewSessionView` takes `SessionView`'s structure: `<div class="composer">` holds `<form class="box" onSubmit={create}>` with the textarea and Send, and the settings `<div class="settings">` sits outside the form. No control in the settings can submit the draft.
- **Edits since the send (F10).** The control counts every write to its text in a plain `edits` number, not a signal, since nothing renders it. All writes, including `finishPrompt`'s, go through the control's `setText`, which increments it. A submit captures the count: `send` at the call, `create` when the user submits the draft, so text changed while `session/create` is in flight also counts. `post(prompt, submitted)` clears the box on success only when `edits === submitted`.
- **Tool subject (F13).** `toolSubject` returns the first string in the input at any depth, in key and array order, skipping raw JSON numbers (`isRawJSON` from `@ungap/raw-json`) and walking `snapshot(input)` as `toolInputJSON` does. With no string anywhere it returns the input as compact JSON through the same `stringify`, so the summary is never blank and keeps raw numbers. `AskUserQuestion` shows its first question; `Example {"ids":[9007199254740993]}` shows `{"ids":[9007199254740993]}`. The approval card's `|| "Input"` fallback becomes unreachable and goes; its summary shows the same subject as the transcript.
- **Unreachable ox (F14).** `connection.ts` exports `class Disconnected extends Error`, and rejects with it whenever the page could not reach the relay: `rpc` while not connected, the event stream's `onerror`, `close`, and a `fetch` that rejects (keeping its message). A non-OK HTTP response stays a plain `Error` with the relay's text. `createPoll` keeps its last answer for `Disconnected`, since the page reports the lost connection, and replaces it with an error for every other failure. A relay error with the stream up therefore shows in the file pane, or as the folder's note in the tree, until the next successful poll replaces it. Every other request already shows any rejection through `createAttempt`, and the messages are unchanged.
- **Menu placement (F15).** `Menu` places itself from a `ResizeObserver` on its element, created in `onSettled` after `showPopover()` and disconnected by the cleanup `onSettled` returns. The observer's first callback, after the first layout and before paint, does the initial placement, and each later size change re-clamps from the same request point.
- **Selection from the hash (F16).** `Layout` listens for `hashchange` (added in `onSettled`, removed by its cleanup) and sets `selected` to `selectionFromHash()`. `select` keeps writing the hash with `history.replaceState`, which fires no `hashchange`. The hash is the selection's input from the URL and `select` the input from the page; both write the one signal. An emptied hash selects nothing.
- **Carriage returns (F17).** `highlight` replaces each `\r` in highlight.js's output with `&#13;`. The HTML parser keeps a character reference's carriage return where it normalizes a raw one, so the pane's text equals the file's.
- **Substitutions (F17).** `.hljs-subst { color: var(--foreground) }`. The comment above the highlight rules says text outside every token, and a substitution inside a string, keep the pane's foreground.
- **Recomputation (F17).** The `text` memo stays. The viewer test's comment drops the claim that it observes re-highlighting and says what `data-marked` shows: the pane is not re-rendered while the file is unchanged.
- **Requirements:**
  - **Only the user sends** and **Creating a session**: Enter in the draft's settings sends nothing; only the prompt box submits.
  - **Composer after a send**: the box keeps anything changed since the submit, through a held `session/create` and a held `session/send`. A failed send leaves the box alone as before. A dropped prompt's return through `finishPrompt` counts as a change, so a send in flight at that moment keeps the returned text.
  - **Tool calls**: every call shows a summary of its input, on every page and after a reload, since the summary is derived from the stored input.
  - **Every request settles**, **File tree**, **File viewer**: a polled read or listing the relay refuses shows its error in the pane or folder that made it, and the next answer replaces it. **Lost connection** holds: a lost connection keeps the last answer, and polling waits for the reload as before.
  - **Removing a workspace** and **Archiving a session**: a menu reason that appears while the menu is open stays on screen.
  - **Session URL**: the selected session follows the URL after any hash change in the document.
  - **File viewer** and **Syntax highlighting**: the pane's text keeps the file's carriage returns, and substitutions show in the pane's foreground.
- **Construction:** The draft's settings sit outside any form, so no control there can submit. Whether the box changed since a submit has one source, the edit count every write passes through. Whether a failure means ox is unreachable is decided once, in `connection.ts`, by the error's type.
- **Ownership:** `edits` is owned by the session control, read and written only inside it. The `ResizeObserver` and the `hashchange` listener are owned by their components and removed by their `onSettled` cleanups. None is stored or retried.

## Check your assumptions

None.

## Test plan

- Enter on the draft's effort slider (F5): e2e. In "a rejected creation keeps the draft and its settings after switching views" (`session.spec.ts:129`), before the permission mode is chosen: record every `/rpc` request whose method is `session/create` with `page.on("request")`, fill the prompt, open `button.picker`, press Enter on the `Effort` slider, press Escape, wait 500 ms, and assert no `session/create` was posted, the header is still "New session", and the prompt keeps its text. Fails before the fix with a create and a send.
- Retyped text equal to the sent prompt (F10): e2e. Extend "the first send hands its text and later edits to the new session" (`session.spec.ts:31`). After it, hold `session/send`, fill and send "same text", wait for the hold, `fill("")` then `fill("same text")`, release, wait for the reply, and assert the box holds "same text". Then add the draft path: open a new session, hold `session/create` and `session/send`, fill and submit "draft text", wait for the create, clear and retype "draft text", release both, and assert the box holds "draft text" after the reply. Both fail before the fix with an empty box.
- Structured input summary (F13): e2e. In "AskUserQuestion is answered from a form" (`session.spec.ts:641`), after the first answer, assert the last `AskUserQuestion` tool's `.detail` is "Which color?". In "Allow sends the exact numbers displayed in the tool input" (`:396`), assert the `Example` tool's `.detail` equals `input` exactly, which checks the compact JSON fallback and its raw numbers. Both are blank before the fix.
- Relay error on a poll (F14): e2e. In `workspace.spec.ts`, replace the "A relay failure keeps the last listing" step: with `failSrc` routed, assert the tree shows "relay unavailable" for `src`; unroute and assert the rows return to the full listing. In the viewer test, before clicking `bin.dat`, `hold(page, "workspace/read_file", { path: "bin.dat" })`, click, wait for the hold, `fail("relay unavailable")`, and assert the panel shows "relay unavailable", then "file is binary" on the next read. The existing "A lost connection lists nothing and keeps the tree" step guards the `Disconnected` condition.
- Menu growing while open (F15): e2e. In the bottom-edge clamp step of "an archived session is read-only and hidden until shown" (`session.spec.ts:1045-1058`), with the Unarchive menu open at the reduced viewport: `hold(page, "workspace/list")`, `services.restartRelay()`, wait for the hold, assert Unarchive's accessible name is "Unarchive Not connected", and assert the menu's bottom is still within 1 px of the viewport's. Then Escape and release. Fails before the fix by the height of the reason line.
- Hash navigation (F16): e2e. At the end of "session list membership and ordering don't wait for transcripts" (`session.spec.ts:819`), with "younger session" selected, set `location.hash` to the older session's encoded ID with `page.evaluate`, and assert `sessionButton(page, "older session")` has `aria-current="page"` and the reply is "You said: older session".
- Carriage returns and substitutions (F17): e2e. In the viewer test, write `src/template.ts` as ``const s = `a ${b}`;\r\nlet c = 1;\rlet d = 2;\n``, open it, and assert the panel's `textContent` equals the file exactly, and that `.hljs-subst` has the panel's computed `color`, read with `getComputedStyle`. Reword the "re-highlighted only when it changes" comment as the Decisions say.

## Implementation plan

1. `cmd/ox/web/src/NewSessionView.tsx`: replace `<form class="composer" onSubmit={create}>` and its `<div class="box">` with `<div class="composer">` holding `<form class="box" onSubmit={create}>` (textarea and Send) and, after the form, the `<Show>` with the settings.
2. `cmd/ox/web/src/ox.tsx`: in `createSessionControl`, rename the signal setter and add `edits` with a `setText` that increments it, used by every write, including `finishPrompt` and the exported `setText`. Change `post(prompt)` to `post(prompt, submitted)` and clear the box on success only when `edits === submitted`. `send` passes `edits`; `create` captures `edits` beside `text()` and passes it. Update `post`'s comment to say text changed since the submit stays in the box.
3. `cmd/ox/web/src/toolInput.ts`: rewrite `toolSubject` as the Decisions say, with a comment naming both cases. `cmd/ox/web/src/ApprovalRequest.tsx`: drop `|| "Input"` from the subject.
4. `cmd/ox/web/src/connection.ts`: add `export class Disconnected extends Error {}` with a comment saying it means the page could not reach ox. Use it for "Not connected to ox", the `onerror` error, "Closed", and the `fetch` rejection (`.catch((e: Error) => fail(new Disconnected(e.message)))`). `cmd/ox/web/src/ox.tsx` needs no change: `onError` passes the error through.
5. `cmd/ox/web/src/poll.ts`: replace the `RpcError` check with `!(e instanceof Disconnected)` and reword the comment: only an answer replaces the last one, and a failure to reach ox keeps it, since the page reports the lost connection.
6. `cmd/ox/web/src/Sidebar.tsx`: move the placement into `place()`, observe the menu with a `ResizeObserver` calling it, focus the first enabled item, and return `() => observer.disconnect()` from `onSettled`. Update the comment to say the menu stays on screen as its size changes.
7. `cmd/ox/web/src/App.tsx`: add an `onSettled` in `Layout` that listens for `hashchange` and calls `setSelected(selectionFromHash())`, returning the removal. Reword the comment on `selectionFromHash`: the hash holds the selected session, so a reload or a hash change selects it.
8. `cmd/ox/web/src/highlight.ts`: append `.replaceAll("\r", "&#13;")` to the result, and add to the comment that a carriage return is written as a character reference so the parser keeps it.
9. `cmd/ox/web/src/style.css`: add `.hljs-subst { color: var(--foreground); }` and reword the comment above the highlight rules.
10. `cmd/ox/web/e2e/session.spec.ts` and `cmd/ox/web/e2e/workspace.spec.ts`: the tests above.
11. `docs/todo.md`: remove the entries for F5, F10, F13, F14, F15, F16, and F17.

## Requirement changes

None.

## Documentation updates

- `docs/todo.md`: remove the seven entries this change fixes.
