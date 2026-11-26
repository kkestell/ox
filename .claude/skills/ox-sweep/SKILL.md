---
name: ox-sweep
description: Sweep the whole Ox codebase for defects in behavior, checking every functional requirement, every workflow from the page to Claude Code and back through reconnects, parking, and concurrent requests, every input Ox reads from outside its code, and the tests, docs, and dead code that misstate or hide behavior. Fans out to child sessions by workflow, by input source, and over the tests and docs, confirms and groups their findings, and writes the sweep in `agents/audits/`, marking each finding to plan, to record, or rejected with its reason. Fixes nothing. Never use it unless the user, or an agent acting on the user's request, invokes it.
argument-hint: "[scope: whole repository, or named workflows or inputs]"
---

Use this skill only when the user, or an agent acting on the user's request, invokes it. Never use it on your own initiative.

## Objective

Find the defects in Ox's behavior, and write a sweep that tells the user which to take through the [project workflow](../../../AGENTS.md#workflow). `/ox-review` checks one change; this sweep checks the code as it is, against every requirement in `docs/functional-requirements.md`. `/ox-audit` looks for the structures behind classes of defects; this sweep looks for the defects themselves, and for the tests that would not catch them, the documents that misstate them, and the code nothing reaches. Child sessions gather candidates. You confirm them, decide which share a cause, and give each one action, as `AGENTS.md` ("Defects") defines the outcomes:

- `Plan`: a confirmed defect, or a test, document, or piece of dead code, whose fix costs less than keeping it.
- `Record`: a confirmed defect that cannot be fixed yet, because it needs reproducing or depends on Claude Code behavior Ox cannot change.
- `Rejected`: speculative, a misreading of the intended behavior, or cheaper to keep than to fix. The reason is recorded so the next sweep does not raise it again.

Correctness matters for what happens in normal use and for the requirements in `docs/functional-requirements.md`. Handling every case that could occur does not.

