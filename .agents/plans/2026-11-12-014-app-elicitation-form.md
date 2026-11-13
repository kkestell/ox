# The app shows an agent's form above the composer and sends the answers

- Task: `2026-11-12-014-app-elicitation-form`
- Kind: plan
- Status: ready
- Source HEAD: `82203839c1c7243c204a02f86aa0324dbe0bb515`
- Dependencies: `2026-11-12-012-elicitation-relay-in-oxd` (events, method,
  `awaiting_input`, the fake agent's `elicit`)

## Outcome and boundaries

While a session has an open elicitation, the panel that rises from behind the
composer shows the form: the message as its title, then each field with its
title, description and control. Choice fields list every option with its
description. Submit sends the answers, Decline declines, and the panel leaves
once the request resolves, including when the agent withdraws it. The session
list shows `Awaiting input`. Every form property type renders, because oxd
advertises form elicitation in full.

Non-goals: styling beyond what the layout needs and what makes elements match
their peers (the user designs the look), and URL elicitation.

## Current system

- `crates/ox/src/conversation.rs` folds events into `Item`s. Open permissions
  are tracked in `requests` and folded by `PermissionRequested` and
  `PermissionResolved` into `Item::Permission { key, request_id, title,
  options, answered }`. `crates/ox/src/lib.rs` has the `permit` command and
  `generate_handler!`.
- `ui/src/App.tsx` finds the oldest unanswered permission and passes it to
  `Composer`, which renders `Permission` above `.box` inside
  `<form className="composer">`, the form that wraps the box and `.settings`.
  `Conversation.tsx` renders nothing for a permission. `protocol.ts` holds the
  types, and `SessionList.tsx` `stateLabels` the state labels.
- `ui/src/styles.css`: `.permission` (the panel rising from behind the box),
  `.permission .options`, `.permission .title code`, `.text-button`,
  `.text-field`, `.status.awaiting_permission` (warning colour), and the
  small muted rule `.prompt .attachment, .tool-call .label, .session-closed`.
- E2E: `ui/e2e/specs/session.e2e.ts` ("asks for permission above the
  composer…") and `ui/e2e/helpers.ts` select `form.composer`.

## Design

**Backend fold (`crates/ox`).** Add `Item::Elicitation { key:
"elicitation:{id}", request_id, message, fields: Vec<Field>, answered }`, where
`Field { name, required, #[serde(flatten)] schema: ElicitationPropertySchema }`
lists the properties in the schema's key order. `requests` also holds open
elicitations, or a sibling map does, and `ElicitationResolved` re-emits the
item with `answered: true`. The new command `respond_elicitation(session_id,
request_id, action: ElicitationAction)` calls
`Client::respond_elicitation`.

**Shared panel.** The rising panel is one kind of element with two contents.
Rename `.permission` to `.request-panel`, `.permission .options` to
`.request-panel .actions`, and `.permission .title code` to
`.request-panel .title code`. `Permission.tsx` uses the new classes and
`Elicitation.tsx` uses the same ones. The panel gets `max-height:
var(--request-panel-max-height)` (a new token) and `overflow: auto`, so a long
form scrolls instead of pushing the composer off screen.

**Composer structure.** An elicitation needs its own `<form>` for native
validation, and forms cannot nest. The outer `.composer` becomes a `<div>`.
The `.box` becomes the `<form>` that owns the submit handler. The panel stays
in `.stack` above it. CSS selectors on `.composer`, `.box` and `.stack` keep
working.

**Which request shows.** `App.tsx` passes the oldest unanswered item whose
type is `permission` or `elicitation` as `request`. `Composer` renders
`Permission` or `Elicitation` for it, keyed by `request_id`. `Conversation`
returns `null` for `elicitation`, as it does for `permission`.
`awaiting_input` counts as running in `Composer` (it shows Stop), is labelled
`Awaiting input` in `stateLabels`, and shares the warning colour by joining the
`.status.awaiting_permission` selector.

**`ui/src/Elicitation.tsx`.**

- Layout: a `<form className="request-panel">` holds `<p className="title">`
  with the message, one block per field, and `.actions` with two
  `.text-button`s, `Decline` and `Submit`. Each field block is a
  `<fieldset>`, whose `<legend>` is the field's title (falling back to its
  name), followed by its description and its control.
- The title, option descriptions and other secondary text join the existing
  small muted rule, so the CSS states each value once.
- Controls by schema:
  - string with `oneOf` or `enum`: radio rows, each a `<label>` with the
    input, the option title, and its description below.
  - array: checkbox rows, built the same way from `anyOf` or `enum`.
  - plain string: `<input className="text-field">` whose `type` follows
    `format` (`email`, `url`, `date`, `datetime-local`; `date-time` values
    are sent as `toISOString()`), with `minLength`, `maxLength`, `pattern`
    and `default`.
  - number and integer: `type="number"` with `min`, `max`, and `step` of `1`
    for an integer or `any` for a number.
  - boolean: a checkbox `<label>` like `ConfigPicker`'s.
  - unknown type: omitted.
- `required` applies to the input, or to the first radio of a group. Defaults
  fill the initial state.
- Answers: text, number, choice and multi-select fields that are left empty
  are omitted. A boolean always sends `true` or `false`. Numbers send as
  numbers.
- `minItems` and `maxItems` are checked on submit through
  `setCustomValidity` and `reportValidity` on the group's first checkbox.
- Submit invokes `respond_elicitation` with `{action: "accept", content}`.
  Decline sends `{action: "decline"}`. Both disable the panel's controls
  while pending and re-enable them on error, as `Permission` does.

**Peers.** Run the AGENTS.md comparison before finishing. Radio and checkbox
rows match the `ConfigPicker` checkbox label. The text inputs and buttons use
`.text-field` and `.text-button` unchanged. The actions row matches the
permission panel's options row.

## Check your assumptions

1. **The composer's outer element changes from a form to a `div`.** The
   message box becomes the `<form>`, because the form panel needs its own form
   for the browser's required and pattern checks, and forms cannot nest. The
   e2e selectors `form.composer` become `.composer`. The alternative was
   validating the form by hand inside the composer's form.
2. **The `.permission` class is renamed `.request-panel`, and its `.options`
   becomes `.actions`.** Permission requests and forms then share one panel
   rule. A second, copied rule for the form panel is the alternative given up.
3. **Every form field type is built, not just the two AskUserQuestion uses.**
   That covers text with formats, number, integer, boolean, and single and
   multi choice, because oxd tells agents it supports forms. This is more UI
   than AskUserQuestion needs. The fake agent's form exercises all of it.
4. **The form has two buttons, Decline and Submit.** There is no Cancel
   button. Stop cancels the turn, and the agent then withdraws the form.
5. **Each field shows its title (Claude's header) as the small muted label.**
   The description (the question) follows in normal text, then the options,
   each with its description in muted text. A long form scrolls inside the
   panel, up to a new height token. The look is the user's to refine.
6. **Only one request shows at a time.** The oldest unanswered permission or
   form, whichever came first, appears in the panel.

## Implementation sequence

1. `crates/ox`: `Item::Elicitation`, the fold, the `respond_elicitation`
   command; unit test.
2. `protocol.ts`: the `elicitation` item, `Field`, `awaiting_input`.
3. `Composer.tsx` restructure, `App.tsx` request selection, `Conversation.tsx`,
   `SessionList.tsx`.
4. `Elicitation.tsx`; `Permission.tsx` and `styles.css` class rename, token,
   scrolling.
5. E2E specs and helpers.

## Verification

- `crates/ox/src/conversation.rs` unit test: `ElicitationRequested` yields an
  unanswered item with fields in key order, each field carrying its flattened
  schema. `ElicitationResolved` re-emits it answered. An unknown request ID
  yields nothing.
- `ui/e2e/specs/session.e2e.ts`, against the fake agent's `elicit`:
  - "asks the agent's questions above the composer":
    - The panel shows `Order`, the legend `Color`, `Pick a color`, the option
      `Blue` with `Cool`, and the sidebar's `Awaiting input`.
    - Submit with no colour leaves the panel up and sends nothing, because the
      colour is required.
    - Pick Blue, tick Nuts, type `hi` in Note, enter 2 in Count, and Submit.
      The agent text is
      `accepted {"color":"blue","count":2,"extras":["nuts"],"note":"hi","rush":false}`,
      and the panel and `Awaiting input` disappear.
  - "declines the agent's questions": the agent text is `declined`.
  - "withdraws the questions when the turn stops": Stop, then `withdrawn`,
    and the panel disappears.
  - The permission spec and helpers use `.composer` and `.request-panel`.
- Commands: `make check-rust` (crates/ox), `make check-ui`, `make e2e`.
- Manual: compare the panel with its peers (permission panel, text fields,
  buttons) for size, colour, spacing and alignment, as AGENTS.md requires.
