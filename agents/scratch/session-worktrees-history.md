# Session worktrees: what went wrong in hypercube

Research note for Ox's session worktrees. Sources: hypercube's history
(`/Users/kyle/src/hypercube`, 992 commits, read at HEAD 60b62ab), its GitHub
issues, and its plans, work logs, reviews, and audits in `.agents/` and
`agents/`. Hashes are hypercube commits; `R`/`G`/`W` numbers refer to review
and audit findings by record path below. "Inferred" marks a claim not shown
directly by a diff or record.

## Summary

Hypercube's worktree defects came from five structures:

1. Creation and removal were multi-step sequences: row, branch, checkout, setup,
   and launch. Each had hand-written rollback, and several parties started,
   stopped, and cleaned up the same resources. The create and rollback order
   was rewritten at least five times (c1cc9ef, e966053, 9989971, 1b51b7f,
   f82142d).
2. Git facts were copied into SQLite and memory: Branch, Base, Tip, NoGit, and a
   merge-status cache. Agents, users, and git changed them underneath.
3. A separate safety check came before a forced delete, often while the agent
   still ran in the directory.
4. Worktrees lived inside the checkout (`.hypercube/…`), and children were
   nested in their parent's worktree.
5. Feature scope: archive/unarchive deleted and recreated worktrees and
   branches, plus merge status and a Merge button, discard, branch choice, PR
   review, and `.worktreeinclude` setup.

The written record holds about 50 worktree findings. There are about 20 fix
commits in the first 9 days (10-25 to 11-02) and about 25 more from 11-03 to
11-10. Some fixes were lost in rewrites (`.worktreeinclude` quoting R3 → W6;
git run in a missing directory R1 → W1 → W22). That shows the fixes lived in
code, not in a design that ruled the bug out.

## 1. The implementations

### First: workers (Go prototype 10-25, Rust 10-27..10-29, Go/tmux 10-30..11-02)

- **Creator.** There was no daemon at first. The orchestrator agent created
  workers through MCP `create_agent`, and the MCP stdio process ran
  `workspace.AddWorktree` itself (32ccf36).
- **Rust era.** It added a background host that owned ptys and panes
  (45e81f6). The MCP process still created the worktree and the state row, then
  asked the host to launch over a socket: two parties per creation.
- **Naming.**
  - Branch `hypercube/<name>`, directory `<checkout>/.hypercube/<name>`, with
    `/.hypercube/` appended to `.git/info/exclude`.
  - The worktree sat inside the checkout so harness folder trust covered it
    (32ccf36).
  - Later `hypercube/<team>/<name>` (806cd59), then for nested sessions
    `hypercube/<parentID>/<name>` in `.hypercube/<parentID>/<name>` (941500d).
- **Base.** The orchestrator's HEAD.
- **State.**
  - At first, a flocked JSON file per workspace holding `Agent{Dir, Branch,
    Target pane, SessionID}`.
  - Later SQLite (f1497f8), with a child row `{Dir, Branch, Ended}`.
  - "Owns a worktree" was `Branch != ""` (f40df1f, plan adb38f8).
  - `NoGit` was cached per workspace (e2e50cf).
- **Setup.** From ce734bb: APFS `cp -c` clones of gitignored paths matched by
  `.worktreeinclude`, then `git submodule update --init --recursive`. A setup
  failure ran `worktree remove --force` and `branch -D`.
- **Removal.**
  - First version: a merged-into-HEAD check, `worktree remove`, `branch -d`,
    kill the pane, drop the row.
  - Later: a `Removable` check, then `worktree remove --force`.
  - The order of the remove and the pane kill flipped (e966053), and EndChild
    removed the worktree while the child agent still ran (941500d).
  - Workspace `delete` pre-checked workers, killed the session, then removed
    worktrees one at a time.
- **Merge.** By prompt only: the orchestrator merged worker branches into its
  checkout.
- **Recovery.** No crash recovery or reconciliation with git.

### Second: worktree sessions (Go daemon + TS web UI, 84e8317 11-03 .. HEAD)

