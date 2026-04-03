---
name: work
description: "Execute a repo plan end to end: implement, commit logical units, and validate with parallel subagent review at the end."
argument-hint: "[docs/agents/plans/... plan, specification, or docs/agents/todo/... file path]"
disable-model-invocation: true
---

## Workflow

### Pre-flight

1. Check that the git repo is clean.
   - Run `git status`. If there are staged changes, unstaged changes, or untracked files, stop and report them to the user.
   - Offer to commit the changes, add them to `.gitignore`, or stash them — whatever makes sense for what you see.
   - Do not proceed until the repo is clean or the user explicitly says to continue with dirty state.

### Orientation

2. Resolve `<input_document> $ARGUMENTS </input_document>`.
   - Prefer `docs/agents/plans/` for implementation plans and `docs/agents/todo/` for tracked follow-up work.
3. Read the work doc completely.
   - Read the plan end to end.
   - Read the files listed in the plan's `Related code` section. Read them — do not skim.
   - Read `CLAUDE.md` for project-specific quality commands, conventions, and patterns.
   - The point is to ship complete work, not just produce movement. Understand the plan before touching code.
4. If anything is unclear or ambiguous after reading the plan, references, and related code, ask the user now.
   - Better to ask once before starting than to build the wrong thing. Get user approval to proceed.
   - Skip this step if the plan is clear and the path forward is obvious.
5. Verify the repo setup and baseline quality commands before major edits — run this in a subagent.
   - Launch a subagent (model: "sonnet") that confirms dependencies are installed, then runs tests, lint, formatter, and type checks using the commands from `CLAUDE.md`.
   - The subagent should return a concise summary: what passed, what failed, and any broken baseline issues.
   - If the baseline is already broken, tell the user before proceeding.
6. Create a Todo list from the plan's implementation tasks.
   - Include testing tasks alongside implementation tasks.
   - Keep tasks specific and completable — each one should map to a checkable plan item.

### Execution

7. Execute the plan task by task — **code first, fix tests after**.
   - Read the related code and nearby patterns before implementing each task.
   - Implement in repo style. Match naming conventions, error handling patterns, and file organization.
   - Comment the code well. Comments should explain the architecture and the "why", not merely describe what the code does.
   - Write or update tests for each piece of new functionality, but don't stop to chase test failures mid-implementation. The goal is to get all the code written for a chunk first, then circle back to make the tests green.
   - Update Todo state and mark the matching plan checkbox complete (`[ ]` → `[x]`).
   - Keep the implementation aligned with the plan unless the user explicitly redirects or the plan is clearly wrong.
   - **After all code for a chunk is written**, run the relevant checks and fix any test failures or lint issues before moving to validation.
   - _Exception for longer, phased plans:_ If the plan has multiple phases that build on each other and the complexity is high enough that deferred test failures would be hard to diagnose, you may test as you go — but state why in your Todo list or message to the user before doing so. The default is still code-first.
8. If you find yourself spinning your wheels or faced with an unexpected obstacle, stop and ask the user for guidance.

### Commit

When a logical chunk of work is complete and its tests pass, commit it.
A "chunk" is a unit of work that can be described in one sentence and stands on its own —
typically one plan section or a tightly related cluster of tasks.

9. Commit each chunk using the 7 rules of great commit messages AND WITH NO CLAUDE CODE ATTRIBUTION.
    - **Commit when:** the chunk is complete, tests pass, and it can be described in one sentence.
    - **Do not commit when:** work is half-finished, tests are failing, or unrelated changes are mixed in.
    - Do not wait until the end to commit. Small, meaningful commits build momentum and make review easier.

Repeat steps 7–9 for each chunk until the plan is complete.

If you start to run out of context, stop and offer to run `/handoff` so we can finish the work in a follow-up session without losing anything important.

### Validate

After all plan work is complete and committed, validate the full body of work with subagent review.

10. Run the full suite.
    - Run tests, lint, formatter, and type checks as applicable.
    - Use the commands from `CLAUDE.md`, not ad hoc substitutes.
    - Fix any failures and commit the fixes before proceeding.
11. Launch two review subagents **in parallel** using the Agent tool.
    - Both subagents must use `model: "sonnet"` for speed and cost efficiency.
    - Each Agent invocation starts with fresh context — include all necessary details in the prompt.
    - Build each subagent prompt by reading the corresponding template, then filling in the specifics of the full plan.
    - **Completeness review** — Read `${CLAUDE_SKILL_DIR}/assets/completeness-review-prompt.md` for the prompt framework. Fill in the plan path, all tasks completed, and the list of all changed files. The subagent reads the plan and all changed files, then evaluates whether the work is genuinely complete with no omissions, hacks, disabled warnings, or workarounds. The subagent should assume that the code builds and the tests pass and should not verify this itself.
    - **Test quality review** — Read `${CLAUDE_SKILL_DIR}/assets/test-quality-review-prompt.md` for the prompt framework. Fill in the plan path, all tasks completed, the test files, and the implementation files. The subagent reads the implementation and tests, then evaluates whether the tests verify real behavior, cover edge cases, and would actually catch bugs.
12. Act on review findings.
    - If either reviewer reports issues worth fixing: fix them, then re-run the relevant quality checks.
    - If both reviewers pass, proceed to handoff.

### Handoff

13. Commit any remaining validated work.
14. Report what shipped, quality-check results, commit summary, branch name, and any follow-up work.

## Principles

- **Start clean** — verify the repo state before touching anything.
- **The plan is your guide** — follow its references, don't reinvent.
- **Code first, then fix tests** — get the implementation down, then make it green. Except in complex phased work where early feedback is worth the interruption.
- **Commit frequently** — small, tested, logical chunks.
- **Validate at the end** — subagent review catches what you miss across the full body of work.
- **Ship complete features** — don't leave things 80% done.
