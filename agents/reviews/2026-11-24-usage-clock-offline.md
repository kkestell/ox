# Keep the page's clock running while disconnected

## Scope and coverage

Reviewed the uncommitted changes on `usage-clock-offline` against base
`566ec77ee53c8350c1d7b66eb67f0c97e58e8794`: `cmd/ox/web/src/ox.tsx`,
`cmd/ox/web/src/poll.ts`, `cmd/ox/web/src/Usage.tsx`,
`cmd/ox/web/e2e/session.spec.ts`, and `docs/architecture.md`, checked against
[the plan](../plans/2026-11-24-usage-clock-offline.md) and traced through the
clock's consumers (`createPoll` via `FileTree.tsx` and `FileView.tsx`, and
`WindowRow` in `Usage.tsx`) and `ready` in `ox.tsx`.

The change delivers the plan's Goal and keeps its Decisions. `clock` carries
`Date.now()` and advances while the page is visible, regardless of the
connection. `createPoll` is the one place that decides whether a request may go
out, reading `ready` untracked. `WindowRow` derives time left from `clock` and
`resets_at` with no impure memo. The plan makes no requirement changes, and
`docs/functional-requirements.md` is untouched. **Subscription usage** now holds
while reconnecting. **Lost connection** is unchanged: polls still send nothing
until `reload` sets `live`. The `docs/architecture.md` row for `poll.ts` matches
the plan's text, and the File tree passage ("once per second while visible and
connected") still describes the code.

The change resolves [#86](https://github.com/kkestell/ox/issues/86). No other
open `bug` issue touches this code.

Gaps:

- `/ox-work`'s final response, with its Tests proved and Checks, was not passed
  on. The checks below were run here instead.

## Summary

No findings.

## Checks run

- `make test`: passed.
- `make e2e`: 175 passed, 4 skipped.
- With the clock gated on `ready()` again in `ox.tsx`, the new
  `a usage reading keeps counting down while the page is disconnected` fails at
  `toContainText("Waiting")`; the other usage specs pass.
- With the `ready` check removed from `createPoll`,
  `a lost connection keeps the tree and lists nothing until the page has reloaded`
  fails; the hidden-page and other lost-connection specs pass.
- Both edits were reverted, and the tree matches the reviewed diff.
- `make live`: not required; the change does not alter how Ox drives Claude Code.
- `make dev` check in headless Chrome: not required; the transport to `oxd` is
  unchanged, and `make e2e` observes when polls are sent.

## Verdict

Ready to commit.