- **Creator.** The daemon's `internal/session` Runtime creates every worktree
  and branch. 47b3fe1 denies OpenCode's own `worktree_create` and
  `session_move` tools.
- **Naming.**
  - Top-level: `hypercube/session-<id>`, or a user-chosen name (872f1a5), in
    `<checkout>/.hypercube/<id>`.
  - Child: `hypercube/<parentID>/<name>` in `.hypercube/<parentID>-<name>`.
  - Three child layouts in six days (84e8317 nested inside the parent's
    worktree, 47b3fe1, aeecc82). Each conflicted with `Dir` values already
    stored in rows.
- **SQLite.**
  - `sessions`: Dir, Branch, Base (start commit, which doubles as the "is a
    worktree session" flag), Tip (branch tip at archive, since archive deleted
    the branch), Ended, Archived, Parent.
  - `workspaces.NoGit`.
  - No kind or state enum: whether a field is valid depends on combinations of
    the others.
- **Memory.**
  - `merges map[id]Merge{Branch, Target, Unmerged, Committed, Uncommitted,
    Error}`, refreshed on turn end, create, restore, unarchive, and discard.
  - Coordination maps: `deleting`, `childCreates`, `workspaceCreates`,
    `workspaceDeleting`, plus `refreshMu`.
- **Create (top-level).** AddWorktree, then Base from the new worktree's HEAD,
  then save the row; remove the worktree if the save fails. AddWorktree rolls
  back itself:
  - an add failure runs `branch -D`
  - a setup failure runs `remove --force` and `branch -D`
- **Create (child).** The order is the reverse: row first, then AddWorktree
  outside the lock. A `childCreates` channel makes removal wait (ffcf0fe).
- **Archive and Delete.** `removeFamily` (removal.go, 444 LOC):
  1. claim
  2. wait for child creations
  3. Removable
  4. stop launches and shells
  5. recheck
  6. record Tip
  7. `worktree remove --force`, repo-wide `worktree prune`, `branch -D`, per
     tree, children first
- **Removable** (8bb30c9, 71cbb6b): no uncommitted files; branch and HEAD
  reachable from checkout HEAD, Base, or any remote-tracking branch; and no
  unpushed submodule commits.
- **Unarchive.** Re-add the worktree at Tip, or at HEAD if Tip is gone. Refused
  if the branch name now exists.
- **Discard.** Stop the family. Then per tree: `reset --hard`, abort any rebase,
  `switch --force-create <branch> <Base>`, `clean -ffdq`.
- **Merge.** The daemon runs no git itself. It checks the live checkout branch,
  then prompts the agent to merge into it (71cbb6b, e460ff1). There is no
  sync or rebase feature, only agent instructions.
- **PR review.** Fetch `refs/pull/N/head` and start a worktree there with Base
  set to the PR head (cbe9975). Chat on a PR row adds a `-N` suffix when the
  branch name is taken (26c8ebb).
- **Changes panel.** Polled read-only git with `GIT_OPTIONAL_LOCKS=0`, because
  Apple Git rewrote the index during `diff` (08c7670).
- **Processes.** Separate holder executables kept agents alive across app
  restarts. Restore re-adopted shells and delivered queued messages. Launch does
  not check that `Dir` exists (inferred from launch.go:132).

## 2. Bug classes

Counts come from the review and audit record. One finding can fall in two
classes.

### A. Half-created resources and rollback (about 9 findings, 10+ fix commits)

- **Leftover branch.** `git worktree add -b` creates the branch before the
  checkout. A failed add left the branch, and every retry failed with "branch
  already exists" (R4, 2026-10-28-001 F9, fixed df669f4).
- **Launch failure.** The row was removed and the worktree and branch kept, so
  the name could not be reused (c1cc9ef).
- **Remove and branch delete.** `worktree remove` succeeded and `branch -d`
  then failed, leaving a half-removed worker (c1cc9ef).
- **Swallowed errors.** Rollback errors were discarded (e966053, 3c7f96b).
- **Failed unarchive.** The worktree and branch stayed, and every retry failed
  (2bfbcdf, audit 2026-11-08-007 F2).
