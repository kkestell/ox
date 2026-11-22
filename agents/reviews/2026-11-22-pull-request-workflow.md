# Pull request workflow

## Scope and coverage

The uncommitted changes in `../ox-pull-request-workflow` on branch
`pull-request-workflow`, against local `main` at `5b4ff4c`, checked against
`agents/plans/2026-11-22-011-pull-request-workflow.md`. Tasks 2–12 are in
scope. Tasks 1, 13, 14's pull request step, and 15 belong to the top-level
agent: the GitHub labels, issues, push, pull request, and branch protection,
and deleting `docs/todo.md` and `docs/known-issues.md`.

Gaps:

- `ci.yml` is untested until the pull request's first `check` run, as the plan
  states.
- The action versions were not checked against their releases.

## Summary

| ID | Severity | Category      | Action   | Status   | Title                                                             |
| -- | -------- | ------------- | -------- | -------- | ----------------------------------------------------------------- |
| F1 | Medium   | Correctness   | Fix      | Resolved | Worktree dev stack's XDG overrides reach Claude sessions          |
| F2 | Low      | Documentation | Fix      | Resolved | ox-review gives a commit scope the merge-base as its base         |
| F3 | Low      | Documentation | Fix      | Resolved | ox-review's `gh pr checkout` can switch the current checkout      |
| F5 | Low      | Documentation | Fix      | Resolved | Reviews skip the open `bug` issue the change resolves             |
| F4 | Low      | Correctness   | Rejected | —        | A probed free port can be taken before Vite or the relay binds it |

## F1. Worktree dev stack's XDG overrides reach Claude sessions

Medium · Correctness · Fix · `cmd/ox/web/dev.mjs:23`

In a linked worktree, `dev.mjs` sets `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, and
`XDG_RUNTIME_DIR` on its own `process.env`. `oxd` inherits them, and
`internal/claude/claude.go:176` starts each `claude` with `cmd.Environ()`, so
every Claude session in a worktree's stack, and every tool it runs, sees
`<checkout>/.state` as the user's configuration directory.

Tools that read `$XDG_CONFIG_HOME` lose the user's settings there. `gh` reports
no logged-in hosts, so a session cannot read issues or open pull requests.
`git` stops reading `~/.config/git/ignore`, which on this machine ignores
`**/.claude/settings.local.json`, so a session's `git add -A` can stage it. The
plan's trust boundary keeps Ox's files under `.state`; it does not ask for
the user's other tools to move there.

### Evidence

- Trace: `dev.mjs:24-29` assigns the variables on `process.env`;
  `stack.mjs:83` spawns `oxd` with the inherited environment;
  `claude.go:176-179` passes it to `claude`.
- `XDG_CONFIG_HOME=/tmp/oxrev-xdg gh auth status` prints "You are not logged
  into any GitHub hosts"; without it `gh` is logged in from `~/.config/gh`.
- `XDG_CONFIG_HOME=/tmp/oxrev-xdg claude auth status` stays logged in, so the
  Claude account itself is unaffected.

### Action

Locate the worktree stack's files with the binaries' existing flags instead of
the environment, so the stack's environment, and the Claude sessions it starts,
stay the user's:

- `oxd`: `-config .state/config.json`, `-db .state/ox.db`,
  `-socket .state/oxd.sock`, and a new `-ur` flag for Ur's directory, defaulted
  through `defaultPath(…, paths.Ur)` like the other three.
- `ox`: `-config .state/config.json` and `-socket .state/oxd.sock`.

`dev.mjs` adds these arguments to the `oxd` and `ox` services in a linked
worktree and sets no XDG variable. The ports stay as they are. Update the
`dev.mjs` comment; `docs/architecture.md`'s sentence still holds.

Test: no automated layer reaches `dev.mjs`, as the plan's Test plan states.
Repeat the manual worktree check, and confirm in a session of the worktree's
stack that `gh auth status` reports the user logged in, and that `.state`
holds the configuration, database, socket, and Ur directory.

## F2. ox-review gives a commit scope the merge-base as its base

Low · Documentation · Fix · `.claude/skills/ox-review/SKILL.md:48`

Step 2 sets the base revision to `git merge-base origin/main HEAD` for every
scope. For a commit scope that base is wrong: reviewing a commit already on
`main`, the merge-base is `HEAD` itself, and the review's recorded base is the
commit under review. The plan settles the merge-base for uncommitted changes,
a branch, and a pull request.

### Evidence

On `main` after a pull, `git merge-base origin/main HEAD` equals
`git rev-parse HEAD`, so a review of `HEAD` records no change against its base.

### Action

State the base by scope: `git merge-base origin/main HEAD` for uncommitted
changes, a branch, or a pull request; the commit's parent for a commit.

## F3. ox-review's `gh pr checkout` can switch the current checkout

Low · Documentation · Fix · `.claude/skills/ox-review/SKILL.md:48`

Step 2 says to review a pull request "in its worktree or in one made with
`gh pr checkout`". Without `--worktree`, `gh pr checkout` switches the branch
of the checkout it runs in. A reviewer started in the main checkout moves it
off `main`, which `AGENTS.md` ("Workflow") keeps on `main`.

### Evidence

`gh pr checkout --help`: the command checks out into the current repository
unless `--worktree path` is given.

### Action

Name the command in full: `gh pr checkout <number> --worktree ../ox-<branch>`.

## F4. A probed free port can be taken before Vite or the relay binds it

Low · Correctness · Rejected · `cmd/ox/web/dev.mjs:7`

`freePort` closes its listener before Vite or `ox` binds the port, so another
process can take it in between.

### Action

Rejected: the window is milliseconds on a developer's machine, and a lost port
fails clearly, since Vite runs with `strictPort` and `ox` exits on a failed
listen, which stops the stack. Holding the ports would cost machinery for a
failure that has not happened.

## F5. Reviews skip the open `bug` issue the change resolves

Low · Documentation · Fix · `.claude/skills/ox-review/SKILL.md:70`

Every defect is now a `bug` issue, including the ones a change is planned to
fix. Such an issue stays open until the pull request merges, so during the
review it is an open `bug` issue that records the defect. `ox-review` step 7
says to skip it "unless what it waits on has changed", and `AGENTS.md`
("Defects") says the same. A fix that leaves the defect in place is then
skipped by the rule meant for defects that cannot be fixed yet, and is caught
only if the reviewer weighs the Intent check above step 7. Most changes in
the coming stabilization work resolve a `bug` issue, so the conflict comes up
on most reviews.

### Evidence

Trace: the plan's Goal links the issue it resolves (`ox-plan` step 14); the
pull request closes it with `Fixes #N` on merge (`AGENTS.md` "Workflow" step
7); `ox-review` step 7 and `AGENTS.md:192` skip every defect an open `bug`
issue records. Before this change, the fix removed its `docs/known-issues.md`
entry in the reviewed tree, so the entry was gone when the review read it.

