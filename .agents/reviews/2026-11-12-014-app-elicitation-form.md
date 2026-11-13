# Review: 2026-11-12-014-app-elicitation-form

## Round 1

- Task: `2026-11-12-014-app-elicitation-form` (covers units 013 and 014 and commit ff94ce8)
- Independence: I did not write this code; the implementers were the 013 and 014 xwork builders.
- Reviewed change: `/Users/kyle/src/ox`, branch `main`, `git diff 68ac8f1` plus `ui/src/Elicitation.tsx`; plans `.agents/plans/2026-11-12-013-ask-user-question-bridge.md` and `.agents/plans/2026-11-12-014-app-elicitation-form.md`; work logs `.agents/work/2026-11-12-013-ask-user-question-bridge.md` and `.agents/work/2026-11-12-014-app-elicitation-form.md`

### Verdict

changes-required: the bridge, the drain fix and the app's fold, command and form logic carry out the plans. The form's radio and checkbox rows use the browser's default input margins, so they misalign with each other and with the field's text. The required `make e2e` result is not recorded.

### Findings

1. **P2: choice rows rely on UA input margins, so radio and checkbox rows do not line up.** `ui/src/styles.css:722` (`.request-panel .choice`), `ui/src/Elicitation.tsx` `Choice`. No rule sets the input's margin or the gap between the input and its text. WebKit's defaults are `3px 3px 0 5px` for a radio and `3px 3px 3px 4px` for a checkbox. A form with a single-select question above a multi-select one, which is AskUserQuestion's common case and the fake agent's `color`/`extras`, puts radio labels 1px right of checkbox labels. Both inputs sit 4–5px inside the left edge shared by the legend, the description and `.text-field`. AGENTS.md requires elements that nearly line up to share an edge, and each shared value to be stated once. Fix: one `.choice` rule for the row (input `margin: 0`, a gap token), used by the request panel rows and the `ConfigPicker` checkbox label, which is the peer the plan names.
2. **P2: required check missing.** The work log records `make e2e` as not run (disk full). The three new specs and the `.composer` selector change have never passed. Record the coordinator's passing `make e2e` run in the work log.

### Notes

- `crates/claude-code-acp/src/runner.rs:473`: the old doc line "Translates a line of claude's output." now sits above `translate_lines` with that function's own line, and `translate` has none.
- `crates/claude-code-acp/README.md` cancellation paragraph: one line runs past the 80-column wrap.
- Plan assumption 3 says the fake agent's form exercises every type. It covers no `number` field, `format` (including the `date-time` round trip through `localDateTime`/`toISOString`), `pattern`, plain `enum`, or `minItems`/`maxItems`. Those paths are untested.
- `pattern` goes to the HTML attribute, which anchors the pattern and compiles it with the `v` flag. JSON Schema patterns are unanchored, so the browser rejects `xabcx` for `abc`.
- An optional single choice cannot be cleared once picked.
- An array whose `items` is an unknown type renders a legend with no controls.

### Verification

- Reused: 013 `make check-rust`, `make check-scripts`, `make smoke-claude` (live AskUserQuestion answered as a form) passed, from the 013 work log and coordinator.
- Reused: 014 `make check` passed on rerun, from the 014 work log.
- Reused: ff94ce8 30/30 runs of `streams_and_resumes_a_persisted_session`, drain unit test failing with the old drain, from coordinator.
- Not run: `make e2e` (coordinator holds the in-progress result). No check was run by this review. The UI was judged from code and CSS, not the rendered app.
