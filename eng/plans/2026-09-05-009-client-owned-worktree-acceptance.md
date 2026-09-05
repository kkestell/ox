# Accept Client-Owned Git Worktrees

## Sources

- `docs/spec.md#isolation-memory-and-delegated-work` — owns client/user worktree
  lifecycle, session-root isolation, preservation, and host-privilege limits
- `eng/roadmap.md#client-owned-worktree-acceptance` — owns the real-Git process
  cases and user-guide gate
- `eng/architecture.md#extension-boundaries` and
  `eng/architecture.md#workspace-boundary` — keep worktrees as fixed activation
  inputs and filesystem confinement separate from process isolation
- `internal/e2e/{harness,session,filesystem,terminal,instructions}_test.go` —
  real process setup, session lifecycle, file-tool, spill, and instruction
  patterns
- `internal/agent/{agent,store,memory}.go` — current close/delete ownership,
  session spill cleanup, and canonical-root memory identity
- `~/src/references/repos/personal/iota/crates/adapter-git/src/lib.rs` and
  `~/src/references/repos/personal/iota/crates/bin-web/src/lifecycle/` — dirty
  worktree, branch, and teardown scenarios to reuse as tests without porting
  Iota's agent-owned lifecycle

## Goal

Prove that Ox accepts independent Git worktrees as ordinary session roots while
leaving their creation, branches, dirty files, and removal entirely to the
client or user. Document the supported workflow and its isolation limits.

## Implementation

- `internal/e2e/worktree_test.go` — build a temporary real Git repository with
  an initial commit and two sibling worktrees on distinct branches. Add focused
  helpers local to this test file for noninteractive Git commands and worktree
  inspection; do not add Git behavior to the shipped binary or generalize the
  process harness around a one-slice fixture.
- Drive two sessions in one Ox process with the two worktree paths as `cwd`. Use
  local file tools to read and mutate the same relative filename to different
  values, then verify each working tree retains only its own result. Assert ACP
  session metadata and model/tool path context use each canonical worktree root
  rather than the Ox process directory or repository's primary checkout.
- In the same real-process coverage, give each worktree distinct root
  instructions, force bounded output to a session spill and read it back, and
  write/search memory. Prove instructions and paths come from the owning
  worktree, spill access remains confined to the owning session, and a fact
  written for one worktree is absent from the other.
- Exercise `session/close`, followed by `session/delete`, after making tracked
  changes and adding an untracked file. Verify both worktree directories,
  dirty/untracked contents, Git worktree registrations, and branch refs remain
  intact while only Ox-owned session records and spills are removed.
- `docs/worktrees.md` — add a short guide showing user-owned `git worktree add`,
  passing the resulting absolute path as the ACP session `cwd`, and explicit
  user cleanup. State that close/delete never remove the worktree or branch,
  file tools are root-confined, approved shells/language servers/MCP retain host
  privileges, and sibling worktrees still share repository metadata.
- `docs/zed.md` — link to the worktree guide where users choose the project root
  instead of repeating its lifecycle or isolation contract.

## Tests

- Run the real-Git process test on the local executor path and verify sibling
  file separation, canonical root reporting, root-specific instructions and
  memory, session-owned spill behavior, and preservation after close/delete.
- Keep Git assertions plumbing-only: test branch/worktree/dirty-state outcomes,
  not Git's own worktree implementation.
- Inspect the documentation diff and run the focused documentation checks in
  addition to the ACP-visible gates required by the roadmap.

## Decisions

- Keep the acceptance coverage in one process-level test file. The feature adds
  no production Git adapter, worktree API, rollback path, or teardown hook.
- Create worktrees directly with the system Git executable in the test. Skip
  only when Git itself is unavailable; test failures from repository setup or Ox
  behavior remain failures.
