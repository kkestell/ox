# Render transcript messages as Markdown

- Task: `2026-11-12-016-transcript-markdown`
- Status: complete
- Plan: `.agents/plans/2026-11-12-016-transcript-markdown.md`
- Review: `.agents/reviews/2026-11-12-016-transcript-markdown.md`

## Changes

Added shared CommonMark/GFM rendering to user messages, assistant messages and
thinking. The component owns external-link handling and table overflow; shared
CSS controls block spacing, code whitespace and wrapping. Tool data stays
literal. Registered Tauri's opener with scoped URL permission. Extended the
fake agent with decoded text/thought chunks and added six transcript E2E cases.

Changed paths:

- `ui/src/{Markdown,Conversation}.tsx`, `ui/src/styles.css`, UI dependencies
- `crates/ox/{Cargo.toml,src/lib.rs,capabilities/default.json}`, `Cargo.lock`
- `crates/oxd/src/bin/oxd-fake-agent.rs`
- `ui/e2e/helpers.ts`, `ui/e2e/specs/markdown.e2e.ts`

## Deviations

None. The opener E2E case intercepts only its IPC fetch to avoid launching
the user's browser. Production uses the official plugin and scoped capability.

## Verification

| Command | Result | Evidence |
| --- | --- | --- |
| `make check-ui` | Passed: Biome and both TypeScript configs | `/tmp/ox-016-check-ui.log` |
| `make check-rust` | Failed: Clippy rejects identical Text/Echo match arms | `/tmp/ox-016-check-rust.log` |
| `make check-rust` | Passed after reusing Echo: formatting, Clippy, 265 tests and doctests | `/tmp/ox-016-check-rust-repaired.log` |
| `make e2e` | Initial failures: invalid selector, missing anchor wait, attempted replacement of readonly Tauri invoke. Corrected test boundaries. | `/tmp/ox-016-e2e.log`, `/tmp/ox-016-e2e-final.log` |
| `make e2e` | Passed before final visual corrections: 46 tests | `/tmp/ox-016-e2e-repaired.log` |
| `python3 /tmp/ox-016-proof.py` | Expected regression failure without renderer; source restored | `/tmp/ox-016-proof-e2e.log` |
| `python3 /tmp/ox-016-table-proof.py` | Expected regression failure without normal table word wrapping; source restored | `/tmp/ox-016-table-proof-e2e.log` |
| `make e2e` | Full-height assertion exposed grid scrollbar sizing; proposed no-shrink rule did not fix it. Replaced grid with block flow and removed that rule. | `/tmp/ox-016-e2e-complete.log`, `/tmp/ox-016-height-diagnostic-2.log` |
| `npm run e2e -- --spec e2e/specs/markdown.e2e.ts --mochaOpts.grep 'keeps wide code'` | Passed with ordinary block flow, including full table height | `/tmp/ox-016-block-focused.log` |
| `make check-ui` | Passed on final sources | `/tmp/ox-016-check-ui-verified.log` |
| `git diff --check` | Passed | Builder session |
| `make e2e` | Passed on final sources: 46 tests, eight suites | `/tmp/ox-016-e2e-verified.log` |
| `make smoke-claude` | Passed with Haiku | `/tmp/ox-016-smoke.log`, `/private/tmp/ox-acp-isbhfyek/` |
| `make e2e` after URL repair | Passed: 46 tests, eight suites | `target/e2e-review/2026-11-12-transcript-markdown/after-fix-e2e.log` |
| `make check-ui` after URL repair | Passed | `target/e2e-review/2026-11-12-transcript-markdown/after-fix-check-ui.log` |
| Focused opener E2E without URL normalization | Failed as expected with mixed-case URL; source restored and rebuilt | `target/e2e-review/2026-11-12-transcript-markdown/url-regression-e2e.log` |
| Full E2E with temporary command capture | Before/after repair: 46 tests passed each. Final capture: 1,294 PNGs, all 46 starts/ends verified. | `target/e2e-review/2026-11-12-transcript-markdown/index.html` |

## Limitations

Accepted exclusions remain: literal tool data; compact titles and composer
forms unchanged; no file viewer, expanded remote-image permissions, HTML
execution, syntax highlighting, math or Mermaid. The opener test intercepts
native transport rather than launching an external application.

## Visual verification

Inspected user/assistant blocks, expanded thinking, wide code/tables and adjacent
tools. Corrected table word wrapping and grid-induced scrollbar clipping using
ordinary block flow; no stack-shrink workaround remains. Final screenshots:
`/var/folders/x3/s0dy9w311sncwf87160crpw80000gn/T/ox-e2e-i9NlrN/`.

## Repair round 1

- Finding addressed: round 1/P2. The native opener now receives the anchor's
  normalized URL. The existing opener E2E case covers lowercase and mixed-case
  schemes/hosts, asserting canonical IPC URLs.
- Changed paths: `ui/src/Markdown.tsx`, `ui/e2e/specs/markdown.e2e.ts`.
- Verification: coordinator checks and regression proof are recorded above.
