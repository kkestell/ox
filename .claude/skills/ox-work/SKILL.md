---
name: ox-work
description: Implement a plan for Ox from `agents/plans/`, or fix the findings of a review from `agents/reviews/`, and leave the changes uncommitted for `/ox-review`. Proves that each new test protects the change before handing off. Use to carry out a plan or a review's fixes, not to plan or review. Never use it unless the user, or an agent acting on the user's request, invokes it.
argument-hint: "[plan path, or fix <review path>]"
---

Use this skill only when the user, or an agent acting on the user's request, invokes it. Never use it on your own initiative.

## Objective

Change Ox exactly as the plan or review says, and hand off changes whose tests are proven, so that `/ox-review` finds nothing the implementer could have found. Leave the changes uncommitted. Edit no plan or review. Do not start other agents.

## Establish the work

1. Read `<work> $ARGUMENTS </work>`. If it names neither a plan in `agents/plans/` nor a review in `agents/reviews/`, ask what to implement and stop. For a review, also read the plan it names.
2. Read `AGENTS.md`, `docs/functional-requirements.md`, and `docs/known-issues.md`. For web client work, also read `cmd/ox/web/AGENTS.md`. Read the sections of `docs/architecture.md`, `docs/protocol.md`, and `docs/claude-code.md` that cover the code the work touches.

## Implement a plan

3. Carry out the plan's Implementation plan in order, keeping its Decisions and its settled Check your assumptions entries. Write each test its Test plan names, in the test it names.
4. Apply its Requirement changes to `docs/functional-requirements.md` word for word, and change nothing else in that file. Make its Documentation updates, and correct any other document or comment the change makes wrong.
5. Settle what the plan leaves open, such as names, helpers, and where code goes, the way its Decisions would. When the code contradicts the plan, such as an owner, function, or Claude Code behavior that is not as it says, or a task that cannot be done as written, stop and report the contradiction instead of choosing another design.

## Fix a review

6. For each `Fix` finding not yet `Resolved`, make the change it states at its cause, and keep the test it names. For each `Record` finding, add the entry it states to `docs/known-issues.md`. Leave `Rejected` findings alone. When a finding's defect does not exist or its fix would contradict the plan, report that with the evidence instead of changing the code.

## Prove the tests

7. Run each test that reproduces a defect once against the base revision, in a detached worktree that you remove afterwards, and confirm it fails there. Failing at its first assertion is enough. Step 8 covers every other new or changed test.
8. Remove each condition the change adds, one at a time, and confirm a test fails. Add or strengthen a test for each removal no test catches. Revert every such edit.
9. Run the checks `AGENTS.md` ("Verification") requires for the change on the final tree, after your last edit, and fix each failure it causes. Report a check as blocked only when something you cannot change prevents it, and name the blocker.

## Final response

If you stopped at step 5, reply with the contradiction, its evidence in the code, and the decision you need, and nothing else. Otherwise reply in the form below and nothing else. Lead with the main point, write plainly, and leave out the steps you took.

```markdown
One or two sentences: what the change does, and whether it is ready for review.

**Files:**

- `internal/claude/claude.go` (modified)
- `cmd/ox/web/src/ox.tsx` (modified)

**Requirement changes:** applied as planned.

**Findings:** F1 fixed; F2 recorded; F3 not fixed: its defect does not exist, because …

**Tests proved:** `TestHeldPromptDropped` fails on the base revision; removing the `ended` check in `Send` fails `TestSendAfterEnd`.

**Open choices:** each choice the plan or review left open that a reviewer should check.

**Checks:** `make test` passed; `make e2e` passed; `make live` passed; headless Chrome through `make dev` passed.

**Blocked:** `make live`: what blocks it.
```

List every file created or modified. Under Tests proved, list each test run against the base revision, and each condition removed in step 8 with the test that caught it. Under Checks, list each check from step 9 with its result on the final tree. Write `None.` for Requirement changes when the plan has none. Include Findings only when fixing a review. Omit Open choices when there are none, and Blocked when every check ran.
