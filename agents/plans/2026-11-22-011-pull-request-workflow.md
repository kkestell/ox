# Pull request workflow and GitHub issues

## Goal

Every change reaches `main` through a pull request from its own worktree and
branch. Planned work, known issues and audit findings move to GitHub issues on
`kkestell/ox`, and `docs/todo.md` and `docs/known-issues.md` are deleted.
`docs/metrics.md` is no longer committed, so open PRs stop conflicting over
it. CI runs `make test`, `make e2e` and `dprint check` on each PR, and `main`
is protected. A worktree's `make dev` runs beside the user's everyday Ox, which
is the `make dev` stack in the main checkout.

## Related code

- `AGENTS.md`: the Source Map, Defects, Workflow, Verification and
  Documentation sections describe the single-checkout flow and the three docs.
- `.claude/skills/ox-plan/SKILL.md`, `ox-work/SKILL.md`, `ox-review/SKILL.md`
  and `ox-audit/SKILL.md`, with their `assets/_template.md`: these read and
  write `docs/todo.md` and `docs/known-issues.md`, name files
  `YYYY-MM-DD-NNN-slug.md`, and assume one checkout. `ox-plan` step 9 has the
  spare-ports `make dev` recipe.
- `.claude/skills/README.md`: diagrams of known issues, of the audit's
  evidence, and of an orchestrator that "commits".
- `.githooks/pre-commit`: runs `scripts/metrics.py`, stages `docs/metrics.md`,
  and then runs `dprint check`.
- `scripts/metrics.py`: snapshots the index, counts the `## ` headings in
  `docs/known-issues.md`, and writes `docs/metrics.md` through `dprint fmt`.
- `Makefile`: the `metrics` and `hooks` targets. It has no worktree target.
- `cmd/ox/web/dev.mjs`: runs `bin/oxd`, `bin/ox -addr localhost:$OX_RELAY_PORT`
  and Vite, with the inherited environment. `vite.config.ts` reads
  `OX_VITE_PORT` and `OX_RELAY_PORT`, with `strictPort`.
- `cmd/ox/main.go:29`: `tailnetPort` 17840 is bound whenever the configuration
  sets `tailscale_ip`, so two stacks sharing the user's configuration collide
  there.
- `internal/paths/paths.go`: configuration from `XDG_CONFIG_HOME`; the
  database, Ur directory and socket from `XDG_STATE_HOME`, or the socket from
  `XDG_RUNTIME_DIR` when that is set.
- `cmd/ox/web/playwright.config.ts`: e2e uses `channel: "chrome"`.
  `e2e/global-setup.ts` builds the binaries and the page itself.
- `go.mod`: `go 1.27.1`. Development uses Node 26.

## Decisions

- **Requirements:** **File locations** holds. Ox still reads the XDG
  variables, and a worktree's `make dev` sets them. No requirement changes,
  since the change is to the development process and Ox behaves the same.
- **Trust boundaries:** A worktree's dev stack listens on `localhost` only, on
  ports the OS picks, and keeps every file under `<checkout>/.state`. It never
  touches `~/.config/ox` or `~/.local/state/ox`.
- **Construction:** A change's slug is its single name. It is the branch name,
  the worktree directory `../ox-<slug>`, and the name of its plan, review and
  audit files, `agents/<kind>/YYYY-MM-DD-<slug>.md`. Only `make worktree`
  derives the directory and branch from it. Existing files keep their names.
- **One commit per branch.** Review runs on the uncommitted change in the
  worktree. Once the verdict is Ready to commit, the top-level agent commits
  once. The base revision for `/ox-work`'s proof and `/ox-review`'s scope is
  `git merge-base origin/main HEAD`, which is the same commit before and after
  the commit.
- **Squash merges keep the agent's commit message.** The repository allows
  squash merges only, with `squash_merge_commit_title=COMMIT_OR_PR_TITLE` and
  `squash_merge_commit_message=COMMIT_MESSAGES`. A single-commit PR lands with
  its commit's subject and body, so `AGENTS.md`'s rule on commit bodies still
  holds. The PR body holds the links, the verdict, the checks, and `Fixes #N`.
- **Branches are cut without tracking.** The command is
  `git worktree add ../ox-<slug> -b <slug> --no-track origin/main`. A branch
  that tracked `origin/main` would make a plain `git push` aim at `main`. The
  first push is `git push -u origin <slug>`.
