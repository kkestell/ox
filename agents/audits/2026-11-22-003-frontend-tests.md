# Web client test audit

## Scope and evidence

This audit reads every web client test at `0484a03` (`mobile-layout`, on
`codex-fixes` at `78f8c2d`) against Playwright's best-practices guide and
Testing Library's guiding principle: a test should use the page the way a user
does. It covers `cmd/ox/web/e2e/session.spec.ts`, `workspace.spec.ts`,
`approval.spec.ts`, the helpers `ui.ts`, `services.ts` and `global-setup.ts`,
`cmd/ox/web/playwright.config.ts`, and `cmd/ox/web/stack.test.mjs`. It skips
the "on a phone" test in `workspace.spec.ts`, which is being rewritten
elsewhere.

The audit read the specs and the markup they query in `cmd/ox/web/src`
(`SessionView.tsx`, `Sidebar.tsx`, `SettingsFields.tsx`, `ApprovalRequest.tsx`,
`FileTree.tsx`, `FileView.tsx`, `NewSessionView.tsx`, `App.tsx`). It checked
each test against `docs/functional-requirements.md` at the same revision. For
requirements with no web test, it searched the Go test names and
`internal/server/methods_test.go`.

Line numbers refer to `0484a03`.

Gaps:

- No test was run. The verdicts come from reading the code. Flakiness claims
  rest on timing visible in the source, not on observed failures.
- `stack.test.mjs` is a `node:test` suite for the dev stack runner, not a
  Playwright spec. It is judged on isolation and timing only. It guards
  developer tooling, which no functional requirement covers.

## Summary

| ID | Cost   | Action | Title                                                         |
| -- | ------ | ------ | ------------------------------------------------------------- |
| F1 | High   | Plan   | Tests find elements by CSS class and DOM structure            |
| F2 | High   | Plan   | The page lacks the roles and names role-based locators need   |
| F3 | High   | Plan   | Tests assert styling and pixel arithmetic                     |
| F4 | Medium | Plan   | Fixed sleeps and DOM-marker probes stand in for awaited events |
| F5 | Medium | Plan   | One daemon is shared by every test in a file                  |
| F6 | Medium | Plan   | Long scenarios check many behaviors under one name            |
| F7 | Low    | Plan   | Helpers are hand-rolled where fixtures fit                    |
| F8 | Low    | Plan   | Non-retrying reads of page state                              |

