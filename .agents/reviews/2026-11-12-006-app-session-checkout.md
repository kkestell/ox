# Review: 2026-11-12-006-app-session-checkout

## Round 1

- Task: `2026-11-12-006-app-session-checkout`
- Author: xreview child `review-006b`, Claude Code (Hypercube child),
  claude-opus-5-5 / effort not exposed; coordinator ID `review-006b` (parent
  `2ea441ae`); native conversation ID `d42bf093-ae08-4e2a-9548-fccf149a21e1`
- Independence: I did not write this code; the implementer was `impl-006b`.
- Reviewed change: `/Users/kyle/projects/ox/.hypercube/2ea441ae`, branch
  `hypercube/session-2ea441ae`, uncommitted change against HEAD `633f1c3`
  (`git diff HEAD` plus untracked `ui/src/Dialog.tsx`,
  `ui/src/NewBranchDialog.tsx`, `ui/e2e/specs/worktree.e2e.ts`); plan
  `.agents/plans/2026-11-12-006-app-session-checkout.md` (revision 4, blob
  `1209438ff63ee43896f7f3cdc78a4a3b8d4d59e7`, matches the log); work log
  `.agents/work/2026-11-12-006-app-session-checkout.md`

### Verdict

accept: the change carries out the plan and the settled decisions (entry 3's
`NOT_FOUND` as deleted, the neutral kept-branch notice, Delete disabled in
flight), the required checks passed on the current code, and each behaviour
has a test shown to fail without it. No blocking findings.

### Justification of the difficult invariants

- **Delete outcome.** `deletion` (`crates/ox/src/lib.rs`) maps only `Remote`
  errors by code, so `UNSAVED_WORK` and `NOT_FOUND` cannot be confused with
  transport errors; the unit test covers all five arms. The UI derives the
  outcome only from the `delete_session` answer, never from `agent_exited`.
- **In-flight state.** `deleting` is a per-session set updated functionally,
  added before the `invoke` and removed in `finally`, so a failed or
  unsaved-work answer re-enables the button; the discard retry re-adds it.
- **Branch dialog.** The `open` flag drops both answers after unmount, and a
  `fetch_error` leaves the unfetched lists in place. `existing` covers a local
  name, a remote entry, and `<remote>/<Branch>`; Create sends `start: null`
  whenever Start From is disabled, so a value typed before the branch matched
  never reaches `oxd`.
- **Shared button class.** Moving `.permission .options button` to
  `.text-button` changed no values; the permission reject colour rule keeps
  higher specificity.

### Notes

- Not seen on screen: no screenshot of the menu or dialogs was taken. The
  group label `.menu-label` has the items' colour, size and padding without
  their hover, so "Claude" may read as an item; the dialog chrome and fields
  are WebKit defaults under the layout-only rule. The look is the user's
  call; worth a glance before merge.
- `ui/src/NewBranchDialog.tsx`: `branches` state carries a `fetch_error`
  that is never read beside the separate `fetchError` state; holding only the
  two lists would remove the duplicate.
- `ui/src/SessionList.tsx`: `unsaved` and `keptBranch` hold one dialog each,
  so two deletes answering close together show only the later answer's
  dialog. The other session stays listed (unsaved) or is deleted (kept), so
  nothing is lost.
- Cancelling the branch dialog while Create is in flight still selects the
  session when it answers. Reasonable; untested.
- Remote names containing `/` are not matched by `existing` (log names it).

### Verification

- Reused: `make check`: passed, `/tmp/ox-006-check.log` (12:20:39, after the
  last code edit at 12:19:26).
- Reused: `make e2e`: 5 spec files passed, 28 tests,
  `/tmp/ox-006-e2e-final.log` (12:21:25).
- Reused: `make smoke-claude`: passed through `delete_and_idle_exit`,
  `/tmp/ox-006-smoke.log` (12:22:16).
- Reused: the nine undo checks in the work log (`/tmp/ox-006-undo-1b.log`,
  `/tmp/ox-006-undo-2.log`, and the `cargo test -p ox deletion` result in the
  implementer's conversation). I read the assertions in
  `ui/e2e/specs/worktree.e2e.ts` and the unit test; they check the behaviours
  the plan's Verification section lists.
- Not run: no fresh checks; the evidence postdates every code file and no
  concrete doubt needed one. No screenshots (see Notes).