- **Labels:** GitHub's built-in labels, by kind of work. `bug` marks every
  defect, wherever it was found: a review's Record finding, an audit's Plan
  finding, or a report. `enhancement` marks planned features. A Record
  finding's body adds **Worst cost**, **Found** and **Waits on**. An audit
  finding's body links the finding's section. No new labels.
- **Who opens and closes issues:**
  - `/ox-work fix` opens a `bug` issue for each Record finding, where
    it now adds a file entry. `/ox-review verify` checks that the issue exists.
  - `/ox-audit` opens a `bug` issue for each Plan finding. It links
    `https://github.com/kkestell/ox/blob/main/agents/audits/<file>#<section>`,
    which resolves once the audit's PR merges.
  - `/ox-audit` closes the issues of earlier findings that have landed, and
    edits stale ones.
  - A plan's Goal links the issue it resolves. The top-level agent puts
    `Fixes #N` in the PR body, and GitHub closes the issue on merge.
- **Metrics print on demand.** `make metrics` prints the report to stdout and
  writes no file. Committing `docs/metrics.md` makes every pair of PRs
  conflict. The pre-commit hook keeps only `dprint check`. The report's Known
  issues section goes, because there is no file left to count.
- **CI runs on `ubuntu-latest`.** Linux runners start faster and have more
  cores than macOS ones, and the image ships Chrome for `channel: "chrome"`.
  The job is named `check`, and branch protection requires that status.
- **golangci-lint is a downloaded release.** `make lint` runs the pinned
  release binary from `bin/golangci-lint-<version>`, which the Makefile
  downloads once from the project's GitHub releases, on every machine and in
  CI. `.DELETE_ON_ERROR` removes a partial download.
- **Worktree dev stacks are fully isolated.**
  - `dev.mjs` treats a checkout whose `.git` is a file as a linked worktree.
  - There it sets `XDG_CONFIG_HOME`, `XDG_STATE_HOME` and `XDG_RUNTIME_DIR` to
    `<checkout>/.state`.
  - It picks free ports for Vite and the relay by listening on port 0, then
    closing.
  - `OX_VITE_PORT` and `OX_RELAY_PORT` win when already set.
  - It prints the page's URL.
  - The stack runs on the built-in configuration, so it never binds the tailnet
    port the user's Ox holds.
  - The main checkout's stack is unchanged.
  - This replaces `ox-plan`'s spare-ports recipe with `make dev` in the
    worktree.
- **This change lands through the workflow it adds.** It is built in
  `../ox-pull-request-workflow`, and its PR is the first CI run. Branch
  protection names the `check` status, so it waits until that status has
  reported once.
- **In-flight worktrees.** `ox-wt-backend`, `ox-wt-sidebar` and
  `ox-wt-transcript` are left alone. When they rebase onto this change, they
  drop their `docs/todo.md` edits and cite the migrated issues.

## Check your assumptions

1. **A worktree's dev stack starts empty and runs on the built-in
   configuration.**
   - **Plan:** `make dev` in the main checkout keeps your sessions, your
     configuration and ports 8080/8081. `make dev` in a worktree starts with no
     workspaces or sessions and the built-in models, on free ports it prints,
     so several can run beside your everyday Ox.
   - **Alternative:** worktree stacks read your configuration file. They would
     get your models, but with a tailnet address set, a second stack fails to
     start because your Ox already holds that port.
2. **Planned work loses its priority order.**
   - **Plan:** issues are grouped by label and listed newest first, and you
     choose what to take up when you start a change.
   - **Alternative:** add priority labels or a GitHub Project board for an
     explicit order. Every new issue then needs a priority, and someone has to
     keep the order current.

## Test plan

- `dev.mjs` worktree isolation: no test reaches it. `stack.test.mjs` tests
  `runStack` given an environment. Check it by hand in the change's worktree:
  - `make dev` prints ports other than 8080/8081.
  - It creates `.state/ox/oxd.sock`.
  - The main checkout's stack keeps running.
  - The printed URL loads in headless Chrome.

  This is the `make dev` check `AGENTS.md` ("Verification") asks for.
- `make worktree NAME=wt-probe`:
  - It creates `../ox-wt-probe` on branch `wt-probe`.
  - The branch has no upstream: `git -C ../ox-wt-probe rev-parse @{u}` fails.
  - `.env` is linked into the new checkout.

  Remove the probe afterwards with
  `git worktree remove ../ox-wt-probe && git branch -D wt-probe`.
- `make metrics` prints the report and leaves `git status` clean.
- The pre-commit hook passes on this change's commit and stages nothing
  besides the commit's files.