Per-test verdicts across 60 tests: 33 keep, 17 split, 7 rewrite, 1 merge,
2 delete. See [Verdicts](#verdicts).

## F1. Tests find elements by CSS class and DOM structure

High · Plan

Most assertions locate their target through class names the user never sees:

- `.reply`, `.prompt`, `.prompt.queued`, `.prompt.message`, `.from`,
  `.error`, `.approval`, `details.tool`, `.name`, `.detail`, `.card pre`,
  `.plan li`, and `summary .subject` in the transcript.
- `select[name=permission_mode]`, `button.picker`, `.settings .picker .model`,
  `.select .loader`, and `.select .chevron-down` in the settings.
- `nav button.open` and `nav li` (`ui.ts:19–20`), `nav li .title`,
  `nav > p`, `.status`, and `.composer.archived p` in the sidebar and
  composer.
- `.row`, `.note`, and `files.locator("p")` in the file tree, and
  `header.context` and `.context h1` in the headers.

Structural selectors encode layout rather than meaning:
`nav li:has-text('child parent') + li` and `+ li + li` (`session.spec.ts:1000,
1027`), `nav li:has-text('Tree first') + li .title` (`:1100`),
`.transcript > :nth-last-child(-n+3)` (`:563`), `.log ~ [role=alert]` (`:275,
875`), and `nav [role=alert]` (`:1171`).

State is read from classes: `.prompt.queued` stands for "pending" in eight
tests, and `toHaveClass(/archived/)` and `toHaveClass(/child/)` stand for
archived and child rows (`:1002, 1069–1071, 1086, 1188, 1193, 1242, 1268`).

Some role-based locators already work without app changes, and the tests do
not use them:

- The session heading is `getByRole("heading", { level: 1 })`.
- The approval card sits in `<section aria-label="Waiting on you">`, which is
  `getByRole("region", { name: "Waiting on you" })`.
- Status dots and tool markers are `role="img"` with an `aria-label`, so they
  are `getByRole("img", { name: "background" })`, not `.status` plus an
  attribute read.
- The archived marker is `getByRole("img", { name: "archived" })`, which
  replaces every `toHaveClass(/archived/)`.
- Errors are `getByRole("alert")` scoped by landmark: `getByRole("main")` or
  `getByRole("navigation")`.
- "Interrupted" and "This session is archived." are `getByText`.

### Replacement

Every locator goes through role and accessible name, label, or text, scoped by
landmark. When the page lacks a role or name, the locator waits on F2. A
pending prompt becomes an element inside a named pending group (F2), not a
class. A test that asserts a class as a proxy for state asserts the accessible
state instead.

## F2. The page lacks the roles and names role-based locators need

High · Plan

These gaps in the app force the CSS selectors in F1. The rewrite fixes them in
the app.

- **Transcript entries.** `.log` and `.transcript` are bare `div`s. Prompts,
  replies, messages, and errors are unnamed `div`s (`SessionView.tsx:106–143`).
  Give the transcript `role="log"` with a name, and make each entry a
  `listitem` or `article` named by its kind ("Prompt", "Reply", "Message from
  <label>"). Put pending inputs in a labeled group at the transcript's end, so
  "pending" becomes containment rather than a class.
- **Tool calls.** `<details class="tool">` has no name. Its input and result
  are anonymous `<pre>`s (`SessionView.tsx:152–166`). Name the group by its
  tool, and label the input and result.
- **Settings controls.** The permission mode `<select>` has only
  `name="permission_mode"` and no label (`SettingsFields.tsx:198`). The model
  picker's name is its changing content (`SettingsFields.tsx:66`). Give both
  labels: `getByRole("combobox", { name: "Permission mode" })` and
  `getByRole("button", { name: "Model" })`.
- **Session list.** Rows are a flat `<ul>` with depth in a CSS variable
  (`Sidebar.tsx:90–99`), so order and nesting cannot be expressed through
  roles. The indentation test (`session.spec.ts:1029–1031`) measures pixels for
  this reason. Nest each child's list inside its parent's item, or use
  `role="tree"` with `treeitem` and `aria-level`. Name the `<nav>`
  ("Sessions").
- **Workspace headers.** Each workspace is a `<p title={path}>`
  (`Sidebar.tsx:300`), found by `getByTitle`, the lowest-priority locator. Make
  it a heading or a named group.
- **Disabled menu reasons.** The reason is a `<span>` inside the menu item, so
  it joins the accessible name. Tests assert names like `"Delete Delete its
  child sessions first"` (`session.spec.ts:1006, 1040, 1074–1076, 1151, 1160,
  1199`, `workspace.spec.ts:145, 152`). Expose the reason as the item's
  description (`aria-describedby`) and assert it with
  `toHaveAccessibleDescription`.
- **Dialogs.** The add-workspace `<dialog>` has no name, and its path field
  has only a placeholder (`Sidebar.tsx:34–37`), so tests use
  `getByPlaceholder`. The confirmation `alertdialog`s have an `<h2>` that is
  not wired as their name. Label the field ("Directory") and name each dialog
  by its heading.
- **File tree.** Folder and file rows are buttons, which works. Notes such as
  "Empty", "permission denied", and relay errors are `<p class="row note">`, and
  the tree is a flat sequence of `.row`s (`FileTree.tsx:94–138`). Expected
  listings are therefore written as `.row` text arrays. Use nested lists or
  `role="tree"`, and make the panel's workspace name a heading.

## F3. Tests assert styling and pixel arithmetic

High · Plan

These assertions check presentation, not behavior. They break on restyling and
pass when the behavior a user relies on is broken:

- `toHaveCSS("list-style-type", …)` for reply lists (`session.spec.ts:106,
  108`).
- The tree guide test reads `getComputedStyle(row, "::before")` for guide
  offsets and border radius (`session.spec.ts:1090–1139`).
- The grandchild's title x-offset exceeds the child's (`:1029–1031`).
- Workspace headers are exactly 28 px high (`:1261–1275`).
- Toggle colors are compared through `getComputedStyle(...).color`, and the
  body cursor is `col-resize` during a drag (`workspace.spec.ts:367–368, 378,
  387, 411–412, 422, 440`).
- `alone()` and `even()` compare transcript, panel, and viewer widths
  (`workspace.spec.ts:375–377`). The default widths of 288 px after reload are
  checked at `:435, 499–500`.
- The file viewer header height equals the session title's, and the viewer
  width equals the transcript's (`workspace.spec.ts:697–700`).
- `scrollWidth > clientWidth` stands for "no wrapping" (`:701`).
- Syntax colors are checked with `toHaveCSS("color", "rgb(…)")` and
  `toHaveCSS("font-style", "italic")` (`:747, 749, 769, 772`). The span count
  stands for "highlighted or plain" (`:733, 736`).
- The menu's bottom edge is at the viewport edge within one pixel
  (`session.spec.ts:1211–1233`).

Geometry that is the behavior stays:

- Drags resize panels within their bounds (`workspace.spec.ts:436–460`), and
  a session's or draft's dragged split comes back.
- Scroll position follows output (`session.spec.ts:301–354`).
- Scroll position is kept across a re-read (`workspace.spec.ts:703–711`).

The menu that stays on screen is user-visible. It becomes
`expect(menu).toBeInViewport({ ratio: 1 })`, not edge arithmetic.

### Replacement

Delete the styling assertions. Whether a toggle is "dimmed" is already its
`aria-pressed`. Whether a row is archived is already its archived image. For
**Syntax highlighting**, check the language and theme chosen for a file name in
a `node:test` test of `highlight.ts`, as `stack.test.mjs` does for the stack,
and drop the color checks.

## F4. Fixed sleeps and DOM-marker probes stand in for awaited events

Medium · Plan

- `waitForTimeout(500)` before asserting no `session/create` was sent
  (`session.spec.ts:181`).
- `waitForTimeout(100)` for "the first menu reports its close after the second
  opens" (`:1208`).
- `quiet()` sleeps 2500 ms per call, five times in the file tree test, plus two
  100 ms sleeps (`workspace.spec.ts:205–209, 243–244, 269, 316–317, 324`). It
  asserts that polling is paused, and it adds over 12 s to `make e2e`.
- `waitForTimeout(2500)` and two `waitForTimeout(1500)` in the file viewer
  test (`workspace.spec.ts:710, 721, 754`).
- `dataset.marked` written on the file pane and on a highlight span, and then
  checked to show no re-render happened (`workspace.spec.ts:717–722, 751–755`).
  This probes Solid's rendering, not anything a user sees.
- `cutReply` and `armCutReply` monkeypatch `EventSource` and signal the test
  through `document.body.dataset.cutReply`. A second patch sets
  `dataset.existing` from a protocol field (`session.spec.ts:795–815,
  842–853`). Withholding a reply is a legitimate injected fault. Passing state
  through `body` attributes, and asserting on a protocol field the user never
  sees, are not.
- The background-status test reads `status.getAttribute(...)` once to prove
  every earlier check ran within the fake's 2000 ms task (`:391–392`). On a
  slow run the task ends first and the test fails for timing alone.
- The IME test dispatches a synthetic `KeyboardEvent` and asserts its
  `dispatchEvent` return value, which is whether the handler called
  `preventDefault` (`:289–294`). The "nothing sent" check that follows is
  immediate, so it cannot see an asynchronous send.
- `stack.test.mjs` sleeps 200 ms after the stack has already resolved
  (`:153`). It sleeps 500 ms to show no second rebuild (`:204`). It sleeps
  300 ms to land a stop inside a 0.5 s TERM trap, then 1000 ms to show nothing
  restarted (`:216–218`).

### Replacement

- Poll timing goes under Playwright's clock: `page.clock.install()`, then
  `page.clock.runFor(...)` past the poll interval, then assert that no request
  was made. A real-time sleep is not needed.
- "Unchanged after a re-read" waits for the next `workspace/read_file`
  response with `page.waitForResponse`, then asserts what the user sees, such
  as scroll position or selection.
- Delete the re-render probes.
- The cut-reply injection signals the test with `page.exposeBinding` and
  asserts only on the page: the alert, the transcript, and the composer.
- The background test needs a task that outlasts its checks. Either it is
  held until the test releases it, or it runs long and the test archives or
  stops it.
- For the IME, drive composition through CDP `Input.imeSetComposition` and
  press Enter. Assert that the composer keeps its text after an awaited sync
  point.
- In `stack.test.mjs`, a trap writes a marker file the test waits on, and
  "nothing restarted" is checked after `await stack` resolves, when no start
  can follow.

## F5. One daemon is shared by every test in a file

Medium · Plan

`session.spec.ts` and `approval.spec.ts` start one oxd and relay in
`beforeAll` (`session.spec.ts:24–30`), and all 36 session tests run against it.
The effects:

- Sessions pile up across tests, so titles must be unique across the file.
  `sessionRow` filters by substring (`ui.ts:20`).
- Structural sibling selectors assume no other session sorts between parent
  and child.
- `open` branches on `count()` to skip adding a workspace a previous test
  already added (`ui.ts:8`).
- Tests that change the daemon's config restore it in `finally`
  (`session.spec.ts:755–761, 779–791`), and a failure inside one leaves the
  next test on whatever config it reached.
- "session list membership and ordering" starts its own stack, because the
  shared one cannot give it an empty list (`:887`).

`workspace.spec.ts` starts a stack inside each test with `try`/`finally`.
`playwright.config.ts` sets `workers: 1` and `fullyParallel: false`, so all
three files run serially.

### Replacement

Make a test-scoped `services` fixture through `test.extend`. Its config comes
from an option (`test.use({ config })`), and teardown runs automatically. Every
test gets a fresh daemon, database, and workspace, which removes `finally`
blocks, `count()` branches, and title-uniqueness constraints. Per-test stacks
already run on free ports in temporary directories, so the config can drop
`workers: 1` and run files in parallel. Add `trace: "retain-on-failure"`.

## F6. Long scenarios check many behaviors under one name

Medium · Plan

Several tests run a dozen behaviors in sequence. An early failure hides the
rest, and the name does not say what broke:

- "a child session shows beneath its parent" (`session.spec.ts:981–1088`) checks
  labels, order, delete blocked by children, child URL and path, the parent's
  message, reload, grandchild nesting, archive blocked while a descendant
  waits, child archive, parent archive, unarchive rules, show archived, and
  delete order.
- "an archived session is read-only" (`:1141–1259`) checks archive blocked
  while busy, the not-connected reason, archive failure, read-only composer,
  other pages, show archived across a panel toggle, unarchive not connected,
  menu replacement, menu placement, unsent text returning, a missed archive
  after reconnect, and reload.
- The file tree test (`workspace.spec.ts:187–351`) and the file viewer test
  (`:661–815`) each cover close to every requirement in their area.
- The panel layout test (`:353–506`) covers toggles, drags, per-session
  splits, draft carry-over, and reload.

The [Verdicts](#verdicts) table lists the split for each.

The file tree test also checks behavior no requirement names. "A listing in
flight holds back the next one for its folder" (`workspace.spec.ts:251–273`)
is request deduplication, an implementation property. Delete that part.

## F7. Helpers are hand-rolled where fixtures fit

Low · Plan

- `services.ts` is sound: it isolates directories, chooses free ports, and
  checks for race-detector exits. Wrap it in the F5 fixture.
- `open` in `ui.ts` branches on DOM state. With per-test isolation it always
  adds the workspace, and it becomes a page fixture (`ox`) that yields a page
  with the workspace added.
- `hold` in `ui.ts` is idiomatic request gating through `page.route`. Keep it.
- Several tests repeat a `page.on("request")` listener that parses
  `postDataJSON` to collect RPC calls by method (`session.spec.ts:171–176,
  264–269, 856–866`, `workspace.spec.ts:45–48, 200–204`). Give `ui.ts` one
  `rpcCalls(page, method)` helper.
- `sessionButton`, `sessionRow`, and `toolResult` become role-based once F2
  lands.
- `newSession` and `ask` in `session.spec.ts` wait on `.reply` and `.approval`.
  They move to role locators and into `ui.ts`, because `workspace.spec.ts`
  repeats the same loop inline four times.
- `callTool` (`session.spec.ts:514–535`) speaks MCP to oxd's socket to act as
  another session's Claude. This is the right boundary for messaging tests.
  Move it into `services.ts` beside the socket it uses.
- `global-setup.ts` builds once with the race detector. Keep it.

## F8. Non-retrying reads of page state

Low · Plan

Some assertions read the page once and compare the result, so they fail on
timing:

- `expect(await width(nav)).toBe(388)` after each drag
  (`workspace.spec.ts:435–478`).
- `expect(await panel.evaluate(pre => pre.textContent)).toBe(template)`
  (`:763`).
- `expect(await distance()).toBeGreaterThan(20)` (`session.spec.ts:344`).

Web-first forms exist for each:

- `toHaveJSProperty("textContent", template)` keeps carriage returns.
- `expect.poll` handles widths.
- The scroll-follow test becomes `toBeInViewport()` on the last reply, and
  `not.toBeInViewport()` while scrolled up, in place of
  `getBoundingClientRect` arithmetic.

## Verdicts

The cross-cutting migrations apply to every test without changing its verdict:
role locators (F1, F2), the `services` fixture (F5), and the `ox` page fixture
(F7). **Keep** means the behavior, structure, and assertions stand apart from
those migrations.

### session.spec.ts

| Test                                                                           | Verdict | Reason                                                                                                                                     |
| ------------------------------------------------------------------------------ | ------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| the first send hands its text and later edits to the new session               | Split   | Three behaviors: edits during create and send stay; text retyped equal to the sent prompt stays; the same during create.                   |
| a reply renders as Markdown and its prompt stays plain                         | Split   | Drop `toHaveCSS`; split rendering, unsafe content (HTML, images, `javascript:` links, `target`), and prompts as sent.                      |
| a failed first send is retried in the session with its settings                | Keep    | Failed creation keeps text and settings, and the user resubmits.                                                                           |
| a session created after the user moves on leaves the selection alone          | Keep    | Guards **Creation keeps the user's view**.                                                                                                 |
| a rejected creation keeps the draft and its settings after switching views    | Split   | Enter in the settings popover sends nothing is a separate test and uses a 500 ms sleep; the failed-creation part stands.                   |
| a pending configuration shows as pending and holds Send until it is confirmed | Split   | Pending holds Send; failure shows the error and actual settings; another page's change during pending. Drop the loader and chevron icon checks. |
| a reconnect keeps typed text and sends nothing again                           | Keep    | Guards **Unsent text** and **Only the user sends**.                                                                                        |
| a send cut off after acceptance shows an error without resending               | Rewrite | Replace the `body.dataset` signal with `exposeBinding`; replace `.log ~ [role=alert]`.                                                     |
| Shift+Enter inserts a newline and Enter during composition doesn't send        | Split   | Shift+Enter stands; the IME half asserts a `dispatchEvent` return value, and its no-send check cannot see an async send.                    |
| overflowing output follows only while the reader is at the bottom              | Split   | Follows at the bottom; composer resize keeps following; scrolled up stays; scrolling down resumes. Use `toBeInViewport`.                   |
| an oversized prompt fails visibly and stays in the box                         | Keep    | Guards **Prompt size limit**.                                                                                                              |
| Stop interrupts a running reply                                                | Merge   | Its assertions are the tail of "Stop appears once the page's send is accepted".                                                            |
| a session in the background shows its status and stays configurable and archivable | Rewrite | Passes only if all checks finish within the fake's 2 s task; one-shot `getAttribute` read.                                                 |
| Stop appears once the page's send is accepted, and stops it                    | Keep    | Guards **Stop**; absorbs the merged test.                                                                                                  |
| a send held in one session leaves another usable                               | Keep    | A user would call a blocked second session a bug.                                                                                          |
| Allow sends the exact numbers displayed in the tool input                      | Keep    | Guards **Allow and Deny** precisely.                                                                                                       |
| a prompt sent while Claude works stays pending until Claude takes it          | Keep    | Guards **One prompt at a time**; pending becomes a group, not `.queued`.                                                                   |
| Stop drops a pending prompt and returns its text to the composer               | Keep    | Guards **Stop**.                                                                                                                           |
| a dropped prompt returns only to the page that sent it                         | Keep    | Guards **Stop** across pages.                                                                                                              |
| all waiting inputs show in order with their send-time labels                   | Split   | Pending order; labels survive deleting the sender; taken messages keep labels after reload. Replace the `:nth-last-child` selector.        |
| Stop drops a pending message without touching the composer                     | Keep    | Guards **Stop** for messages.                                                                                                              |
| a reconnect reconciles a prompt taken / dropped while the page was away (×2)  | Keep    | One behavior, parameterized; idiomatic.                                                                                                    |
| AskUserQuestion is answered from a form                                        | Split   | Submit gating; Other toggling; submitted answers; Decline; malformed input falls back to Allow and Deny.                                   |
| a tool whose process ended with oxd shows stopped on every page                | Keep    | Guards **Tool calls** and **One transcript**.                                                                                              |
| a reconnect keeps an expanded tool call open                                   | Keep    | Layout through reconnect; assert the result is visible, not the `open` attribute.                                                          |
| a session whose harness is no longer offered shows its settings                | Keep    | Guards **Retired settings**; its `finally` restore goes away with F5.                                                                      |
| reselecting a model repairs a retired effort when there is only one choice     | Keep    | Guards **Retired settings** and **Effort across models**; same F5 note.                                                                    |
| a lost creation reply is recovered after leaving the draft                     | Rewrite | `body.dataset` signal and `nav button.open` counts; see F4.                                                                                |
| a retried creation recovers the session without sending                        | Rewrite | Two `EventSource` patches; asserts the protocol's `existing` field through `body.dataset`.                                                 |
| session list membership and ordering don't wait for transcripts                | Split   | Hash-change selection is its own test of **Session URL**; it hand-rolls its own stack.                                                     |
| a cancelled deletion's reply cannot close the next confirmation                | Keep    | Guards **Every request settles**.                                                                                                          |
| on reconnect each session waits for its own settings                           | Keep    | Guards **Lost connection**.                                                                                                                |
| a child session shows beneath its parent with its title                        | Split   | About ten behaviors (F6); drop the x-offset check and `toHaveClass`; order through nested roles.                                           |
| a child's guide starts at its parent's dot or where its previous sibling branches | Delete  | Asserts `::before` offsets and radii; pure styling.                                                                                        |
| an archived session is read-only and hidden until shown                        | Split   | About twelve behaviors (F6); drop the 100 ms sleep; menu placement becomes `toBeInViewport`.                                               |
| workspace headers keep their height when archived sessions overflow the sidebar | Delete  | Asserts a 28 px header height; pure styling.                                                                                               |

### workspace.spec.ts

| Test                                                                     | Verdict | Reason                                                                                                                                    |
| ------------------------------------------------------------------------ | ------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| a malformed session link still lists workspaces                          | Keep    | Uses `.locator(".name")`, which F2 replaces.                                                                                              |
| a draft holds Send until the session list loads                          | Keep    | Guards **Lost connection** for drafts.                                                                                                    |
| a draft keeps its permission mode through a relay restart                | Keep    | Guards **Unsent text** and **Only the user sends**.                                                                                       |
| a model without efforts creates a session without effort                 | Keep    | Request check through `waitForRequest`; idiomatic.                                                                                        |
| a cancelled add or remove cannot settle the next dialog                  | Split   | Add and remove are separate behaviors.                                                                                                    |
| a workspace is removed once it has no sessions                           | Split   | Disabled reasons including archived; confirm text and failure in the dialog; removal discards the draft.                                  |
| the file tree lists the workspace and keeps each session's expanded folders | Split   | Listing and hidden names; per-session expansion; updates; paused while hidden; relay error; unreachable keeps the listing; lost connection; unreadable and empty folders; another workspace's tree. Replace `quiet()` sleeps with the clock; delete the in-flight dedupe part. |
| panel toggles and drags change the layout, which lasts until reload      | Split   | Toggles and their place; drag bounds; per-session split; draft split carries; reload resets. Drop colors, cursor, `alone`, `even`, and 288 px. |
| the file viewer shows each session's open file                           | Split   | Open, highlight, replace; scroll kept across re-read; updates and deletion; read errors; per-session and draft open file; reload. Drop header height, widths, `scrollWidth`, CSS colors, and `dataset.marked`; move language choice to a unit test. |

### approval.spec.ts (live)

| Test                                    | Verdict | Reason                                                                                    |
| --------------------------------------- | ------- | ----------------------------------------------------------------------------------------- |
| an approval is answered from its card   | Split   | Allow and denial with a typed reply are two behaviors; each needs one tool call anyway.   |
| an ExitPlanMode plan renders as Markdown | Keep    | Guards the plan half of **Tool calls** against real Claude input.                         |
| an AskUserQuestion is answered from its form | Keep    | Guards **Questions from Claude** against real Claude input; the fake test covers the form. |

### stack.test.mjs

| Test                                                 | Verdict | Reason                                                     |
| ---------------------------------------------------- | ------- | ---------------------------------------------------------- |
| a changed binary restarts alone                      | Keep    | Waits on log lines and PIDs.                               |
| an unchanged binary is not restarted                 | Keep    |                                                            |
| a failed build restarts nothing                      | Keep    |                                                            |
| a service with restart false is not restarted        | Keep    |                                                            |
| an exit the stack did not cause stops the stack      | Keep    |                                                            |
| an exit after a restart stops the stack              | Keep    |                                                            |
| a retiring service that fails stops the stack        | Rewrite | `sleep(200)` after the stack resolved proves nothing.      |
| one terminal Ctrl-C signals each service once        | Keep    |                                                            |
| SIGTERM to the stack signals every service           | Keep    |                                                            |
| stopProcess accepts only status 0 or the signal it sent | Keep    | Cases of one function.                                     |
| a second Enter during a rebuild does not start another | Rewrite | `sleep(500)` in place of waiting for the log line and stack exit. |
| stopping during a restart starts no new process      | Rewrite | `sleep(300)` must land inside a 0.5 s trap; `sleep(1000)` in place of `await stack`. |

Counts: keep 33, split 17, rewrite 7, merge 1, delete 2.

## Untested requirements

These requirements have no web test. Where a Go test covers the server half,
it is named, and the page's half is still untested.

- **Adding a workspace:** adding the same directory again changes nothing
  (`TestSameDirectoryIsOneWorkspace` covers the server).
- **Session labels:** a short identifier before the first prompt.
- **Session status:** parked, running, and waiting on the user. Only idle,
  background, and archived are checked.
- **New-session defaults** and **New-session choices:** no test picks a
  harness or model for a new session, or checks the default model and effort
  shown. The fake config offers one harness.
- **Blank prompts:** no test at any layer.
- **Tool calls:** the done and failed markers.
- **Failed turns** and **Unexpected exits:** the error in the transcript
  (`TestUnexpectedExitIsReported` covers the server).
- **Claude's own turns** (`TestTurnsClaudeStartsArePublished` covers the
  server).
- **Setting commands:** the error and kept composer
  (`TestClaudeRefusesSettingCommandsBeforeStarting` covers the server).
- **When settings can change:** settings unavailable while running.
- **Model within its harness:** an existing session's picker offers only its
  harness.
- **Claude's own mode changes:** the page showing the new mode
  (`TestClaudeModeChangeIsAdopted` covers the server).
- **Permission requests:** the card after a reload.
- **Approvals wait:** withdrawal on every page.
- **One answer per approval:** the error on a second answer
  (`TestApprovalAnsweredOnce` covers the server).
- **Lost conversation:** the notice and the text returning to the composer
  (`TestUnresumableConversationStartsAnew` covers the server).
- **Lost connection:** asking for a reload when `oxd` is still down.
- **Deleting a session:** removal from another page.
- **File tree:** hiding `.svn`, `.hg`, `.hypercube`, and `Thumbs.db`. Only
  `.git` is checked in the page, and `TestWorkspaceFiles` checks `.DS_Store`
  and `.git`.
- **File viewer:** files over 1 MiB and non-UTF-8 files in the pane
  (`TestWorkspaceFiles` covers the server).
- **Narrow screens** and **Touch screens** wait on the phone test being
  rewritten.
