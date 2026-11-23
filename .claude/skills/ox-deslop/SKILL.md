---
name: ox-deslop
description: Audit Ox for dead code, duplication, broken guarantees, over-engineering, hollow tests, and comments or docs that contradict the code. Splits the repository into areas reviewed concurrently in child sessions, confirms and groups their findings, and writes the audit in `agents/audits/`, marking each finding to plan, to record, or rejected with its reason. Fixes nothing. Never use it unless the user, or an agent acting on the user's request, invokes it.
argument-hint: "[scope: whole repository, an area, or since a revision or earlier audit]"
---

Use this skill only when the user, or an agent acting on the user's request, invokes it. Never use it on your own initiative.

## Objective

Find the defects and unnecessary complexity in Ox that are worth a change, and write an audit that tells the user which to take through the [project workflow](../../../AGENTS.md#workflow). Child sessions gather candidates. You confirm them, decide which share a cause, and give each one action, as `AGENTS.md` ("Defects") defines the outcomes:

- `Plan`: a confirmed defect, or a design, test, or documentation problem whose fix costs less than keeping it.
- `Record`: a confirmed defect that cannot be fixed yet, because it needs reproducing or depends on Claude Code behavior Ox cannot change.
- `Rejected`: speculative, a misreading of the intended behavior, or cheaper to keep than to fix. The reason is recorded so the next audit does not raise it again.

Audit the code as it is, not a diff, and judge it by its behavior and evidence, not by who wrote it. `/ox-audit` looks for the structures behind classes of defects; this audit looks for the defects and the slop themselves. When several findings share a structure, say so in the finding.

Change no code, tests, or documentation, and stage or commit nothing. Edit only the audit file, and open, edit, and close `bug` issues for its findings. Start other agents only as [Child sessions](#child-sessions) describes.

## Gather the evidence

1. Run `git status`. If the checkout has uncommitted changes, stop and report them to whoever asked for the audit, as `AGENTS.md` ("Workflow") describes. Then read `<audit_scope> $ARGUMENTS </audit_scope>`. With no scope, audit the whole repository, with the revision of the last deslop audit in `agents/audits/` as the baseline, or all history if there is none. Record the revision, the baseline, and the state of the working tree you audit.
2. Read `AGENTS.md`, `docs/functional-requirements.md`, `docs/architecture.md`, `docs/protocol.md`, `docs/harnesses/claude-code.md`, and `cmd/ox/web/AGENTS.md`.
3. Read the earlier audits in `agents/audits/`, the reviews in `agents/reviews/` since the baseline, and the `bug` issues, open and closed (`gh issue list --label bug --state all`). Do not raise a finding an earlier audit or review rejected unless the evidence its reason rests on has changed. Skip a defect that an open `bug` issue records, unless what the issue waits on has changed. When a later fix is cited as something an earlier audit missed, check whether the defect existed at that audit's revision, and use a confirmed miss to brief the area that owns it.
4. Inventory the hand-written source, tests, docs, scripts, and configuration. Skip `bin/`, `node_modules/`, `cmd/ox/web/dist/`, lockfiles, and generated files. Treat `agents/` as working records: read them as evidence, and do not audit them against the code.

## Carve up the repository

5. Split the scope into at most 10 areas along real boundaries, keeping tests with the code they exercise and each document with the code whose facts it owns. Give every in-scope file an owner. A whole-repository audit usually splits like this:
   1. Claude harness: `internal/claude`, `docs/harnesses/claude-code.md`
   2. Sessions and lifecycle: `session.go`, `server.go`, `history.go`, `conn.go`, `internal/agent`, `internal/fake`, `live_test.go`
   3. RPC methods and protocol: `methods.go`, `internal/jsonrpc`, `docs/protocol.md`
   4. MCP: `mcp.go`, `cmd/ox-mcp`
   5. Daemon, configuration, and store: `cmd/oxd`, `internal/config`, `internal/store`, `internal/paths`, `docs/architecture.md`
   6. Ur: `internal/ur`
   7. Relay and tooling: `cmd/ox/main.go`, the e2e services, `Makefile`, `scripts/`, `.githooks`, `.github`, `.claude/skills`, `AGENTS.md`
   8. Web state: the connection, protocol, and store modules, `App.tsx`, `Sidebar.tsx`, and their specs
   9. Web session view: the session, approval, and new-session views and their specs, `cmd/ox/web/AGENTS.md`
   10. Workspace files: `files.go`, the file tree and file view, `docs/functional-requirements.md`
6. Name the workflows that cross areas, such as a prompt from the composer to Claude and back, an approval, a park and wake, or a settings change, and give each to one child, to follow from its entry point to the outcome the user sees. Group related workflows in one child where practical.
7. If the scope does not fit in 10 children, cover the areas whose failures cost the user most, and record what is left uncovered. Never imply a partial audit covered the whole repository.

## Child sessions

8. Give each child its own detached worktree at the audited revision, made with `git worktree add --detach "$TMPDIR/ox-deslop/<area>" HEAD`, with `cmd/ox/web/node_modules` linked from this checkout, so the children's builds, tests, and probes cannot collide. Remove the worktrees once the children have answered.
9. Start one child per area and run them concurrently: an Ox session started with `create_session`, or another agent the user names, such as a Codex task on Sol. Brief each one in full; a child does not inherit this skill or your conversation. Give it:
   - Its worktree's absolute path and revision, and two sentences on what Ox is.
   - The files to read first: `AGENTS.md`, `docs/functional-requirements.md`, the sections of `docs/architecture.md`, `docs/protocol.md`, and `docs/harnesses/claude-code.md` its area touches, and `cmd/ox/web/AGENTS.md` for web client code.
   - Its files and workflows, their entry points, the open `bug` issues and earlier rejections in its area, and any leads worth checking. A lead is a suspicion, not a finding to prove.
   - The checks it can run. A child in a sandbox without network or sockets runs `go test -race ./<pkg>` and `node --test <file>` directly, since `make test` downloads golangci-lint, and reports browser, socket, and live checks as gaps.
   - The [lenses](#lenses), the [child rules](#child-rules), and the [child report](#child-report), copied in full.
10. End your turn until every child has answered.

### Lenses

Each child covers every lens in its area.

1. **Dead code:** unused functions, exports, branches, files, settings, environment variables, flags, Makefile targets, protocol fields, store columns, and CSS rules. Check every caller, the web client, the tests, and the configuration before calling something unused.
2. **Duplication:** logic written twice, near-copies that have drifted, one fact kept in two places, or tools and scripts that do the same job. Similar code alone is not a finding; show the syncing it forces or the behavior that already differs.
3. **Broken guarantees:** what a caller, the page, or the user is entitled to expect, traced through the code that delivers it. Cite the functional requirement by title when one applies, and report plain bugs when none does. At a handoff between `oxd`, the relay, the page, and Claude Code, follow what each side receives, when the action takes effect, and what the user sees on success, failure, a lost reply, a reconnect, and a parked or exited process. Follow paths that normal use, the code, or past defects reach.
4. **Over-engineering:** what `AGENTS.md` ("Implementation") rules out: speculative abstractions, options, compatibility layers, automatic recovery, retries, and fallbacks no current requirement needs; checks for states the design says cannot exist; indirection that only forwards calls. One caller alone is not proof; say what can be removed and what behavior stays.
5. **Hollow tests:** a test whose path or assertion does not reach the behavior it claims, as `AGENTS.md` ("Verification") requires: a test that passes without the behavior, a test of internals instead of the public interface, a test that hands a component the value its real caller would get wrong, or a live or e2e check that waits for an earlier event than the one it claims. Say which failure would still pass.
6. **Comments and docs against the code:** what `AGENTS.md` ("Documentation") rules out: a wrong or stale comment or document, a fact stated outside the document that owns it, history or correction narrative, references to plans or issues, and comments that restate the code. Check operational instructions, such as Makefile targets and the steps in `AGENTS.md`, by what they do. Say what a reader relying on the text would get wrong.

### Child rules

- Change no files except temporary ones you remove before you answer, and make no git writes. Fix nothing, however small.
- Confirm a conclusion that turns on runtime behavior with the smallest check that reaches it: an existing test, a stand-in `claude` script, an injected `/rpc` delay or failure in an e2e spec, a probe against built binaries on a temporary socket, database, and port, or a small live check against Claude Code with Haiku. Keep test state and output out of the checkout. When a check cannot run, say what stays unconfirmed.
- Ignore formatting and naming preferences. Follow your workflows across area boundaries, and send leads in other areas to the parent in your report.
- Make one pass, and answer after about 40 minutes or 60 tool calls, whichever comes first, with the coverage you reached.
- Reply with `send_message`, or in your final message if you cannot. Start no sessions of your own, and propose no action for a finding.

### Child report

For each candidate, give its lens, its exact locations, and the callers and tests you checked. For a behavior, give the trigger normal use reaches, the owner of the state involved, the outcome, and the requirement it breaks. For complexity, say what a reader has to reason about or keep in sync and what could be removed. Say whether each candidate was observed live, reproduced, or traced in the source. List unconfirmed leads separately. End with leads for other areas, the coverage you reached, its gaps, and the checks you ran. A pass may find nothing.

## Confirm and weigh the findings

11. Confirm every candidate yourself against the current source, its callers, and its tests, including the guards and recovery around it. Follow each workflow from its entry point to the outcome the user sees; where two children stopped at the same interface, follow the handoff yourself or record the gap. Revert every edit a check needs before you finish. Leave out what you cannot confirm.
12. Group candidates that one change at one cause would correct into one finding, across files and areas, and keep failures that need independent changes apart. When a fix moved a responsibility, check who owns it before, during, and after the move.
13. Give each finding its severity, origin, and action:
    - **Severity**, as the template defines it.
    - **Origin**, relative to the baseline: introduced, worsened, or present at the baseline. Check the code at the baseline before you say. Say when the origin is unknown, or when a finding groups old and new symptoms.
    - **Action**, as the Objective defines it, weighing the cost of keeping the problem against the cost of the change. Leave out an observation whose only case is that someone may touch the file later. Whether the change would alter what the user sees is for `/ox-plan` to settle with the user; note it, and do not ask.
14. Before you write, check whether the audited files changed during the audit. Recheck the findings they affect, or record the gap. Report no stale line numbers.

## Write the audit

15. Read this skill's `assets/_template.md` and write the audit in `agents/audits/`, named `YYYY-MM-DD-<slug>.md`, where the slug is the branch name.
16. Make each `Plan` finding complete enough to plan from: the problem, every place it appears, the evidence, the consequence, and the change at the cause. Record a candidate as a `Rejected` finding only when a later audit or review would otherwise raise it again, and state the reason.
17. Group the `Plan` findings into changes, each one pull request through the project workflow: findings whose fixes share an owner or the same files form one change, and small unrelated fixes share one cleanup change. A finding gets its own change only when its fix is large enough to earn a plan, work, and review cycle. List the changes in the audit's Changes section, highest-severity first, with the one to take up first.
18. Open one issue labeled `bug` for each change and each `Record` finding, with `gh issue create --label bug`: a title naming the defect, one sentence on what goes wrong, and a link to each finding's section, `https://github.com/kkestell/ox/blob/main/agents/audits/<file>#<section>`, which resolves once the audit's pull request merges. A `Record` issue also states the defect's worst cost, how it was found, and what it waits on. Close the issue of an earlier finding that has landed or that this audit shows does not exist, and edit one this audit shows is stale.
19. Give the final response and stop.

## Final response

Reply in the form below and nothing else. Lead with the main point, write plainly, and leave out the candidates you ruled out without recording them and the steps you took.

```markdown
One or two sentences: what the audit found, and the number of findings by severity and action.

| ID | Severity | Lens              | Action   | Title                                  | Location                    |
| -- | -------- | ----------------- | -------- | -------------------------------------- | --------------------------- |
| F1 | High     | Broken guarantees | Plan     | Reconnect sends the first prompt again | `cmd/ox/web/src/ox.tsx:42`  |
| F2 | Low      | Over-engineering  | Rejected | Retry for a failure that cannot occur  | `internal/server/conn.go:8` |

**Audit:** `agents/audits/YYYY-MM-DD-<slug>.md`

**Changes:** each change, its findings, and its issue.

**Not covered:** the areas or files left out, and why.

**Recommended finding** F1, Reconnect sends the first prompt again.
```

List every finding, `Plan` and `Record` findings first, each group by severity, highest first. If there are no findings, replace the table with `No findings warrant a change.` Omit Not covered when the audit covered its whole scope. Recommend the highest-severity `Plan` finding by ID and title when there is one.
