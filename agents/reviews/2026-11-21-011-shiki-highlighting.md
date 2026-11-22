# Review: highlight the file viewer with Shiki

## Scope and coverage

Uncommitted changes on base `44be010`, checked against
`agents/plans/2026-11-21-011-shiki-highlighting.md`: `cmd/ox/web/package.json`,
`package-lock.json`, `src/highlight.ts`, the highlighting rules in
`src/style.css`, and the test "the file viewer shows each session's open file"
in `e2e/workspace.spec.ts`. The other hunks in `style.css`,
`workspace.spec.ts`, and `docs/todo.md` belong to other work and were not
reviewed. Callers checked: `FileView.tsx`, `workspace/read_file` and its 1 MiB
limit in `internal/server/files.go`.

Gaps:

- `/ox-work`'s Tests proved and Checks lists were not passed on. The tests were
  proved here by mutation instead (see Checks run).

## Summary

| ID | Severity | Category    | Action   | Status | Title                                         |
| -- | -------- | ----------- | -------- | ------ | --------------------------------------------- |
| F1 | Low      | Correctness | Rejected | —      | Shiki blocks the page for seconds on 1 MB files |

## F1. Shiki blocks the page for seconds on 1 MB files

Low · Correctness · Rejected · `cmd/ox/web/src/highlight.ts:147`

`highlight` runs synchronously in `FileView`'s memo on the main thread, and runs
again on each changed read. Near `read_file`'s 1 MiB limit, Shiki takes several
times as long as highlight.js did, so the page freezes for about two seconds
each time such a file changes.

### Evidence

Node probe on `src/ox.tsx` repeated to 992,715 bytes: Shiki 2,186 ms as `.ts`
and 2,179 ms as `.tsx`; highlight.js 265 ms on the same text. An 88 KB
`package-lock.json` takes 45 ms. A single 800 KB JavaScript line takes 1,663 ms
in Shiki and 1,034 ms in highlight.js.

### Action

Rejected. The cost is linear and only reaches the user near the 1 MiB limit, a
size that hand-written source rarely reaches. Avoiding it needs either a worker,
which would make `highlight` asynchronous against the plan's decision, or a size
above which files show as plain text, which changes **Syntax highlighting**.
Either costs more than this case does.

## Checks run

- `make test`: passed.
- `make e2e`: 42 passed.
- Mutation, the font-style rule removed from `style.css`: the test fails at
  `workspace.spec.ts:581` (`font-style` `normal`, expected `italic`).
- Mutation, `.tsx` mapped to `typescript`: the test fails (`div` colored
  `rgb(149, 56, 0)`, expected `rgb(17, 99, 41)`).
- Mutation, the trailing `&#13;` of CRLF lines dropped: the `template.ts`
  `textContent` check fails.
- Node probe of `highlight`: Go, TS with CRLF and lone CR, empty and
  blank-line files, Markdown emphasis and strong, and HTML with `<script>` all
  produce only escaped text in `<span style>` elements, with line counts
  matching the source.
- `npm ls`: `shiki`, `@shikijs/langs`, `@shikijs/themes` at 4.6.0;
  `highlight.js` gone from the lockfile.
- `make live` and the headless Chrome check: not required; the change does not
  alter how Ox drives Claude Code or how the page reaches `oxd`.

## Verdict

Ready to commit. F1 is rejected; there are no open findings.
