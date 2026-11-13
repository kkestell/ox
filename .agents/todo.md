# To do

## Local merge workflow

Spec: [git-workflows](specs/git-workflows.md). Two `/kplan` → `/kwork` →
`/kreview` cycles. Sizes are rough estimates of lines changed, tests included.

- [x] **Daemon and CLI** (~1,800). Tested through `oxctl`.
  - Move a branch: fast-forward or forced, the expected-commit check,
    updating a checkout that has the branch, refusing unsaved work or a
    running agent.
  - Compare: a comparison ref on worktree sessions, published with the
    session, recomputed on turn end, on moves, deletes and fetches in the
    repository, and on a refresh request.
- [x] **App** (~1,000, about 260 of them deletions).
  - The New Task menu: New Task starts a local merge session; Session in
    Checkout stays.
  - The right-aligned status icon on session rows.
  - The Merge into main button in the conversation header.
  - Removes the existing-branch and new-branch dialogs and `useBranches.ts`.

## Future work

- The pull request and review workflows, as the spec describes them.
- Workspace settings and the per-workspace default workflow, which matter
  once there is a second workflow to choose.