### Action

In `AGENTS.md` ("Defects") and `ox-review` step 7, exempt the issues the change
resolves: a review checks that the change fixes each issue its plan resolves,
and reports any other defect an open `bug` issue records only when what that
issue waits on has changed.

## Checks run

- Implementer's checks taken as reported: `make test`, `make e2e` (108 passed,
  4 skipped), the headless Chrome worktree check, the `make worktree` probe,
  `make metrics`, the pre-commit script, `dprint check`, and `ci.yml` parsing.
  These cover every check `AGENTS.md` ("Verification") requires for the
  change. `make live` is not required: the change does not alter how Ox drives
  Claude Code.
- `gh auth status` and `claude auth status` with `XDG_CONFIG_HOME` pointed at
  an empty directory, for F1.
- `rg 'todo\.md|known-issues|metrics\.md|NNN|9080'` outside `agents/`: no
  matches.
- `docs/functional-requirements.md` is unchanged, as the plan's Requirement
  changes state.

## Verification 1

The uncommitted tree on `pull-request-workflow` against `5b4ff4c`. Ran
`TestDaemonOwnsSocketAndDatabaseUntilShutdown` (passes), `go vet ./cmd/oxd`
and `gofmt -l cmd/oxd` (clean). Took the implementer's `make test`,
`make e2e` (108 passed, 4 skipped), `dprint check`, and worktree `make dev`
check in headless Chrome as reported.

| ID | Status   | Evidence                                                                                                                                                                                                                                                        |
| -- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `dev.mjs` sets no XDG variable and passes `-config`, `-socket`, `-db`, and `-ur` under `.state`; `oxd` gains `-ur`, defaulted through `paths.Ur`. The implementer's `ps eww` showed no XDG variables on `oxd`, and `.state` held every file after an Ur prompt. |
| F2 | Resolved | Step 2 gives the merge-base for uncommitted changes, a branch, or a pull request, and the commit's parent for a commit.                                                                                                                                          |
| F3 | Resolved | Step 2 names `gh pr checkout <number> --worktree ../ox-<branch>`.                                                                                                                                                                                               |

No new findings.

## Verification 2

The uncommitted tree on `pull-request-workflow` against `5b4ff4c`, after the
labels change to `bug` and `enhancement`. Read the diffs of `AGENTS.md`, the
four skills, the ox-review and ox-audit templates, and the README. Ran
`rg -i 'known.issue|\btodo\b|todo\.md|audit issue'` outside `agents/` and the
two files being migrated: no references remain. `docs/todo.md` and
`docs/known-issues.md` were still on disk at this revision; the top-level agent
deletes them. Took the implementer's `dprint check` as reported. `make test`
and `make e2e` were not re-run: the change since Verification 1 is text only.

| ID | Status   | Evidence                                   |
| -- | -------- | ------------------------------------------ |
| F1 | Resolved | Unchanged since Verification 1.            |
| F2 | Resolved | Unchanged since Verification 1.            |
| F3 | Resolved | Step 2 is unchanged since Verification 1.  |

New finding: F5.

## Verification 3

The uncommitted tree on `pull-request-workflow` against `5b4ff4c`, limited to
F5 and the deletion of `docs/todo.md` and `docs/known-issues.md`. Both files
are deleted, and `rg 'todo\.md|known-issues'` outside `agents/` finds nothing.
`dprint check` passes.

| ID | Status   | Evidence                                                                                                                                                                                 |
| -- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F5 | Resolved | `AGENTS.md` ("Defects") and `ox-review` step 7 skip a recorded defect unless the change resolves its issue or what it waits on has changed, and check that the change fixes each issue it resolves. |

No new findings.

## Verdict

`Ready to commit`.
