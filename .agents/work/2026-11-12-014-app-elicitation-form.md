# Work: the app shows an agent's form above the composer

- Task: `2026-11-12-014-app-elicitation-form`
- Plan: `.agents/plans/2026-11-12-014-app-elicitation-form.md`
- Status: complete

## Files changed

- `crates/ox/src/conversation.rs`: `Item::Elicitation`, `Field`, `Request` as a
  permission-or-form enum with one `item()` builder; unit test
  `an_elicitation_opens_with_its_fields_and_closes_by_request_id`.
- `crates/ox/src/lib.rs`: `respond_elicitation` command.
- `ui/src/Elicitation.tsx` (new), `Composer.tsx` (outer `div`, `.box` is the
  form, renders `Permission` or `Elicitation`), `App.tsx`, `Conversation.tsx`,
  `SessionList.tsx`, `Permission.tsx`, `protocol.ts`, `styles.css`
  (`.request-panel`, `.actions`, `--request-panel-max-height`, fieldset and
  `.choice` layout, `.status.awaiting_input`).
- `ui/e2e/helpers.ts`, `ui/e2e/specs/session.e2e.ts`: `.composer` selectors,
  three elicitation specs.

## Deviations

- Fields come in alphabetical order, not the agent's order. The library's
  `ElicitationSchema.properties` is a `BTreeMap`, so the agent's order is gone
  before oxd sees the request.
- A boolean field shows as one checkbox row, with its title as the label and
  its description muted underneath like an option's. It has no legend, so the
  title appears once.
- `minItems`/`maxItems` (and `required` on a multi-select, meaning at least
  one) are kept in sync on the group's first checkbox with `setCustomValidity`
  whenever the values change. The browser's own submit validation then reports
  them, instead of a manual check on submit.
- Format `uri` maps to `type="url"`; the protocol's value is `uri`.

## Checks

- Undo check: making `ElicitationRequested` fold to nothing fails the new unit
  test.
- `make check`: the first run stopped at the known
  `a_git_directory_git_rejects_is_refused_with_gits_reason`. On a rerun,
  `cargo test --workspace --no-fail-fast` had no failures, and `check-ui` and
  `check-scripts` passed. Someone other than this task modified
  `crates/oxd/tests/worktrees.rs` and `crates/claude-code-acp/src/runner.rs`
  during the run; they were left alone.
- `make e2e`: not run. The app build failed with "No space left on device"
  (330 MiB free; `target/debug` holds 22 GB). Caches were not deleted because
  a `make dev` instance may use them. The new e2e specs have never run.

## Shortcuts

- The e2e specs are unverified (above).
- The AGENTS.md peer comparison was done by reading the CSS, not by viewing
  the running app. Choice rows reuse the browser's default checkbox margins,
  as the `ConfigPicker` label does. Text fields and buttons use `.text-field`
  and `.text-button` unchanged.

## Coordinator e2e run

- `make e2e` (run by the coordinator after round 1): passed, 6 spec files, 34
  tests.

## Repair round 1

Findings from `.agents/reviews/2026-11-12-014-app-elicitation-form.md` and the
coordinator's screenshots.

- Bottom spacing: `.request-panel` keeps its padding (the `--radius-2xl` tuck)
  and max height. Its title and fields sit in a `.scroll` region inside that
  padding, above `.actions`, so the actions stay visible at the same height
  as the permission panel's. `Permission` uses the same structure. The scroll
  region's inset is `--ring-width`, so focus rings are not clipped.
- Choice rows: one shared `.choice` rule (a grid, input `margin: 0`,
  `--choice-gap`, centre alignment, description in the label's column). The
  panel's radio and checkbox rows and the `ConfigPicker` checkbox label use
  it. Screenshots show the legend, the controls and `.text-field` on one left
  edge.
- `pattern`: checked with `new RegExp(pattern, "u")` and `setCustomValidity`,
  unanchored. A pattern that does not compile is not checked.
- A choice field whose items are of an unknown type, or that has no options,
  is not rendered.
- An optional multi-select left empty skips its `minItems`. A selection keeps
  the options' order.
- Fake agent form: added `size` (plain enum), `sauces` (plain enum array,
  `minItems` 2), `extras` `maxItems` 2 with a third option, `code` (pattern
  `\d{3}`), `email` (format email), `when` (date-time with a default), and
  `weight` (number 0.5–10).
- E2E: "asks the agent's questions" now checks that the date-time default
  goes back as the same instant. The new spec "checks the agent's constraints
  before sending the answers" asserts `:invalid` for a number below its
  minimum, a bad email, an unmatched pattern, too many extras and too few
  sauces. It then sends a valid set that includes `x123y` for `\d{3}`.
- Undo check: with the HTML `pattern` attribute, the new spec fails at
  `input[aria-label="Code"]:invalid` not to be existing.

Checks: `make check` passed. `make e2e` passed (6 spec files, 35 tests).
`make smoke-claude` passed (the fake agent is in `crates/oxd`).

## Limitations

- Once an optional single choice is picked, it can't be cleared.
- A required field of a type the app can't render is omitted, so Submit
  sends content without it and the agent rejects it.
- The date-time round trip runs in the test machine's timezone. In UTC it
  can't tell a correct local conversion from no conversion.
- Formats `uri` and `date`, and `minLength`/`maxLength`, have no e2e
  assertion.
