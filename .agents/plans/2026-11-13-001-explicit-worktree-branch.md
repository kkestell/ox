# Explicit worktree branch choice

## Goal

A new worktree session names its branch with a single string today, and oxd works out from the repository whether that name means "check out this local branch", "track this remote branch", or "create this branch". A misspelled branch name therefore silently creates a new branch off `HEAD`. Separately, a start point is accepted with no branch, which only gives a new auto-named branch somewhere other than `HEAD`, something a named new branch already does.

When this is done:

- `session.new` says what the client wants: a worktree on the auto-named `ox/<id8>` branch from `HEAD`, on an **existing** branch (local, or remote-only, which becomes a local tracking branch), or on a **new** named branch with an optional start point. oxd refuses an existing branch that is not there and a new branch whose name is taken.
- A start point exists only on a new named branch.
- `workspace.branches` says which worktree has each local branch checked out.
- The app offers "New Worktree on Existing Branch…" and "New Worktree on New Branch…". The existing-branch dialog shows branches checked out elsewhere, with their worktree, and they can't be chosen.
- `oxctl new` takes `--worktree`, `--branch <existing>`, or `--new-branch <name> [--start <commit>]`.

Delete, archive and unarchive are unchanged.

## Related code

- `crates/ox-protocol/src/messages.rs` — `Checkout` (line 187), `Branches` (line 283), `SessionNew` and `WorkspaceBranches` docs, and the serialization tests around line 515.
- `crates/oxd/src/worktree.rs` — `plan` (line 260) resolves the branch and base; `resolve` (line 362) decides between local, tracked remote, and new; `checked_out_at` (line 456) finds the worktree that holds a branch; `Kind` (line 212).
- `crates/oxd/src/branches.rs` — `list` (line 55) builds the branch lists with `git for-each-ref`.
- `crates/oxd/src/daemon.rs` — `new_session` (line 392) unpacks `Checkout::Worktree`; `branches` (line 472) builds `Branches`.
- `crates/oxd/tests/worktrees.rs` — `in_worktree` helper (line 443) and the creation and listing tests.
- `crates/oxctl/src/main.rs` — `Command::New` flags (line 40) and their mapping to `Checkout` (line 208).
- `crates/ox/src/lib.rs` — Tauri `new_session` and `workspace_branches` pass `Checkout` and `Branches` through unchanged.
- `ui/src/protocol.ts` — `Checkout` and `Branches` types.
- `ui/src/SessionList.tsx` — the New Session menu (line 276) and the dialog it opens.
- `ui/src/NewBranchDialog.tsx` — the current single-field dialog.
- `ui/e2e/specs/worktree.e2e.ts` — menu and dialog end-to-end tests.

## Decisions

- **Protocol shape.** `Checkout::Worktree { branch: Option<WorktreeBranch> }`, where

  ```rust
  #[serde(tag = "type", rename_all = "snake_case")]
  pub enum WorktreeBranch {
      /// A local branch no worktree has checked out, or a remote branch,
      /// named `<branch>` or `<remote>/<branch>`, that becomes a local
      /// branch tracking it.
      Existing { name: String },
      /// A new branch that starts at `start`, `HEAD` when unset, with no
      /// upstream. Refused if a local or remote branch has the name.
      New { name: String, #[serde(default)] start: Option<String> },
  }
  ```

  `branch: None` keeps the `ox/<id8>` branch from `HEAD`. On the wire it looks like `{"type":"worktree","branch":{"type":"new","name":"topic","start":"main"}}`. The `start` field of `Checkout::Worktree` goes away, along with the "a start point applies only to a new branch" refusal in `plan`, because the type can no longer express that case.

- **Daemon resolution.** Split `resolve` into `resolve_existing` and `check_new`:
  - `resolve_existing` is the current `resolve` without its last fallback. Where `found` is empty, it refuses with `no branch {name} locally or on a remote` instead of returning `Kind::New`.
  - `check_new` runs `check_branch_name` and then refuses a name that is a local branch (`a branch {name} exists`), a remote branch's full name `<remote>/<branch>`, or the name of a branch on any remote (`{name} exists on {remote}`).
  - `plan` matches on `Option<WorktreeBranch>` to get `(branch, kind, base)`. Every refusal still happens before anything is created.

