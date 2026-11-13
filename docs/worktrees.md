# Where sessions run

A session runs either in its workspace's own checkout or in a new Git worktree
of the workspace's repository. A worktree is on one of three kinds of branch: a
new branch named for the session, an existing branch, or a new branch you
name.

## Choosing a checkout

| Where the session runs | Branch | Branch starts at | App (New Session menu) | `oxctl new` |
|---|---|---|---|---|
| The workspace's checkout | Whatever the checkout has | — | Existing Checkout | (no flag) |
| New worktree | New `ox/<first 8 digits of the session ID>` | `HEAD` | New Worktree | `--worktree` |
| New worktree | An existing local branch | Its tip | New Worktree on Existing Branch… | `--branch <name>` |
| New worktree | A new local branch that tracks a remote branch, named `<branch>` or `<remote>/<branch>` | The remote branch's tip | New Worktree on Existing Branch… | `--branch <name>` |
| New worktree | A new branch you name | `HEAD` | New Worktree on New Branch…, with Start From empty | `--new-branch <name>` |
| New worktree | A new branch you name | The commit you give | New Worktree on New Branch…, with Start From filled in | `--new-branch <name> --start <commit>` |

The worktree options appear only for a workspace in a Git working tree. In
`oxctl`, `--worktree`, `--branch` and `--new-branch` exclude each other, and
`--start` needs `--new-branch`.

The New Worktree on Existing Branch dialog lists local branches and remote
branches. A local branch that a worktree has checked out is shown with that
worktree and cannot be picked. A remote branch is listed only when no local
branch has its name. The New Worktree on New Branch dialog notes a name that a
branch already has and disables Create.

Both dialogs list the branches as they stand, then again after fetching every
remote. A failed fetch is noted in the dialog, and the lists stay as they were.

## Worktrees

Each worktree is the directory `worktrees/<checkout name>-<first 8 digits of
the session ID>` in Ox's data directory, `$XDG_DATA_HOME/ox` or
`~/.local/share/ox`. When the workspace is a subdirectory of its checkout, the
session runs in the same subdirectory of the worktree.

A new branch has no upstream. A branch made from a remote branch tracks it.

## Refusals

Ox refuses to start a worktree session, and creates nothing, when:

- the workspace is not in a Git working tree, or its repository has no commits;
- an existing branch is checked out in another worktree, including the
  workspace's own checkout;
- an existing branch exists neither locally nor on any remote;
- an existing branch is named `<remote>/<branch>` while a local `<branch>`
  exists;
- an existing branch is on several remotes and `checkout.defaultRemote` does
  not pick one of them;
- a new branch's name is a local branch, a remote branch's full name, or the
  name of a branch on any remote;
- the name is not a valid branch name, or the start point is not a commit;
- the workspace's subdirectory does not exist at the commit the worktree
  starts at.

## Comparing a session's branch

A worktree session can compare its branch with a ref, such as `main` or
`origin/main`. Ox shows the branch's commit, the ref's commit, and how many
commits the branch is ahead of the ref and behind it. It compares again when
the session's agent ends a turn, after a branch move, a session delete or a
fetch in the repository, and when you ask.

```sh
oxctl new --workspace <id> --worktree --compare main
oxctl compare <session>               # compare again and print
oxctl compare <session> origin/main   # compare with another ref
oxctl compare <session> --clear       # stop comparing
```

`oxctl compare` prints `<ref>`, `<branch commit>`, `<ref commit>`, `<ahead>`
and `<behind>`, separated by tabs, and prints nothing for a session with no
comparison ref. The ref must name a commit when it is set. A ref or branch
that later names no commit makes `oxctl compare` fail with the reason.

## Moving a branch

`oxctl move-branch` sets a branch to a commit, when the branch is at the
commit `--expected` names. It moves only by fast-forward unless `--force` is
given.

```sh
oxctl move-branch --workspace <id> main <session branch tip> --expected <main's commit>
```

When the workspace's checkout or a worktree has the branch, its files and
index move with it. Ox refuses the move while tracked files there have
uncommitted changes, while a session's agent there is starting, in a turn, or
waiting for an answer, and when the move would overwrite an untracked file.
Untracked files the move leaves alone, and idle agents, don't stop it.

## Deleting and archiving

| Action | Worktree | Branch Ox created | Branch that existed before the session |
|---|---|---|---|
| Delete | Removed | Deleted when every commit on it is on another branch, a remote branch or a tag; otherwise kept, and Ox names it | Kept |
| Archive | Removed | Kept | Kept |
| Unarchive | Added again, on the session's branch | — | — |

Ox creates the branch for every worktree session except one on an existing
local branch. Delete and archive refuse to remove a worktree that has
uncommitted changes, commits on no branch, remote branch or tag, or submodule
commits on no remote branch. Discarding (`--discard` in `oxctl`) removes it anyway.
