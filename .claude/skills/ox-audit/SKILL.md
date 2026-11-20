---
name: ox-audit
description: Audit Ox for structures that let classes of defects happen, such as a state the types allow but the design forbids, one fact kept in two places, or a rule several callers decide for themselves. Draws on past plans, reviews, fixes, and known issues as evidence, confirms each structure in the code, and writes the audit in `.agents/audits/`, marking each finding to plan or rejected with its reason. Fixes nothing. Each finding to plan becomes the request for `/ox-plan`. Never use it unless the user, or an agent acting on the user's request, invokes it.
argument-hint: "[scope: whole repository, an area, or since a revision or earlier audit]"
---

Use this skill only when the user, or an agent acting on the user's request, invokes it. Never use it on your own initiative.

## Objective

Find the structures in Ox that let defects happen, and write an audit that tells the user which are worth changing. A structure is worth changing when keeping it costs more than replacing it: the defects it has caused, the defects it still permits, and the checks, guards, and syncing it forces on every reader. Give every finding one action:

- `Plan`: a confirmed structure whose replacement costs less than keeping it. The finding becomes the request for `/ox-plan`.
- `Rejected`: speculative, a misreading of the intended design, or cheaper to keep than to replace. The reason is recorded so the next audit does not raise it again.

Change no code, tests, or documentation, and stage or commit nothing. Edit only the audit file. Do not start other agents.

## Gather the evidence

1. Read `<audit_scope> $ARGUMENTS </audit_scope>`. With no scope, audit the whole repository since the last audit in `.agents/audits/`, or all history if there is none. Record the revision and the state of the working tree you audit.
2. Read `AGENTS.md`, `docs/functional-requirements.md`, `docs/known-issues.md`, and `docs/architecture.md`; for web client code, also `cmd/ox/web/AGENTS.md`. Read the sections of `docs/protocol.md` and `docs/claude-code.md` that cover the code in scope.
3. Read the earlier audits in `.agents/audits/`. Note which `Plan` findings have landed, and do not raise a `Rejected` finding again unless the evidence its reason rests on has changed.
4. Collect the defects in scope: the `Fix` and `Record` findings in `.agents/reviews/`, the entries in `docs/known-issues.md`, and the commits that fix behavior, found through `git log` and their diffs. For each, note what went wrong, where, and what the fix changed: the structure that allowed it, or a guard, special case, or retry at the symptom.

## Find the structures

5. Group the defects by the cause they share, across features, files, and components. Two defects with one cause, a fix that guarded a symptom, and a fix that had to touch several places to keep them in agreement each point at a structure. For each group, ask why the code could reach the wrong state at all.
6. Read the code in scope for structures no defect has exposed yet. Ask of each piece of state what keeps it consistent with everything that depends on it, and of each rule who decides it. The structures `AGENTS.md` ("Correct by construction") names are the common ones; also look for logic written twice that must agree, state copied across a boundary and kept in sync, one concept with several names or representations, an abstraction its callers work around, special cases gathering at one place, and checks for states the design says cannot exist. Code that looks alike but need not agree is not a finding.
7. For each candidate, establish:
   - The structure, and every place it appears in the code.
   - The wrong states it lets the code build, which of them have happened, with the defects that showed them, and which are reachable now.
   - The structure that would make those states impossible to build. Treat the documented design as earlier choices, open to question: for each piece of state involved, list every component the docs or code say owns, holds, tracks, or decides it, find which component observes the events that change it, and ask which single owner or representation would leave no wrong state to build. Name that owner even when the docs name another.
   - What the replacement removes: the guards, syncing, and special cases that exist only because of the structure.
8. Confirm each candidate by tracing the code, reproducing a wrong state, or running a focused check: a stand-in `claude` script, an injected `/rpc` delay or failure in an e2e spec, or a probe against built binaries on a temporary socket, database, and port. Revert every edit a check needs before you finish. A candidate with no defect behind it and no reachable wrong state is `Rejected` as speculative.
9. Give each confirmed structure its action, weighing the cost of keeping it against the cost of replacing it: the code and tests the change touches, the risk to behavior users rely on, and whether it changes anything the user can observe. Prefer the replacement that deletes the most code. Whether a replacement would change what the user sees is for `/ox-plan` to settle with the user; note it, and do not ask.

## Write the audit

10. Read this skill's `assets/_template.md` and write the audit in `.agents/audits/`, named `YYYY-MM-DD-NNN-slug.md` with the next sequence for the day.
11. Make each `Plan` finding stand on its own as a request for `/ox-plan`: the structure, every place it appears, the defects it caused, the wrong states it still permits, the owner or representation that replaces it, and what the replacement removes. Leave the design of the change to the plan. For each `Rejected` finding, state the reason.
12. Give the final response and stop.

## Final response

Reply in the form below and nothing else. Lead with the main point, write plainly, and leave out the candidates you ruled out and the steps you took.

```markdown
One or two sentences: what the audit found, and the number of findings by action.

| ID | Cost   | Action   | Title                                      | Places |
| -- | ------ | -------- | ------------------------------------------ | ------ |
| F1 | High   | Plan     | Session state copied into each connection  | 4      |
| F2 | Low    | Rejected | Two similar argument parsers               | 2      |

**Audit:** `.agents/audits/YYYY-MM-DD-NNN-slug.md`

**Next:** `/ox-plan .agents/audits/YYYY-MM-DD-NNN-slug.md F1`
```

List every finding, `Plan` findings first, each group by cost, highest first; if there are none, replace the table with `No findings.` and omit Next. Places is the number of places the structure appears. Give a Next line for the highest-cost `Plan` finding.