- **Checked-out branches in the listing.** `Branches.local` becomes `Vec<LocalBranch>`, where `LocalBranch { name: String, worktree: Option<PathBuf> }` and `worktree` is the worktree that has the branch checked out. `branches::list` adds `%(worktreepath)` to its `for-each-ref` format, which git fills only for `refs/heads`. This covers the workspace's own checkout, other Ox sessions, and worktrees made outside Ox. `remote` stays `Vec<String>`.

- **App menu and dialogs.** "New Worktree on Branch…" becomes two menu items:
  - **"New Worktree on Existing Branch…"** opens `ExistingBranchDialog`. Its Branch field is a `<select>` with a Local group and a Remote group. Local branches that have a `worktree` appear as disabled options labelled `main — checked out at /path`. The Remote group lists only remote branches with no local branch of the same name, with or without the remote's prefix (a local branch literally named `origin/feature` would win in `resolve_existing`), and sends them as `origin/feature`, which also avoids the several-remotes refusal.
  - **"New Worktree on New Branch…"** opens `NewBranchDialog`, reduced to Branch name and Start From. It keeps the datalist of branches for Start From. When the typed name matches a local branch, or a remote branch with or without its remote, it shows "A branch named X exists" and disables Create.

  Both dialogs load the branches the way `NewBranchDialog` does now, first as they stand and then again after a fetch. That loading moves into a `useBranches(workspaceId)` hook in `ui/src/useBranches.ts` (alongside `useWorkspaces.ts`). The hook returns `{ branches, fetchError }`.

- **oxctl flags.** `--worktree`, `--branch <name>` and `--new-branch <name>` are mutually exclusive (a clap `ArgGroup`), and `--start` requires `--new-branch`. Help text:
  - `--branch`: "Run in a new worktree on this existing branch: a local branch, or a remote branch to track"
  - `--new-branch`: "Run in a new worktree on this new branch"
  - `--start`: "Start the new branch at this commit [default: HEAD]"

## Check your assumptions

1. **`oxctl --branch` and `{"branch": "<name>"}` no longer create branches.** Kept.
   - **Decided:** `--branch` and `existing` refuse a name that doesn't exist, so a typo fails. The project has no external users (AGENTS.md), so every caller is updated.
   - **Consequences:** Any script or habit of `oxctl new --branch newname` has to switch to `--new-branch newname`. `oxctl new --start X` without a branch is refused by clap.
   - **Alternative:** Keep `--branch` creating when absent and add `--existing-branch` for the strict form.
   - **Trade-offs:** Keeping `--branch` lenient saves retyping but leaves the typo trap on the shortest flag. The strict form fixes the problem the change exists for.
2. **The existing-branch picker is a `<select>`, not a text field with suggestions.** Kept.
   - **Decided:** A select can't be mistyped and can show disabled checked-out branches with their worktree. A datalist can't disable options.
   - **Consequences:** No type-to-filter beyond the browser's jump-by-first-letter. Repositories with hundreds of remote branches give a long list.
   - **Alternative:** Keep a text field with a datalist and rely on oxd's refusal for typos and checked-out branches.
   - **Trade-offs:** A text field is quicker in big repositories but catches mistakes only after Create, and can't show which worktree holds a branch before you pick it.
3. **The choice is made in the menu, with two items, not by a toggle inside one dialog.** Kept.
   - **Decided:** "New Worktree on Existing Branch…" and "New Worktree on New Branch…" each open a dialog with fixed fields, so neither dialog has a mode control. The menu grows from three items to four.
   - **Consequences:** Switching between existing and new means cancelling and reopening from the menu.
   - **Alternative:** One "New Worktree on Branch…" dialog with an Existing/New toggle at the top.
   - **Trade-offs:** A toggle keeps the menu short and allows switching in place, at the cost of mode state and fields that appear and disappear in one dialog.
4. **Remote branches that already have a local branch of the same name are left out of the existing-branch picker.** Kept.
   - **Decided:** Picking `origin/main` when `main` exists locally is refused today ("a local branch main exists; name it instead"), so listing it would only lead to that error.
   - **Consequences:** `origin/main` doesn't appear. You pick `main` from the Local group, and it is disabled if it's checked out.
   - **Alternative:** List every remote branch and let oxd refuse.
   - **Trade-offs:** Listing everything shows the full remote picture, but offers choices that always fail.