Change no code, tests, or documentation. Edit and commit only the sweep file, push it to `main`, and open, edit, and close `bug` issues for its findings. Start other agents only as [Child sessions](#child-sessions) describes.

## Gather the evidence

1. Run `git status`. If the checkout has uncommitted changes, stop and report them to whoever asked for the sweep, as `AGENTS.md` ("Workflow") describes. Then read `<sweep_scope> $ARGUMENTS </sweep_scope>`. With no scope, sweep the whole repository. Record the revision and the state of the working tree you sweep.
2. Read `AGENTS.md`, `docs/functional-requirements.md`, `docs/architecture.md`, `docs/protocol.md`, `docs/harnesses/claude-code.md`, and `cmd/ox/web/AGENTS.md`.
3. Read the earlier sweeps and audits in `agents/audits/` and the `bug` issues, open and closed (`gh issue list --label bug --state all`). Do not raise a finding an earlier sweep, audit, or review rejected unless the evidence its reason rests on has changed. Skip a defect that an open `bug` issue records, unless what the issue waits on has changed.
4. Collect the defects fixed since the last sweep, or in all history if there is none: the closed `bug` issues, the `Fix` and `Record` findings in `agents/reviews/`, and the commits that fix behavior. Each one is a lead for the child whose workflow or input it touched, since the same failure often recurs on a neighboring path.

## Carve up the code

5. Split the sweep into workflow children, input children, and one tests and docs child, at most 13 together. A workflow child follows its workflows from their entry point on the page to the outcome the user sees, through the relay, `oxd`, and Claude Code or Ur, and owns the requirements those workflows deliver. An input child checks one source of input everywhere Ox reads it. The tests and docs child reads every test, document, and comment, and looks for code nothing reaches. A whole-repository sweep usually splits like this:
   1. Prompts and transcript: a prompt from the composer to Claude and back, stop, and attachments. **Sending prompts**, **Transcript**.
   2. Settings: a settings change on a running, idle, and parked session. **Settings**, **Configuration**.
   3. Approvals: an approval and a question, answered, refused, and left open across a reconnect. **Approvals**.
   4. Sessions working together: child sessions, messages, and child reports. **Sessions working together**.
   5. Session lifecycle: create, resume, park and wake, archive, and delete. **Creating sessions**, **Persistence and parking**, **Archiving and deleting**.
   6. Pages and connection: reconnect, a relay 503, two pages on one session, the session list, and the usage readout. **Pages and connection**, **Session list**, **Layout**.
   7. Workspaces and files: workspaces, worktrees, the file tree, the file view, and the changes view. **Workspaces**, **Files**.
   8. Daemon and Ur: `oxd` startup and shutdown, and an Ur turn with shell approval. **oxd**, **Ur**, **Claude Code**.
   9. Claude Code output: its stream-json messages, control requests and responses, hooks, exit, and stderr, and the flags Ox passes, against `docs/harnesses/claude-code.md`.
   10. Git and the filesystem: worktree and diff commands, file reads, paths, symlinks, sizes at stated limits, and the user's Git settings.
   11. Requests from outside: listeners, the relay, page requests, and the MCP forwarder, including malformed, out-of-order, and cross-origin requests.
   12. Configuration, environment, store, and OpenRouter: configuration files, the inherited environment, the database across restarts, and OpenRouter's streams.
   13. Tests, docs, and dead code: the Go tests, the web client's unit and Playwright specs, `docs/`, `AGENTS.md`, `cmd/ox/web/AGENTS.md`, the skills, the `Makefile`, `scripts/`, and the comments throughout.
6. Assign every requirement in scope to exactly one workflow child: every requirement in `docs/functional-requirements.md` for a whole-repository sweep, and those the scope's workflows deliver for a narrower one. A requirement in scope that no workflow reaches is a gap to record.
7. If the scope does not fit in 13 children, cover the workflows and inputs whose failures cost the user most, and record what is left uncovered. Never imply a partial sweep covered the whole repository.

## Child sessions

8. Give each child its own detached worktree at the swept revision, made with `git worktree add --detach "${TMPDIR:-/tmp}/ox-sweep/<child>" HEAD`, with `cmd/ox/web/node_modules` linked from this checkout, so the children's builds, tests, and probes cannot collide. Remove the worktrees once the children have answered.
9. Start one child per workflow group and input source, and the tests and docs child, and run them concurrently: an Ox session started with `create_session` on this session's model and effort, or another agent the user names, such as a Codex task on Sol. Brief each one in full; a child does not inherit this skill or your conversation. Give it:
   - Its worktree's absolute path and revision, and two sentences on what Ox is.
   - The files to read first: `AGENTS.md`, `docs/functional-requirements.md`, the sections of `docs/architecture.md`, `docs/protocol.md`, and `docs/harnesses/claude-code.md` its scope touches, and `cmd/ox/web/AGENTS.md` for web client code.
   - Its workflows and the requirements they deliver, by title, its input source and the code that reads it, or its tests and docs. Add the open `bug` issues and earlier rejections in its scope, and its leads from step 4. A lead is a suspicion, not a finding to prove.
   - The checks it can run. A child in a sandbox without network or sockets runs `go test -race ./<pkg>` and `node --test <file>` directly, since `make test` downloads golangci-lint, and reports browser, socket, and live checks as gaps.
   - Its concern from [Concerns](#concerns), the [child rules](#child-rules), and the [child report](#child-report), copied in full.
10. End your turn until every child has answered.

### Concerns

**Workflows.** For each workflow, list every asynchronous edge it crosses: a request and its reply, an event, a stream opening or closing, a timer, a process exit, a child report. For each edge, check every state it can arrive in: its request in flight, its reply lost, a reconnect, navigation away and back, a second page acting on the same session, the process exiting or parking, and `oxd` running independent requests in any order. Check that each requirement assigned to you holds through all of them, and that:

- Text the user typed is never lost or sent twice.
- The page offers an action unless the state it has from `oxd` rules it out, shows its result as soon as the user takes it, and returns to `oxd`'s state with the error if `oxd` refuses it.
- Each wait on a reply, a pipe, a process, or a child of Claude ends, by what it waits on or by a bound. Every request, stop, and shutdown gets a result, and each error is shown where its request was made.
- State is changed only by its owner: the session goroutine in `oxd`, the session's control on the page. A race that can leave state the design says cannot exist is a defect, however unlikely.

**Inputs.** For each place Ox reads your input source, list the forms the input takes in normal use, from the source's documentation, its observed behavior, and the settings a user may have, and check each one: the result Ox shows, the error it reports, the limit it keeps, and what it admits. Also check that:

- The Claude process runs with the model, effort, permission mode, and account the page shows, including after a partial failure, a restart, or with an inherited environment. A setting Claude would silently ignore or default is refused.
- Output is read to its last line, including one that ends at EOF; a process's exit is seen even while a child holds its pipes; a failure reason on stdout or stderr reaches the request that failed.
- Listeners, the relay, file paths, process environments, and files outside Ox's own directories admit only the user and touch only what Ox owns.

**Tests, docs, and dead code.**

- **Hollow tests:** a test whose path or assertion does not reach the behavior it claims, as `AGENTS.md` ("Verification") requires: a test that passes without the behavior, a test of internals instead of the public interface, a test that hands a component the value its real caller would get wrong, or a live or e2e check that waits for an earlier event than the one it claims. A Playwright spec that breaks the prohibitions in `cmd/ox/web/AGENTS.md` is one too. Say which failure would still pass.
- **Comments and docs against the code:** what `AGENTS.md` ("Documentation") rules out: a wrong or stale comment or document, a fact stated outside the document that owns it, history or correction narrative, references to plans or issues, and comments that restate the code. Check operational instructions, such as Makefile targets and the steps in `AGENTS.md`, by what they do. Say what a reader relying on the text would get wrong.
- **Dead code:** unused functions, exports, branches, files, settings, environment variables, flags, Makefile targets, protocol fields, store columns, and CSS rules that the linters in `make lint` do not report. Check every caller, the web client, the tests, and the configuration before calling something unused, and say what can be removed and what behavior stays.

### Child rules

- Change no files except temporary ones you remove before you answer, and make no git writes. Fix nothing, however small.
- Confirm a conclusion that turns on runtime behavior with the smallest check that reaches it: an existing test, a stand-in `claude` script, an injected `/rpc` delay or failure in an e2e spec, a probe against built binaries on a temporary socket, database, and port, or a small live check against Claude Code with Haiku. Keep test state and output out of the checkout. When a check cannot run, say what stays unconfirmed.
- Report missing handling only when a requirement or a failure normal use reaches needs it.
- Follow your workflows across component boundaries, and send leads outside your scope to the parent in your report.
- Make one pass, and answer after about 40 minutes or 60 tool calls, whichever comes first, with the coverage you reached.
- Reply with `send_message`, or in your final message if you cannot. Start no sessions of your own, and propose no action for a finding.

### Child report

For a test, document, or dead code, give its exact locations, the callers and tests you checked, and what its lens asks you to say. For any other candidate, give its exact locations, the trigger normal use reaches, the owner of the state involved, the outcome the user sees, and the requirement it breaks by title, or the plain bug when none applies. Say whether each candidate was observed live, reproduced, or traced in the source. List unconfirmed leads separately. End with the edges, input forms, or files you checked, each assigned requirement and whether it holds, leads for other children, the coverage you reached, its gaps, and the checks you ran. A pass may find nothing.

## Confirm and weigh the findings

11. Confirm every candidate yourself against the current source, its callers, and its tests, including the guards and recovery around it. Where two children stopped at the same interface, follow the handoff yourself or record the gap. Revert every edit a check needs before you finish. Leave out what you cannot confirm.
12. Check that every requirement in scope has a child's answer, and record each one without an answer as a gap.
13. Group candidates that one change at one cause would correct into one finding, across files and children, and keep failures that need independent changes apart.
14. Give each finding its severity, its origin, and its action:
    - **Severity**, as the template defines it.
    - **Origin**: the commit that introduced the defect, found with `git log -S` or `git blame`, and that change's review in `agents/reviews/` when there is one. Say when the origin is unknown.
    - **Action**, as the Objective defines it, weighing the cost of keeping the defect against the cost of the change. Whether the change would alter what the user sees is for `/ox-plan` to settle with the user; note it, and do not ask.
15. Before you write, check whether the swept files changed during the sweep. Recheck the findings they affect, or record the gap. Report no stale line numbers.

## Write the sweep

16. Read this skill's `assets/_template.md` and write the sweep in `agents/audits/`, named `YYYY-MM-DD-<slug>.md`, where the slug is the branch name.
17. Make each `Plan` finding complete enough to plan from: the defect, every place it appears, the evidence, the consequence, and the change at the cause. Record a candidate as a `Rejected` finding only when a later sweep or review would otherwise raise it again, and state the reason.
18. Group the `Plan` findings into changes, each one pull request through the project workflow: findings whose fixes share an owner or the same files form one change, and small unrelated fixes share one change. A finding gets its own change only when its fix is large enough to earn a plan, work, and review cycle. List the changes in the sweep's Changes section, highest-severity first, with the one to take up first.
19. Open one issue labeled `bug` for each change and each `Record` finding, with `gh issue create --label bug`: a title naming the defect, one sentence on what goes wrong, and a link to each finding's section, `https://github.com/kkestell/ox/blob/main/agents/audits/<file>#<section>`, which resolves once the sweep is pushed. A `Record` issue also states the defect's worst cost, how it was found, and what it waits on. Close the issue of an earlier finding that has landed or that this sweep shows does not exist, and edit one this sweep shows is stale.
20. Commit the sweep file alone, with the sweep's title as the subject. Run `git fetch` and `git rebase origin/main`, and push straight to `main` with `git push origin HEAD:main`.
21. Give the final response and stop.

## Final response

Reply in the form below and nothing else. Lead with the main point, write plainly, and leave out the candidates you ruled out without recording them and the steps you took.

```markdown
One or two sentences: what the sweep found, and the number of findings by severity and action.

| ID | Severity | Category     | Action   | Title                                  | Location                    |
| -- | -------- | ------------ | -------- | -------------------------------------- | --------------------------- |
| F1 | High     | Requirements | Plan     | Reconnect sends the first prompt again | `cmd/ox/web/src/ox.tsx:42`  |
| F2 | Low      | Settling     | Rejected | A stalled reader holds its connection  | `internal/server/conn.go:8` |

**Sweep:** `agents/audits/YYYY-MM-DD-<slug>.md`

**Commit:** https://github.com/kkestell/ox/commit/<sha>

**Changes:** each change, its findings, and its issue.

**Passed review:** each finding whose origin is a reviewed change, with the review.

**Not covered:** the workflows, inputs, or requirements left out, and why.

**Recommended finding** F1, Reconnect sends the first prompt again.
```

List every finding, `Plan` and `Record` findings first, each group by severity, highest first. If there are no findings, replace the table with `No findings warrant a change.` Omit Passed review when no finding came from a reviewed change, and Not covered when the sweep covered its whole scope. Recommend the highest-severity `Plan` finding by ID and title when there is one.