- **Rollback through the safety check.** The unarchive rollback went through
  the user-removal check, which refused a restored Tip that no ref reached. The
  session could never be retried (1f8799f, W23).
- **Persist, then fail.** Review persisted the session and worktree before the
  first prompt. The prompt failed and returned 409 with no ID, so each retry
  added an orphan worktree session (78e76b8).
- **Cost.** Names that cannot be reused, orphan worktrees and branches, and
  sessions stuck until fixed by hand.
- **Structure.** Creation is a sequence of separate side effects. Each caller
  wrote its own rollback, and the rollback reused rules written for another
  purpose.

### B. Lifecycle owned by several parties; creation racing removal (about 9 findings)

- **Two parties per launch (Rust).** The MCP process created the worktree and
  the host owned the pane. A lost launch reply left a pane the MCP side thought
  had failed (e966053). Rollback then depended on the host confirming Close
  (9989971, a1b7280).
- **Delete from a stale list.** `delete` used a worker list read before
  stopping the host, so workers created in between were orphaned (bff984f). A
  host could also start during delete (582f49f).
- **Task rollback.** It deleted a team whose orchestrator had already created
  worktree workers (e9fc670).
- **Reordering regression.** Reordering child creation to worktree-first
  (1b51b7f) let Stop overlap setup and leave a worktree and branch with no row.
  It was reproduced with a FIFO gate (review 2000e39 G3) and reverted
  (f82142d).