5. **A new branch can't reuse a name that exists only on a remote.** Kept.
   - **Decided:** `new` refuses `feature` when `origin/feature` exists, because creating an unrelated local `feature` alongside it would be the same kind of silent surprise this change removes.
   - **Consequences:** To start a branch called `feature` somewhere other than `origin/feature`, you pick another name.
   - **Alternative:** Allow it, creating `feature` with no upstream at the start point.
   - **Trade-offs:** Allowing it is more flexible, but creates a local branch that shadows a remote branch it doesn't track.

## Test plan

`crates/oxd/tests/worktrees.rs`: replace `in_worktree` with `on_ox_branch()`, `on_existing(name)` and `on_new(name, start)` helpers that build `Checkout`, and update every call site.

- `a_named_new_branch_starts_where_asked_without_an_upstream`: uses `on_new("topic", Some("origin/feature"))`; expectations unchanged.
- `an_existing_branch_is_checked_out_unless_a_worktree_has_it` and `a_branch_only_on_a_remote_is_tracked`: use `on_existing`; expectations unchanged.
- New `an_existing_branch_that_is_not_there_is_refused`: `on_existing("nonexistent")` is refused with `INVALID_REQUEST`, no session is listed, and the branch list is unchanged.
- Replace `a_start_point_is_refused_for_a_branch_that_exists` with `a_new_branch_is_refused_when_its_name_is_taken`: `on_new` with `other` (local), `feature` (remote only) and `origin/feature` is refused, with no session and the branch list unchanged.
- `a_request_git_cannot_satisfy_is_refused_with_nothing_created`: the start-injection case becomes `on_new("topic", Some("--output=/tmp/x"))`, and the name cases use `on_new`.
- `branches_are_listed_most_recently_committed_first`: compares `local` names. Add assertions that `main` reports the clone's top level as its worktree and `newer` reports none.
- New `branches_name_the_worktree_that_has_them_checked_out`: after an `on_existing("other")` session starts, `other`'s worktree is the session's worktree path.

`crates/ox-protocol/src/messages.rs`: the `session.new` round-trip test covers `branch: null`, `existing`, and `new` with and without `start`, and a missing `branch` field.

`ui/e2e/specs/worktree.e2e.ts`:
- The menu in Git offers four items in order: Existing Checkout, New Worktree, New Worktree on Existing Branch…, New Worktree on New Branch….
- Existing-branch dialog:
  - lists `main` disabled with the workspace path in its label;
  - lists `origin/feature` and not `origin/main`;
  - lists `origin/late` after the fetch;
  - choosing `origin/feature` starts a session whose worktree is on `feature`.
- New-branch dialog:
  - creates `topic` from `main`;
  - typing `feature` or `main` shows the exists note and disables Create;
  - a failed fetch shows `.fetch-error` and keeps the suggestions.

## Implementation plan

1. `crates/ox-protocol/src/messages.rs`: add `WorktreeBranch` and `LocalBranch`, change `Checkout::Worktree` and `Branches.local`, rewrite the `Checkout`, `SessionNew` and `WorkspaceBranches` doc comments to describe the new forms, and update the serialization tests. `lib.rs` re-exports `messages::*`, so the new types need no export change.
2. `crates/oxd/src/worktree.rs`: change `plan` to take `Option<WorktreeBranch>`, split `resolve` into `resolve_existing` and `check_new`, drop the start-on-existing arm, and update the doc comments of `plan`, `Kind` and the resolvers.
3. `crates/oxd/src/branches.rs`: read `%(worktreepath)` in `list` and return `Vec<LocalBranch>` for the local branches. `crates/oxd/src/daemon.rs`: update `new_session`'s destructuring and `branches`.
4. `crates/oxd/tests/worktrees.rs`: the helper and test changes above.
5. `crates/oxctl/src/main.rs`: replace the flags with `--worktree`, `--branch`, `--new-branch` and `--start` as above, and build `Checkout` from them.
6. `ui/src/protocol.ts`: add `WorktreeBranch` and `LocalBranch`, and change `Checkout` and `Branches`.
7. `ui/src/useBranches.ts`: the branch-loading hook taken from `NewBranchDialog`.
8. `ui/src/ExistingBranchDialog.tsx`: the select-based dialog. `ui/src/NewBranchDialog.tsx`: reduce it to name and Start From, using `useBranches`, with the exists check. Both send `checkout: { type: "worktree", branch: {...} }`.
9. `ui/src/SessionList.tsx`: two menu items, separate dialog state for each, and `branch: null` for New Worktree.
10. `ui/e2e/specs/worktree.e2e.ts`: the e2e changes above.