- The PR's `check` run passes. It is the only test of `ci.yml`.

## Implementation plan

1. Done by the top-level agent: the worktree `../ox-pull-request-workflow` on
   branch `pull-request-workflow`, cut from local `main`, which is ahead of
   `origin/main`, with `.env` linked and this plan moved in. All remaining
   work happens in the worktree. The top-level agent rebases it onto `main`
   once the in-flight `wt-sidebar` and `wt-transcript` changes land, and
   pushes `main` to `origin` (with the user's confirmation) before the first
   PR.
2. `Makefile`: add `worktree` to the targets and `.PHONY`.
   - It fails without `NAME`.
   - It runs `git fetch origin` and
     `git worktree add ../ox-$(NAME) -b $(NAME) --no-track origin/main`.
   - It symlinks `$(CURDIR)/.env` into the new checkout when `.env` exists.

   Reword the `metrics` comment.
3. `cmd/ox/web/dev.mjs`: implement the worktree isolation from Decisions.
   - Set the variables on `process.env` before `runStack`, so oxd, ox and
     Vite inherit them.
   - Pass the relay port to `ox -addr` as now.

   `.gitignore`: add `/.state/`.
4. `scripts/metrics.py`:
   - Print the markdown instead of writing `OUTPUT`.
   - Drop the `dprint fmt` pass, the `OUTPUT` constant, and the known-issues
     count and its section.
   - Update the docstring and the report's opening line to
     "Printed by `make metrics` from the files in the index."
5. `.githooks/pre-commit`: reduce it to `dprint check` and update its comment.
   Delete `docs/metrics.md`.
6. Create `.github/workflows/ci.yml`. It runs on `pull_request` and on `push`
   to `main`, with one job, `check`, on `ubuntu-latest`. Use the current major
   version of each action. Steps:
   - checkout
   - `setup-go` with `go-version-file: go.mod`
   - `setup-node` with Node 26 and npm caching on
     `cmd/ox/web/package-lock.json`
   - `dprint/check`
   - `make test`
   - `make e2e`
   - on failure, upload `cmd/ox/web/test-results`
7. `AGENTS.md`:
   - **Source Map:**
     - Drop `known-issues.md`, `metrics.md` and `todo.md`.
     - Add `.github/workflows/ci.yml` ("PR checks").
     - The pre-commit comment becomes "checks docs formatting".
     - `metrics.py`'s comment becomes "prints size and complexity".
   - **Defects:**
     - **Record** opens a GitHub issue labeled `bug` that states the
       defect's worst cost, how it was found, and what it waits on.
     - A review reads the open `bug` issues first, and reports a
       recorded defect again only when what it waits on has changed.
     - An issue is closed once its defect is fixed or shown not to exist.
   - **Workflow, opening paragraph.** Replace it:
     - The main checkout stays on `main` and only pulls.
     - Each change starts with `make worktree NAME=<slug>` from a clean main
       checkout, and every step runs in that worktree.
     - A child session that finds uncommitted work outside its brief stops and
       reports it to the session that started it.
     - Changes reach `main` only by pull request, which the user merges.
   - **Workflow, step 7.** The top-level agent:
     - sanity-checks the result, including the requirements diff;
     - commits the change with its plan and review;
     - runs `git fetch` and `git rebase origin/main`, then re-runs `make test`
       and `make e2e`. When the rebase resolved a conflict in code, the
       reviewing session also runs `/ox-review verify` on the rebased commit
       before the push;
     - pushes with `git push -u origin <slug>`;
     - opens the PR with `gh pr create`. The title is the commit subject. The
       body links the plan and review, states the verdict, lists the checks
       run (with `make live` run or not run), and adds `Fixes #N` for each
       issue the change resolves.
   - **Workflow, new step 8.** After the user merges, the top-level agent
     removes the worktree and local branch, then pulls in the main checkout.
   - **Workflow, audit paragraph.** Replace it:
     - Every audit finding marked to plan becomes an issue labeled `bug`
       that links its section.
     - The audit lands through its own PR.
     - The PR that fixes a finding closes its issue.
     - An audit or review that finds an issue stale corrects or closes it.
   - **Verification.** The `make dev` check runs `make dev` in the change's
     worktree.
   - **Documentation:**
     - Drop the `known-issues.md`, `todo.md` and `metrics.md` entries.
     - Add a line saying that planned work and defects live in the
       repository's GitHub issues, labeled `enhancement` and `bug`.
8. `.claude/skills/ox-plan`:
   - Description and argument-hint: "issue" replaces "todo item, known issue".
   - Step 1 reads a named issue with `gh issue view`.
   - Step 2 reads the open `bug` issues.
   - Step 5: one change lands as one PR.
   - Step 9: the recipe becomes `make dev` in the worktree.
   - Step 10: the file name is `YYYY-MM-DD-<slug>.md`, where the slug is the
     branch name.
   - Step 14: drop the todo removal. The Goal links the issue the change
     resolves.
   - Final response example: the plan path changes to match.
   - Template Goal: "Link the issue it resolves, or the audit finding it comes
     from, if any."
9. `.claude/skills/ox-work`:
   - Step 1: the worktree holds only this change.
   - Step 2: known issues as in ox-plan.
   - Step 6: each Record finding becomes `gh issue create --label bug`,
     and its number goes in the Findings line.
   - Step 7: the base revision is `git merge-base origin/main HEAD`, checked
     out detached in a temporary worktree.
10. `.claude/skills/ox-review`:
    - Description and step 2: add a pull request as a target, for PRs that did
      not come through the workflow, such as another agent's or a
      contributor's. Read it with
      `gh pr view` and `gh pr diff`, and review it in its worktree or in one
      made with `gh pr checkout`. The base revision is the merge-base with
      `origin/main`.
    - Objective's Record: "The implementer opens a `bug` issue."
    - Steps 3 and 7 read the open `bug` issues.
    - Step 11: the file name as in ox-plan.
    - Step 15: a Record finding is resolved by its issue.
    - Template Action: "For `Record`, the `bug` issue to open: worst
      cost, how it was confirmed, and what it waits on."
11. `.claude/skills/ox-audit`:
    - Objective: edit only the audit file, and open, edit and close `bug`
      issues.
    - Step 2: known issues as above.
    - Step 3: read the earlier audits and the `bug` issues, open and closed.
    - Step 4: evidence takes in the Record findings, the open `bug` issues,
      and the merged PRs in scope (`gh pr list --state merged`).
    - Step 10: the file name as above.
    - Step 12: one `bug` issue per Plan finding (a title naming the defect,
      one sentence, the section link). Close those that landed and edit stale
      ones.
    - Final response: "Still open" counts open `bug` issues.
    - Template Defects: "review finding, issue, or commit".
12. `.claude/skills/README.md`:
    - The orchestrator "opens the PR" instead of committing.
    - In the requirements diagram, the requirements diff is checked "before
      opening the PR".
    - The Known issues section and its `KI` nodes describe issues labeled
      `bug` instead of the file.
13. Done by the top-level agent after the rebase. Before each group of commands, show the user the exact commands and get
    confirmation. These steps reach outside the repository.
    1. Open one issue per `docs/todo.md` item, labeled `enhancement`, with the
       item's bold title as the title and its text as the body.
    2. Open one issue per `docs/known-issues.md` entry, labeled `bug`,
       with its heading as the title and its paragraph and bullets as the body.
    3. In both, rewrite relative doc links as
       `https://github.com/kkestell/ox/blob/main/docs/...`.

    Then delete both files, and confirm `rg 'todo\.md|known-issues'` finds
    nothing outside `agents/`.
14. Run `make docs` and the checks in the Test plan. The top-level agent then
    follows the new step 7 to open the PR, and waits for `check` to pass.
15. After the user merges, and with confirmation:
    1. Allow squash merges only, and delete head branches on merge:
       `gh repo edit --enable-squash-merge --enable-merge-commit=false --enable-rebase-merge=false --delete-branch-on-merge`
    2. Keep the commit's message on squash:
       `gh api -X PATCH repos/kkestell/ox -f squash_merge_commit_title=COMMIT_OR_PR_TITLE -f squash_merge_commit_message=COMMIT_MESSAGES`
    3. Protect `main` with `gh api -X PUT repos/kkestell/ox/branches/main/protection`:
       - `required_status_checks: {strict: true, contexts: ["check"]}`
       - `required_pull_request_reviews: {required_approving_review_count: 0}`
       - `required_linear_history: true`
       - `enforce_admins: true`
       - `restrictions: null`

## Requirement changes

None.

## Documentation updates

- `AGENTS.md`: as in task 7.
- `docs/architecture.md` ("Web UI"): add that `make dev` in a linked worktree
  keeps its configuration, state and socket under the checkout's `.state` and
  serves on free ports.
