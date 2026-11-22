# Rewrite the web client tests to Playwright best practice

## Goal

Carry out every finding (F1–F8) and every per-test verdict of the
[web client test audit](../audits/2026-11-22-003-frontend-tests.md), and apply
the same standard to the "on a phone" test the audit skipped. When done, every
spec finds elements by role and accessible name, label, or text, scoped by
landmark; acts as the user does; asserts with web-first assertions on
visibility, text, ARIA state, and focus; and checks no styling. Each test runs
against its own `oxd` and relay, checks one behavior, and waits on events, not
time. The page gains the roles, names, and descriptions those locators need,
with no change a sighted user can see.

The standard is `cmd/ox/web/AGENTS.md` ("Specs test what the user sees and
does"). The todo entry **Rewrite the web client tests to Playwright best
practice** goes with this change.

## Related code

- `cmd/ox/web/e2e/session.spec.ts`, `workspace.spec.ts`, `approval.spec.ts` —
  the specs being rewritten.
- `cmd/ox/web/e2e/ui.ts` — page helpers; becomes the home of the fixtures.
- `cmd/ox/web/e2e/services.ts` — runs `oxd` and the relay; gains `callTool`.
- `cmd/ox/web/playwright.config.ts` — serial today (`workers: 1`).
- `cmd/ox/web/stack.test.mjs` — three tests sleep in place of waiting.
- `cmd/ox/web/src/SessionView.tsx` — transcript, tool calls, composer,
  settings form; unnamed `div`s today.
- `cmd/ox/web/src/SettingsFields.tsx` — `ModelPicker` and `Select`, both
  without stable names.
- `cmd/ox/web/src/Sidebar.tsx` — session list, workspace headers, menus,
  dialogs.
- `cmd/ox/web/src/ApprovalRequest.tsx` — approval card and question form.
- `cmd/ox/web/src/FileTree.tsx` — already nested lists; the header is a `<p>`.
- `cmd/ox/web/src/highlight.ts` — `highlight(path, text)`, the unit under the
  new `node:test` test.
- `cmd/ox/web/src/ox.tsx:491` — the page's one-second `clock`, a
  `setInterval` that Playwright's clock controls.
- `cmd/ox/web/src/connection.ts` — replies arrive over `EventSource`, which
  the cut-reply injection patches.
- `cmd/ox/web/src/style.css` — selectors that follow the markup changes.
- `Makefile` — `test` runs `node --test`.

## Decisions

### Accessibility (F2)

Every change below keeps the page's look. Where markup changes, the
stylesheet's selectors follow it so the rendered page is the same; the user
judges how it looks.

- **Transcript.** `.log` gets `role="log"` and `aria-label="Transcript"`. Each
  entry is named by its kind:
  - A prompt is `<article aria-label="Prompt">` (the `PromptBubble` element
    itself, keeping its classes).
  - A message is `<article aria-label="Message from <from_label>">`, keeping
    its visible sender line.
  - A reply is wrapped in `<article aria-label="Reply">` around `Markdown`.
  - A failed result and an unexpected exit are `<article aria-label="Error">`
    (the existing `.error` element).
  - A tool call stays `<details class="tool">`, the transcript's direct
    child, since `details.tool + details.tool` depends on it, and gets
    `aria-label={tool name}`, making it a `group` named by its tool.
- **Pending inputs.** Queued inputs render inside
  `<section aria-label="Pending">` at the transcript's end, present only while
  there are any. "Pending" is containment: `log.getByRole("region", { name:
  "Pending" }).getByRole("article")`. The section is a flex column with the
  transcript's gap, so the bubbles keep their alignment and spacing. The
  bubbles keep the `queued` class for their dimming.
- **Approval card.** The card's outer element becomes `<article>` named by its
  title through `aria-labelledby` ("Allow Bash?", "Claude asks"). Cards are
  found inside `region "Waiting on you"`.
- **Settings.** `Select` takes a `label` prop rendered as `aria-label`; both
  callers pass "Permission mode", giving `combobox "Permission mode"`.
  `ModelPicker`'s button is named "Model" followed by its visible content, so
  the visible text stays part of the name: a `hidden` span with text "Model"
  and the button's own ID in `aria-labelledby`. Tests find it with
  `getByRole("button", { name: /^Model\b/ })` through one helper, and read its
  value with `toContainText`. `aria-busy` stays the pending state.
- **Session list.** `<nav aria-label="Sessions">`. Each workspace becomes a
  `<section aria-labelledby>` named by an `<h2>` holding the workspace name.
  The `<section>` holds the header row (the former `<p title={path}>`, now a
  `div` keeping its `title`, its context menu, the heading, Workspace actions,
  and New session) and the session `<ul>`. The list stays flat, so the guide
  lines and their CSS stay as they are. Each `<li>` gets
  `aria-level={depth + 1}`, which `getByRole("listitem", { level })` and aria
  snapshots read. The row's button is named by its title span
  (`aria-labelledby`) and described by its status or archived image
  (`aria-describedby`), so `getByRole("button", { name: title, exact: true })`
  finds it and `toHaveAccessibleDescription("background")` reads its status.
  IDs come from `createUniqueId`.
- **Menu reasons.** Each menu item is named by its label span
  (`aria-labelledby`) and described by its reason span (`aria-describedby`).
  Tests assert `getByRole("menuitem", { name: "Delete" })` with
  `toHaveAccessibleDescription(...)` and `toBeDisabled()`.
- **Dialogs.** The add dialog gets `aria-label="Add workspace"`, and its field
  gets `aria-label="Directory"`, keeping its placeholder. The confirmation
  `alertdialog`s get `aria-labelledby` on their `<h2>` and `aria-describedby`
  on their description, so tests assert `toHaveAccessibleDescription` instead
  of `useInnerText` containment.
- **File tree.** The header's workspace name becomes an `<h2>`, styled as the
  `<p>` was. The tree is already nested lists with `aria-expanded` and
  `aria-current`. Listings are asserted with `toMatchAriaSnapshot` on the Files
  panel, which reads order and nesting. Absent entries (hidden names, a
  collapsed folder's children) are asserted with `toHaveCount(0)` on their
  buttons. Notes ("Empty", "permission denied", relay errors) stay `<p>` and are
  found with `getByText` inside their folder's `listitem`.
- Tool input and result `<pre>`s stay unnamed. Tests find their content by
  text, and "expanding shows it" is `toBeVisible()` after a click on the
  summary.

### Fixtures (F5, F7)

- `ui.ts` exports `test` and `expect` from `test.extend`:
  - `config`: an option, default `fakeConfig`, set with
    `test.use({ config })`.
  - `services`: test-scoped. It starts `startServices(config)`, yields, and
    stops in teardown. Teardown keeps `stop()`'s check that `oxd` did not
    exit, including a race report.
  - `ox`: a `Page` that has loaded `services.url` and added
    `services.workspace` through the Add workspace dialog, with the
    workspace's heading visible.
- Tests that need the page before a workspace exists use `page` and
  `services`: the malformed link, Send held for the session list, and the
  cancelled add. An `addWorkspace(page, path)` helper is shared by the fixture
  and these tests.
- Helpers in `ui.ts`, all role-based:
  - `prompt`, `send`, `stop`, `model`, `permissionMode`.
  - `transcript(page)`, `pending(page)`, `waiting(page)` (the "Waiting on
    you" region).
  - `workspaceSection(page, name)`, `sessionButton(page, title)`,
    `sessionItem(page, title)` (the `listitem` that has that button).
  - `openMenu`, `menuItem` (exact name).
  - `newSession(page, text)` (sends and waits for its reply and for Stop to
    go).
  - `ask(page, tool, inputJSON)` (returns the approval article).
  - `sessionId(page)`, `hold`.
  - `rpcCalls(page, method)`: an array the page's `request` listener fills
    with the params of each `/rpc` post of that method.
- `services.ts` gains `callTool(from, name, args)` as a method of `Services`,
  beside the socket it uses.
- With a stack per test, `finally` restores of the config, `count()` branches,
  title-uniqueness, and the hand-rolled stacks in `workspace.spec.ts` all go.
- `playwright.config.ts`: `fullyParallel: true`, no `workers` setting,
  `use.trace: "retain-on-failure"`. Every stack runs on a free port in its own
  temporary directories, so tests share nothing.

### Waiting (F4, F8)

- **Polling under the clock.** Tests of polling call `page.clock.install()`
  before loading the page and `page.clock.pauseAt(...)` once loaded. Each poll
  then happens only on `page.clock.runFor(1000)`. "Nothing listed" is: record
  `rpcCalls`, `runFor` several ticks in the state under test, change the state,
  `runFor(1000)`, await the next request with `page.waitForRequest`, and assert
  that it is the first one recorded since the state began. EventSource
  reconnection is native, so the fake clock does not slow reconnects.
- **Re-reads.** "Unchanged after a re-read" waits on the next
  `workspace/read_file` response with `page.waitForResponse` after
  `runFor(1000)`, then asserts what the user sees. Scroll position uses
  `toHaveJSProperty("scrollTop", n)` and `toHaveJSProperty("scrollLeft", n)`,
  after scrolling with `mouse.wheel`. Text with carriage returns uses
  `toHaveJSProperty("textContent", template)`.
- **Re-render probes.** The `dataset.marked` probes are deleted. "Clicking the
  open file leaves its view alone" becomes the scroll position kept across
  that click and the next re-read.
- **Cut replies.** `cutReply(page, field)` installs the `EventSource` patch
  with `addInitScript`. The patch defines `window.cutNextReply()`, which arms
  it, and reports each withheld reply through a binding exposed with
  `page.exposeBinding`. The helper returns `{ arm(), cut }`, where `cut`
  resolves when a reply is withheld. Every patch also reports each reply's
  result through a second binding, so the retried-creation test can await the
  reply that finds the session already created. It uses that only as a wait;
  its assertions are on the page: the alert, the "Session recovered" status,
  the composer, the transcript, and `rpcCalls(page, "session/send")` being
  empty.
- **Background status.** The test uses `background 60000`, checks the
  background status, settings enabled, Stop absent, and Archive enabled, then
  archives the session. Archiving stops the task, so nothing outlives the test.
- **IME.** Composition is driven with CDP `Input.imeSetComposition` on a
  session from `page.context().newCDPSession(page)`, then `keyboard.press
  ("Enter")`, then `Input.insertText` to commit. The sync point is a real send
  afterwards: the transcript's prompts are exactly the earlier prompt and the
  committed text, so a send during composition would show as an extra prompt.
  The test must fail with `!event.isComposing` removed from `sendOnEnter`. If
  CDP composition does not reach `keydown` with `isComposing`, the
  implementer records that in the work log and keeps the synthetic
  `KeyboardEvent`, with this sync point replacing the immediate check.
- **Settings-popover Enter.** "Enter in the draft's settings sends nothing"
  drops the 500 ms sleep. It then submits for real and asserts that
  `rpcCalls(page, "session/create")` has exactly one entry.
- **Menu replacement.** The 100 ms sleep goes. Opening the workspace menu
  while the session menu is open is followed by choosing "Hide archived" in
  it and asserting the archived row hides. The click proves the new menu
  stayed open past the first menu's close.
- **Menu placement.** Menu placement is `toBeInViewport({ ratio: 1 })`, after
  opening near the window's bottom edge and again after a reason appears.
- **Widths.** Widths after a drag are read with `expect.poll(() =>
  locator.boundingBox()).toMatchObject(...)` or a polled width.
- **`stack.test.mjs`.**
  - "a retiring service that fails stops the stack" drops `sleep(200)`. Once
    `await stack` resolves, `stopping` is set and no start can follow.
  - "a second Enter during a rebuild does not start another" drops
    `sleep(500)`. After `restarted ox`, it asserts one `rebuilding` line and
    the `a rebuild is in progress` line. Both Enter lines are read before the
    first build ends, so a second rebuild would already have logged.
  - "stopping during a restart starts no new process" gives `ox` the trap
    `trap 'touch $0.stopping; sleep 0.5; exit 0' TERM` and waits for that
    file in place of `sleep(300)`. Then `stack.stop(0)` and `await stack`
    replace `sleep(1000)`, followed by waiting until the old `ox` exits.
    Then it asserts that the PID is unchanged and nothing restarted.

### Styling assertions (F3)

These are deleted with nothing in their place:

- `toHaveCSS` on colors and font style.
- `getComputedStyle` reads of padding, font size, opacity, cursor, and
  display.
- `alone()` and `even()`, and header heights.
- Fixed widths of 288 px and 390 − 24 px.
- `scrollWidth > clientWidth`, and span counts standing for highlighting.
- `getAnimations` slide checks.
- The safe-area inset measurements, touch-target heights, and the 16 px font
  size in the phone test.
- The toggles' x positions at either end of the header.

Geometry that is the behavior stays:

- Drags resize within bounds, and the split leaves 240 px a side.
- A session's or draft's split comes back.
- Reload returns each width to the width the test measured at first load, in
  place of 288 px and `even()`.
- A drawer's width on a narrow screen is unchanged by a drag on a wide one,
  compared with its width before the drag.
- The panel toggles keep their box as they move between headers.
- Following output is checked with `toBeInViewport()` and
  `not.toBeInViewport()` on the last reply, in place of
  `getBoundingClientRect` arithmetic.

**Syntax highlighting** moves to `cmd/ox/web/highlight.test.mjs`, a `node:test`
file that imports `./src/highlight.ts`. Node 26 strips its types, and it
imports only packages. It asserts through `highlight(path, text)`:

- Registered languages come back as `<span>` tokens: by extension (`.go`,
  `.TSX`) and by name (`Dockerfile`, `Makefile`).
- Unregistered files, `notes.txt` and `.bashrc` (a dot-first base name), come
  back as escaped plain text with no `<span`.
- The text round-trips: with the tags stripped and character references
  decoded, the output equals the input, CRLF line endings included.

The e2e test keeps only what the page shows: the file's text, its carriage
returns, and its tab.

### Concerns

- **Requirements:** No requirement changes. Every requirement a test guards
  stays guarded under its split. **Touch screens** is styling only, and its
  checks are deleted; the user judges it. **Syntax highlighting** moves to the
  unit test.
- **Construction:** Test isolation is by construction. A test cannot see
  another's sessions, config, or workspace, because each owns its stack. The
  accessible name of each control has one source, the markup, and the specs
  read it.
- **Trust boundaries:** Unchanged. Stacks listen on `127.0.0.1` free ports, as
  today.

## Check your assumptions

None.

## Test plan

Each item names the test and the behavior it guards. Names are the test titles.
Every locator follows the Decisions above. "Keep" tests change only by those
migrations.

### `session.spec.ts`

- **Split** "the first send hands its text and later edits to the new session":
  - "edits typed while a new session is created and sent stay in the composer"
  - "text retyped equal to the sent prompt stays after the send"
  - "text retyped equal to the prompt stays after the creation"
- **Split** "a reply renders as Markdown and its prompt stays plain":
  - "a reply renders Markdown": strong, code, and both lists through roles
    (`strong`, `code`, `list`, `listitem`).
  - "a reply shows raw HTML as text, loads no images, and opens safe links in
    a new tab": `getByRole("img")` count 0, link `target`/`rel`, the
    `javascript:` text shown, no link named "bad".
  - "a prompt shows as the plain text sent"
- **Keep** "a failed first send is retried in the session with its settings".
- **Keep** "a session created after the user moves on leaves the selection
  alone".
- **Split** "a rejected creation keeps the draft and its settings after
  switching views":
  - "Enter in the new-session settings sends nothing"
  - "a rejected creation keeps the draft and its settings after switching
    views"
- **Split** "a pending configuration shows as pending and holds Send until it
  is confirmed":
  - "a pending setting is marked pending and holds Send until confirmed"
  - "a failed setting change shows the error and the settings Claude has"
  - "another page's change during a pending one shows without being marked
    pending"
  - Loader and chevron checks are dropped.
- **Keep** "a reconnect keeps typed text and sends nothing again".
- **Rewrite** "a send cut off after acceptance shows an error without
  resending": `cutReply` with `exposeBinding`, the alert in
  `getByRole("main")`, and `rpcCalls`.
- **Split** "Shift+Enter inserts a newline and Enter during composition doesn't
  send":
  - "Shift+Enter starts a new line"
  - "Enter during input-method composition does not send" (CDP, as above)
- **Split** "overflowing output follows only while the reader is at the
  bottom":
  - "output follows at the bottom"
  - "resizing the composer keeps following"
  - "output does not follow while scrolled up"
  - "scrolling back to the bottom resumes following"
- **Keep** "an oversized prompt fails visibly and stays in the box".
- **Merge** "Stop interrupts a running reply" into "Stop appears once the
  page's send is accepted, and stops it". The Error article reads
  "Interrupted".
- **Rewrite** "a session in the background shows its status and stays
  configurable and archivable": `background 60000`, the status by accessible
  description, archived at the end.
- **Keep** "a send held in one session leaves another usable".
- **Keep** "Allow sends the exact numbers displayed in the tool input": the
  tool group by name and its result by `toContainText`.
- **Keep** "a prompt sent while Claude works stays pending until Claude takes
  it": pending through the Pending region; once taken, the transcript's
  articles in order.
- **Keep** "Stop drops a pending prompt and returns its text to the composer".
- **Keep** "a dropped prompt returns only to the page that sent it".
- **Split** "all waiting inputs show in order with their send-time labels":
  - "pending inputs show in order at the transcript's end and hold Send":
    articles named "Prompt" and "Message from first from A", and the
    transcript's last articles.
  - "a pending message keeps its sender's label after the sender is deleted"
  - "taken messages keep their labels after a reload"
- **Keep** "Stop drops a pending message without touching the composer".
- **Keep** the two "a reconnect reconciles a prompt … while the page was away"
  tests.
- **Split** "AskUserQuestion is answered from a form":
  - "Submit waits for an answer to every question"
  - "choosing an option or Other leaves one answer to a single-select
    question"
  - "submitted answers reach the tool by question text"
  - "Decline denies the question"
  - "a malformed question falls back to Allow and Deny"
- **Keep** "a tool whose process ended with oxd shows stopped on every page":
  the marker by `toHaveAccessibleName("stopped")`.
- **Keep** "a reconnect keeps an expanded tool call open": the input visible,
  not the `open` attribute.
- **Keep** "a session whose harness is no longer offered shows its settings":
  no `finally`.
- **Keep** "reselecting a model repairs a retired effort when there is only one
  choice": no `finally`.
- **Rewrite** "a lost creation reply is recovered after leaving the draft":
  `cutReply`, and the session count by `nav.getByRole("listitem")`.
- **Rewrite** "a retried creation recovers the session without sending": the
  reply binding as its wait, and page-only assertions.
- **Split** "session list membership and ordering don't wait for transcripts":
  - "session list membership and order don't wait for transcripts" (the
    fixture's stack)
  - "a hash change selects the session it names" (**Session URL**)
- **Keep** "a cancelled deletion's reply cannot close the next confirmation":
  the dialog by name.
- **Keep** "on reconnect each session waits for its own settings".
- **Split** "a child session shows beneath its parent with its title", merged
  with "a child's own children list before its next sibling" for order:
  - "a child session shows its title, its directory, and its parent's message,
    also after a reload"
  - "children list beneath their parent, each level one deeper, before the
    parent's next sibling": `aria-level`, through `getByRole("listitem", {
    level })` or an aria snapshot of the session list.
  - "a session with children cannot be deleted"
  - "a waiting descendant keeps its family from being archived"
  - "a child archived alone hides beneath its parent with its own children"
  - "archiving a parent archives the sessions beneath it"
  - "a child cannot be unarchived while its parent is archived"
  - "archived sessions show with an archived marker when shown": the archived
    image and `toHaveAccessibleDescription("archived")`, in place of
    `toHaveClass`.
  - "deleting a family from the leaves up enables each parent's Delete"
- **Split** "an archived session is read-only and hidden until shown":
  - "Archive waits until the session is idle"
  - "Archive and Unarchive are unavailable while not connected"
  - "a failed archive shows its error in the session list"
  - "an archived session shows its transcript and Unarchive in place of the
    composer"
  - "an archived session is archived on every page"
  - "showing archived sessions lasts through hiding the session list"
  - "opening a menu while another is open shows the new one"
  - "a menu opened near the bottom edge stays on screen as it grows"
  - "unarchiving returns the unsent text"
  - "an archive missed while disconnected shows after reconnecting"
  - "a reload hides archived sessions, and an archived session's URL opens it"
- **Delete** "workspace headers keep their height when archived sessions
  overflow the sidebar".

### `workspace.spec.ts`

- **Keep** "a malformed session link still lists workspaces": the
  workspace's heading.
- **Keep** "a draft holds Send until the session list loads".
- **Keep** "a draft keeps its permission mode through a relay restart":
  `rpcCalls`.
- **Keep** "a model without efforts creates a session without effort" (with
  `test.use({ config })`).
- **Split** "a cancelled add or remove cannot settle the next dialog":
  - "a cancelled add cannot settle the next add dialog"
  - "a cancelled removal cannot settle the next removal dialog"
- **Split** "a workspace is removed once it has no sessions":
  - "Remove workspace gives its reason while sessions remain, archived ones
    included"
  - "removing a workspace asks first and shows a failure in the dialog":
    `toHaveAccessibleDescription` for the confirm text.
  - "removing a workspace discards its draft"
- **Split** "the file tree lists the workspace and keeps each session's
  expanded folders", each with `toMatchAriaSnapshot` listings and the clock
  where it polls:
  - "the file tree lists folders first and hides `.git`"
  - "each session keeps its own expanded folders"
  - "the tree shows changes on disk" (`runFor(1000)`)
  - "the tree lists nothing while the page is hidden"
  - "a relay error shows in place of a folder's listing until the next
    listing"
  - "an unreachable relay keeps the last listing"
  - "a lost connection keeps the tree and lists nothing until the page has
    reloaded"
  - "an unreadable folder says why, and an empty folder says Empty"
  - "another workspace's draft shows that workspace's tree": the heading
    names it.
  - The in-flight deduplication part is deleted.
- **Split** "panel toggles and drags change the layout, which lasts until
  reload":
  - "the toggles show and hide the panels from the header they sit in":
    `aria-pressed`, scoped by `main` and the Files panel, the box kept across
    the move.
  - "dragging a sash resizes its panel within bounds, and only while the
    button is down"
  - "the file split leaves each side its minimum and comes back when the file
    reopens"
  - "panel widths are page-wide and each session keeps its own split"
  - "a draft's split carries to the session it creates"
  - "a reload restores the default widths": compared with the first load.
- **Split** "on a phone" / "the page shows one pane at a time", under the same
  `test.describe` and `test.use`, with every styling check deleted:
  - "the session list opens at load only when the URL names no session"
  - "the session list closes on picking a session or tapping outside it"
  - "the file tree drawer closes on tapping outside, from its toggle by
    keyboard, or on opening a file"
  - "an open file replaces the transcript until its tab closes"
  - "panels cannot be resized on a narrow screen": the separators
    `toBeHidden()`.
  - "crossing the breakpoint closes the drawer, and a wide drag leaves the
    drawer's width"
- **Split** "the file viewer shows each session's open file":
  - "clicking a file opens it under its tab and marks its row, and another
    file replaces it": the pane empty while the first read is held.
  - "the pane keeps its scroll position across a re-read and a click on the
    open file" (clock)
  - "the pane shows changes on disk and why a deleted file cannot be read"
    (clock)
  - "the pane keeps a file's carriage returns": `toHaveJSProperty`.
  - "a read error shows in the pane until the next read, and a binary file
    says so"
  - "each session and draft keeps its open file, and a draft's carries to
    its session"
  - "a reload closes the open file"
  - Header height, widths, `scrollWidth`, CSS colors, span counts, and
    `dataset.marked` are deleted.

### `approval.spec.ts` (live)

- **Split** "an approval is answered from its card":
  - "Allow runs the tool"
  - "a typed reply denies with that message"
- **Keep** "an ExitPlanMode plan renders as Markdown": list items by role
  inside the approval article and the tool group.
- **Keep** "an AskUserQuestion is answered from its form".
- All three use `test.use({ config: haikuConfig })` and the fixtures.

### Other tests

- `stack.test.mjs`: the three rewrites above. The other tests are kept.
- `highlight.test.mjs` (new): the **Syntax highlighting** cases above. No
  existing test reaches `highlight.ts` outside a browser.

Each test that a split, rewrite, or merge produces must pass. Each one that
replaces a deleted probe (IME, cut reply, background, clock polling, scroll
kept, menu replacement) must fail when its behavior is broken in the source.
The work log records how.

## Implementation plan

1. **Markup.** Make the F2 changes in `SessionView.tsx`, `Markdown`'s caller,
   `ApprovalRequest.tsx`, `SettingsFields.tsx` (and its callers
   `SessionView.tsx` and `NewSessionView.tsx`), `Sidebar.tsx`, and
   `FileTree.tsx`.
2. **Styles.** Update `style.css` selectors so the page looks the same:
   - the Pending section
   - the workspace section, header, and `h2`
   - the Files header `h2`
   - the transcript articles
   - the approval article

   Check the views in headless Chrome against the `make dev` stack on spare
   ports, before and after, and name the views touched in the report.
3. **Fixtures and helpers.** Write the fixtures and helpers in `ui.ts`, move
   `callTool` into `services.ts`, and set `playwright.config.ts` to
   `fullyParallel`, default workers, and `trace: "retain-on-failure"`.
4. **`session.spec.ts`.** Rewrite it test by test from the table above.
5. **`workspace.spec.ts`.** Rewrite it the same way, including the phone tests.
6. **`approval.spec.ts`.** Rewrite it, and run it with `make live`.
7. **Stack and highlighting tests.** Rewrite the three `stack.test.mjs` tests,
   add `highlight.test.mjs`, and add it to `make test`'s `node --test` line in
   `Makefile`.
8. **Final checks.** Run `make test`, `make e2e`, and `make live`. Grep the
   specs for any remaining:
   - `locator(` with a CSS class or structural selector
   - `waitForTimeout`
   - `toHaveCSS`
   - `getComputedStyle`
   - `dataset`
   - `toHaveClass`
9. **Docs.** Remove the todo entry, and update `cmd/ox/web/AGENTS.md`.

## Requirement changes

None.

## Documentation updates

- `docs/todo.md`: remove **Rewrite the web client tests to Playwright best
  practice**.
- `cmd/ox/web/AGENTS.md`: "Each spec file runs its own `oxd` and relay on a
  temporary socket, database, and port" becomes "Each test runs its own `oxd`
  and relay on a temporary socket, database, and port, through the `services`
  fixture in `e2e/ui.ts`." Also add that `make test` runs `highlight.test.mjs`
  with `node:test` beside the specs. Keep each as one sentence in the existing
  list item.