- **Family removal versus child creation.**
  - Removal snapshotted children before claiming. A child created meanwhile
    was nested in the parent's worktree, removed unchecked, and its row
    cascaded (13e16d1, review 2026-11-04-003 F1, High, data loss).
  - Removal claimed a child mid-setup, and both removal and creation rollback
    cleaned up: "branch not found" (#58, ffcf0fe).
- **Workspace deletion versus session creation.** A session admitted during
  deletion was cascaded away, leaving a dirty worktree and a live runtime
  orphaned (#41, 5ee6d6c).
- **The claim's side effects.** The removal claim then had to be honored by
  wake, park, and delivery. A refused removal stranded queued messages and left
  the idle timer disarmed (review 2026-11-05-006 F1 and F3).
- **Cost.** Data loss, orphan worktrees, and stuck or forever-running agents.
- **Structure.** Membership and resources were read without owning them. Each
  fix added a claim map or channel, and every subsystem then had to honor it.

### C. State duplicated from git (about 9 findings)

- **`NoGit`.**
  - Cached once, so it stayed set after `git init` (df669f4 F6).
  - Later set by any `rev-parse` failure (dubious ownership, Xcode license),
    which disabled worktrees until the workspace was re-added (#59, 28cbbe7).
- **Branch.**
  - The orchestrator branch stored at start went stale after `git switch`
    (ca04082).
  - The removal check trusted the stored branch while the agent had detached
    or switched, risking unmerged commits (c1cc9ef).
  - Checkout sessions rewrote their branch on every launch, giving the wrong
    PR match (plan review 2026-11-09-003 F1).
- **Base.** Read from the checkout instead of the new worktree, so a
  concurrent commit made a fresh session show "Merged" (4203baf, F2).
- **Merge cache.**
  - It kept entries for archived and deleted sessions (2bfbcdf F7).
  - The cached merge target meant Merge told the agent to merge into a branch
    the checkout had left (71cbb6b #5b).
- **Discard assumption.** Discard assumed HEAD was the session branch, so it
  would have rewound the agent's other branch (plan review 2026-11-09-013 F2).
- **Tip.** Storing Tip instead of keeping the branch means a garbage-collected
  commit restarts the session from HEAD without telling the user
  (docs/storage.md, 8bb30c9).
- **Cost.** Wrong status, merges into the wrong branch, features disabled, and
  silent loss of a branch's work.

### D. Removal with uncommitted or unreachable work (6 findings, data loss)

- **Display config.** The dirty check honored `status.showUntrackedFiles` and
  submodule `ignore`, then `worktree remove --force` deleted the work (audit
  2026-10-29-001 A8, open when the Rust era ended).
- **Writes after the check.** EndChild removed the worktree before stopping the
  agent and shell (13e16d1, review 2026-11-04-003 F2, High).
- **Submodule commits.** These live under `.git/worktrees/<n>/modules`, and
  `--force` deleted the only copy (71cbb6b #1).
- **Nested child.** A child nested in the parent's worktree was deleted with
  the parent (13e16d1 F1).
- **Discard in the wrong repository.** Discard's `reset --hard` in a
  `.hypercube/<id>` directory that was no longer a worktree root would have
  reset the user's main checkout. Caught in plan review 2026-11-09-013 F1.
- **Structure.** `--force` after a separate check, with the process still
  running, in a location nested inside another repository.

### E. Process or record outliving its directory (6 findings)

- **Missing directory.** Removal ran git in a stored directory that no longer
  existed, so a workspace or family could never be deleted. This recurred
  three times: R1 (df669f4 F5), W1 (4203baf F1), and W22 (EndChild with a
  missing parent directory, open at HEAD).
- **Remove before stop.** Removal took the worktree before the pane (e966053,
  dfd27b1, 941500d).
- **Late shell adoption.** Restored shells were adopted after serving began, so
  deletes missed them (2bfbcdf F1).
- **Uninstall.** It leaves worktrees and branches behind (#61).

### F. Location inside the checkout (5 findings)

- **Copying other sessions.** A broad `.worktreeinclude` pattern copied
  `.hypercube/` into new worktrees, including other sessions' uncommitted files
  and `.git` pointers (#42, 5ee6d6c).
- **Git walking up to the user's checkout.** Discard's reset (above).
- **Nested child loss.** Children nested in the parent's worktree (13e16d1).
- **Empty-directory cleanup.** Leftover `.hypercube/` directories needed
  explicit removal (9f3b729, 2026-11-01-001 F29).
- **Layout churn.** 47b3fe1 and aeecc82, the same day.

### G. Branch name collisions (5 findings)

- **Shared worker names.** Workers named alike in one workspace shared a
  branch and worktree (806cd59).
- **"Branch already exists."** Failed adds and failed unarchives caused it on
  retry (R4, W16, W23).
- **User branches.** Unarchive is refused when the user has made a branch with
  the session's name (872f1a5, W34).
- **Possible ref conflict.** `hypercube/<x>` beside `hypercube/<x>/<y>` can hit
  git's directory/file ref conflict (inferred, never observed).

### H. Setup and git output handling (7 findings)

- **Quoted paths.** `.worktreeinclude` paths git quotes broke creation twice:
  df669f4 F8, then the fix was dropped by the Go rewrite and fixed again in
  13e16d1.
- **Linux.** `cp -c` is macOS-only (4d85b1d).
- **stderr parsed as paths** (ee4fd4f).
- **Counting.** Porcelain collapses an untracked directory to one entry
  (4203baf F3).
- **Silent skip.** Stat errors on `.worktreeinclude` and `.gitmodules` were
  treated as absent (audit 2026-11-08-007 F10, open).
- **Leftover merge or rebase.** An agent left one in progress, which blocked
  discard, archive, and delete (20b699d).

### I. Merge and sync scope (6 findings plus instruction churn)

- **Counting PR commits.** Unmerged counts included the PR's commits, and
  Merge would merge them too (cbe9975 note).
- **Children's merge target.** Merge eligibility admitted children, which
  would merge into the wrong checkout (plan review 2026-11-09-011 #1).
- **Stale target.** See class C (71cbb6b).
- **Duplicate prompts.** Repeated Merge clicks send duplicate prompts (#72,
  open).
- **Agent instructions.** Agents were misinformed about where worktrees start,
  the merge target, and pushing (#53, e460ff1, 9e0186a).

### J. Archive as a second lifecycle (about 8 findings)

- **Archive deleted and recreated worktrees and branches.** That brought in
  Tip, the unarchive rollback bugs (A), the name collisions (G), and the stale
  merge cache (C).
- **Flag, not state.** Archived sessions still answered offers and accepted
  child proposals (a3e7e77 F2, #29, 2fab08c).
- **Wasted work.** Archived sessions were still refreshed with git (5a90594).
- **Complexity.** `removeFamily` reached cognitive complexity 59, branching on
  archive across every phase (#18, b16b6ee).

## 3. Cost relative to value

| Feature | Findings / fix commits | Value | Verdict |
| --- | --- | --- | --- |
| Archive deletes and unarchive recreates worktree+branch (Tip) | ~8 findings; 2bfbcdf, 1f8799f, 2fab08c, a3e7e77 | Frees disk and branch names | Worst ratio |
| Merge status cache + Merge button | ~7 findings; 71cbb6b, e460ff1, 9e0186a, #72 open | One button that sends a prompt | Poor |
| Child worktrees (nested, then sibling) | ~9 findings incl. 2 High; 3 layouts; 13e16d1, ffcf0fe, 1b51b7f, f82142d | Parallel children in one repository | Poor as built; the nesting caused most of it |
| `.worktreeinclude` and submodule setup | ~6 findings; ce734bb, df669f4, 13e16d1, 4d85b1d, 5ee6d6c | Gitignored env files present | Real value, high cost |
| Discard | 3 findings, one would have reset the main checkout | Throw away work in one click | Poor |
| PR review and branch choice | ~4 findings; 78e76b8, W25, W34 | Convenience | Moderate |
| Removal with `--force` after a check | 6 data-loss findings | Required | Necessary, but built wrong |
| Cached `NoGit` | 3 findings; df669f4, 28cbbe7, 6382506 | None over a live check | Pure cost |

## 4. Constraints for Ox, and the proposal

### Proposal

- Ox runs `git worktree add` once, at session creation, and stores only the
  path, in `directory`.
- Creation fails atomically.
- There is no branch tracking, no sync or merge UI, and no automatic removal.
- Cleanup is an explicit action that refuses when there are uncommitted
  changes.

### Supported by the evidence

- **Store only the path.** This removes class C, the largest stale-state
  class. Branch, HEAD, dirty state, and merge state are read live when needed.
  Whether a session owns a worktree can be derived instead of stored: its
  directory lies under Ox's worktree directory (resolved by `internal/paths`).
  That avoids hypercube's overloaded `Base != ""` flag.
- **No merge or sync UI, no archive-driven removal.** This removes classes I
  and J, about 14 findings, and the merge cache.
- **No automatic removal.** This removes the removal-claim machinery that
  every subsystem had to honor (class B side effects).
- **Ox's existing deletion rule.** A session can be deleted only when it has
  no children. That already rules out the family-removal races (13e16d1,
  ffcf0fe), as long as worktree removal happens only for a childless session.

### Gaps and contradictions

1. **The proposal needs a branch decision.** `git worktree add` either
   creates a branch or detaches HEAD.
   - A failed `add -b` leaves the branch behind (probe below; R4).
   - A failed `add --detach` leaves nothing (probe).
   - Detached work is lost when the worktree is removed: `worktree remove`
     succeeded on a worktree whose HEAD held a commit no ref contained
     (probe).
   - Recommendation: create `ox/<session-id>` with `-b`. The name comes from a
     fresh ID, so it cannot collide (class G gone). On failure, Ox deletes the
     branch it just created. Ox never deletes the branch afterward, so
     committed work survives cleanup by construction. That removes
     hypercube's reachability rule and the Tip column.
   - Cost: `ox/*` branches accumulate until the user deletes them.
     Hypercube's plan 2026-11-09-008 rejected keeping branches for this reason
     alone, and then paid with W16, W23, and W34.
2. **"Refuses when there are uncommitted changes" must be Ox's own check, not
   git's.** Probe on git 2.50.1:
   - Without `--force`, `git worktree remove` refuses untracked files by
     default.
   - With `status.showUntrackedFiles=no` it deleted an untracked file (the A8
     class).
   - It refuses worktrees containing submodules.
   - Ignored files are deleted.

   Constraint: stop the session's process first, then run
   `git status --porcelain --untracked-files=all --ignore-submodules=none`,
   then check that `git for-each-ref --contains HEAD` is non-empty, which
   catches detached commits. Then run `git worktree remove` without
   `--force`, which keeps git's submodule refusal. Never pass `--force`
   except to roll back a worktree this call just created, before any process
   ran in it.
3. **"Explicit action" should be Delete, not a separate Remove.** A worktree
   removed while its session remains gives a session whose `directory` does
   not exist, which a parked session would try to resume into. Tying cleanup
   to Delete makes the order one sequence in the session's owner: stop
   Claude, check, `git -C <workspace> worktree remove <path>`, then delete
   the row. If git refuses, the session is not deleted. Archive leaves the
   worktree alone.
4. **Run cleanup git from the workspace directory, never from the
   worktree.** Removal from a missing directory recurred three times (R1, W1,
   W22). Probe: `git -C <repo> worktree remove <path>` succeeds when the
   directory was deleted externally. A worktree outside the checkout also
   means git can never walk up into the user's repository (W30).
5. **Location.** Put worktrees under Ox's XDG data directory (via
   `internal/paths`), named by session ID, flat for children too. This removes
   class F and the include-copy recursion.
   - Hypercube's only reason for an in-repo location was harness folder trust
     (32ccf36). Whether `claude -p` needs trust for a directory outside the
     workspace has to be checked and recorded in
     `docs/harnesses/claude-code.md`. Not verified here.
6. **"Atomic creation" has an ordering requirement.**
   - Run `git worktree add` before the session exists, with no row or
     goroutine yet. On failure, delete only the branch this call created.
     Then persist the session. If persisting fails, `worktree remove --force`
     and `branch -D` the fresh, unused worktree.
   - The creation must be admitted by the same owner that refuses workspace
     removal while sessions exist. Otherwise #41 recurs: a workspace removed
     during `worktree add`.
   - This is one owner and one sequence, not a claim map. It needs a test
     with an injected git failure and an injected store failure.
7. **No setup step.** The proposal is silent on `.worktreeinclude`. Leaving
   setup out keeps creation to one git command and removes class H's setup
   bugs. Cost: gitignored files such as `.env` and `node_modules` are absent.
   That is the one feature here with real value lost; it is a product
   question.
8. **Children.** By default a child runs in its parent's directory, as Ox
   does today. A child that asks for a worktree gets its own flat worktree
   from the workspace's HEAD. A worktree is never nested inside another.
9. **The directory field exists only for children.** Ox stores `directory` in
   the `children` table, and a top-level session takes the workspace's
   directory (`internal/store/store.go`, `internal/server/session.go`
   `directory()`). Top-level worktree sessions need that value on every
   session. The file tree, which lists the workspace directory, must list the
   session's directory instead.
10. **A missing directory at start fails clearly.** The user can delete a
    worktree outside Ox. Starting or resuming a session whose directory is
    gone must fail with a clear error, and Delete must still work (point 4).

### Verdict

The proposal is right on scope. It removes the classes that produced most of
hypercube's bugs: duplicated git state, archive as a second lifecycle, the
merge cache and UI, and automatic or concurrent removal.

It needs five constraints:

- a session-ID branch that Ox never deletes
- Ox's own dirty and detached-HEAD check after stopping the process, never
  `--force` (git's check alone loses data)
- cleanup as part of Delete, run from the workspace directory
- worktrees outside the checkout
- creation completed before the session exists, admitted by the workspace's
  owner

## Probe

Run with git 2.50.1 (Apple Git-155) in temporary repositories:

- `git worktree add -b ox/e <existing non-empty dir>` failed, and `ox/e`
  remained. The same happened with an unwritable parent.
- `git worktree add --detach <unwritable path>` failed and left no branch and
  no worktree entry.
- `git worktree remove` on a worktree with an untracked file refused by
  default, and removed it when `status.showUntrackedFiles=no` was set.
  `git status --porcelain --untracked-files=all` still listed the file under
  that setting.
- `git worktree remove` removed a worktree with a commit on a detached HEAD.
  `git for-each-ref --contains HEAD` printed nothing for that HEAD.
- `git worktree remove` refused a worktree containing a submodule.
- `git worktree remove` succeeded on a worktree whose directory had been
  deleted, and cleared its entry.
