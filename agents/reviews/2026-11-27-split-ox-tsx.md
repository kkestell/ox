# Review: split ox.tsx into snapshot, attempt, sessionControl, and ox

## Scope and coverage

Uncommitted changes in the `split-ox-tsx` worktree against base `566e832`, checked against `agents/plans/2026-11-27-split-ox-tsx.md` (issue #201).

Gaps:

- `make e2e` was not run here; the implementing session ran it.
- `make test` and `make live` were not run; the change touches only the web client's modules.

## Summary

No findings.

## Checks run

- Line-set comparison of the original `ox.tsx` against `snapshot.ts`, `attempt.ts`, `sessionControl.ts`, and the new `ox.tsx`. Every original line appears in a new file except the plan's stated changes: the `export` keywords and import blocks, the `snapshot`/`state` accessors replacing `session()` (`live`, `canStop`, `canConfigure`, `recoverable`), `snapshotOf` extracted from `load`, `attach()` calling `refuse`, the `fail` setter in `createAttempt`, and `post()` settling once on each path (success before `reconcilePrompt`, failure after `restore`, same order as before).
- Import chain: `snapshot.ts` and `attempt.ts` import nothing local except `protocol`; `sessionControl.ts` does not import `ox.tsx`; no cycle.
- All consumer imports in `App`, `ApprovalRequest`, `Attachments`, `FileTree`, `SessionView`, `Sidebar`, `TabArea`, `TabPicker`, and `Workspace` point at the owning module. `poll.ts` imports nothing from `./ox`.
- `npm run lint` (Biome and `tsc -p e2e`): no errors. Its 19 warnings are CSS specificity notes in unchanged styles.
- `docs/functional-requirements.md` is untouched, as the plan says.

## Verdict

Ready to commit.
