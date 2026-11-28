---
name: ox-work
description: Implement a plan for Ox from `agents/plans/`, or fix the findings of a review from `agents/reviews/`, and leave the changes uncommitted for `/ox-review`. Use to carry out a plan or a review's fixes, not to plan or review. Never use it unless the user, or an agent acting on the user's request, invokes it.
argument-hint: "[plan path, or fix <review path>]"
---

Use this skill only when the user, or an agent acting on the user's request, invokes it. Never use it on your own initiative.

## Objective

Change Ox exactly as the plan or review says, and hand off changes whose checks pass, so that `/ox-review` finds nothing the implementer could have found. Leave the changes uncommitted. Edit no plan or review. Start other agents only as [Child sessions](#child-sessions) describes.

## Child sessions

When a plan splits into parts that edit disjoint files, and no part needs code another part writes, implement the parts concurrently in child sessions started with Ox's `create_session`, one part each. Examples are a server change and a web client change against a protocol the plan fixes, or independent fixes in separate packages. A split pays off only when each part is substantial.

Before you start the children:

- Write the shared pieces the parts depend on yourself, such as a type, a protocol field, or a helper. If you cannot, keep the work in this session.
- Brief each child with the plan's path, its steps from the Implementation plan and Test plan, and the files only it may edit.
- Tell each child to run only its own package's tests or its own spec, reply with `send_message` listing the files it changed, and start no sessions of its own. Tell each child that its siblings edit other files in the same checkout at once, and that their edits are not uncommitted work outside its brief.

Then end your turn until every child has answered.

Afterwards, read the children's changes as one change, apply the Requirement changes, and run the checks yourself on the final tree, as step 7 describes. Children do not run `make test`, `make e2e`, or `make live`. A run in a shared checkout sees the other children's unfinished edits, and the checks count only on the integrated tree.

Keep this work in this session:

- A plan whose steps build on each other.
- Parts that edit the same file.
- Edits to `docs/functional-requirements.md`.
- A review's fixes, which are usually small and need the review's context.

## Establish the work

1. Run `git status`. The change's worktree holds only this change: implementing a plan starts from one holding only that plan; fixing a review, from one holding only the reviewed change and its plan and review. If anything else is uncommitted, stop and report it to whoever started the work, as `AGENTS.md` ("Workflow") describes. Then read `<work> $ARGUMENTS </work>`. If it names neither a plan in `agents/plans/` nor a review in `agents/reviews/`, ask what to implement and stop. For a review, also read the plan it names.
2. Read `AGENTS.md`, `docs/functional-requirements.md`, and the open `bug` issues (`gh issue list --label bug`). For web client work, also read `cmd/ox/web/AGENTS.md`. Read the sections of `docs/architecture.md`, `docs/protocol.md`, and `docs/harnesses/claude-code.md` that cover the code the work touches.

## Implement a plan

3. Carry out the plan's Implementation plan in order, keeping its Decisions and its settled Check your assumptions entries. Write each test its Test plan names, in the test it names.
4. Apply its Requirement changes to `docs/functional-requirements.md` word for word, and change nothing else in that file. Make its Documentation updates, and correct any other document or comment the change makes wrong.
5. Settle what the plan leaves open, such as names, helpers, and where code goes, the way its Decisions would. When the code contradicts the plan, such as an owner, function, or Claude Code behavior that is not as it says, or a task that cannot be done as written, stop and report the contradiction instead of choosing another design.

## Fix a review

6. For each `Fix` finding not yet `Resolved`, make the change it states at its cause, and keep the test it names. For each `Record` finding, open the issue it states with `gh issue create --label bug`. Leave `Rejected` findings alone. When a finding's defect does not exist or its fix would contradict the plan, report that with the evidence instead of changing the code.

## Run the checks

7. Run the checks `AGENTS.md` ("Verification") requires for the change on the final tree, after your last edit, and fix each failure it causes. Report a check as blocked only when something you cannot change prevents it, and name the blocker.

## Final response

If you stopped at step 5, reply with the contradiction, its evidence in the code, and the decision you need, and nothing else. Otherwise reply in the form below and nothing else. Lead with the main point, write plainly, and leave out the steps you took.

```markdown
One or two sentences: what the change does, and whether it is ready for review.

**Files:**

- `internal/claude/claude.go` (modified)
- `cmd/ox/web/src/ox.tsx` (modified)

**Requirement changes:** applied as planned.

**Findings:** F1 fixed; F2 recorded as #123; F3 not fixed: its defect does not exist, because …

**Open choices:** each choice the plan or review left open that a reviewer should check.

**Checks:** `make test` passed; `make e2e` passed; `make live` passed; headless Chrome through `make dev` passed.

**Blocked:** `make live`: what blocks it.
```

List every file created or modified. Under Checks, list each check from step 7 with its result on the final tree. Write `None.` for Requirement changes when the plan has none. Include Findings only when fixing a review. Omit Open choices when there are none, and Blocked when every check ran.
