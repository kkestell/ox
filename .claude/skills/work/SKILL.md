---
name: twork
description: "Execute a repo plan end to end: implement, test continuously, validate each chunk with parallel subagent review, commit logical units, and prepare the branch for `/tverify`."
argument-hint: "[plan file, specification, or todo file path]"
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
3. Read the work doc completely.
   - Read the plan end to end.
   - Read the files listed in the plan's `Related code` section. Read them — do not skim.
   - Read `CLAUDE.md` for project-specific quality commands, conventions, and patterns.
   - The point is to ship complete work, not just produce movement. Understand the plan before touching code.
4. If anything is unclear or ambiguous after reading the plan, references, and related code, ask the user now.
   - Better to ask once before starting than to build the wrong thing. Get user approval to proceed.
   - Skip this step if the plan is clear and the path forward is obvious.
5. Verify the repo setup and baseline quality commands before major edits.
   - Confirm dependencies are installed. Run tests, lint, formatter, and type checks to establish a green baseline.
   - If the baseline is already broken, tell the user before proceeding.
6. Create a Todo list from the plan's implementation tasks.
   - Include testing tasks alongside implementation tasks.
   - Keep tasks specific and completable — each one should map to a checkable plan item.

### Execution

7. Execute the plan task by task.
   - Read the related code and nearby patterns before implementing each task.
   - Implement in repo style. Match naming conventions, error handling patterns, and file organization.
   - Write or update tests for each piece of new functionality.
   - Run the relevant checks after each change.
   - Update Todo state and mark the matching plan checkbox complete (`[ ]` → `[x]`).
   - Keep the implementation aligned with the plan unless the user explicitly redirects or the plan is clearly wrong.
   - Test continuously. Fix failures as they appear instead of saving them for the end.
8. If you find yourself spinning your wheels or faced with an unexpected obstacle, stop and ask the user for guidance.

### Validate and commit

When a logical chunk of work is complete and its tests pass, validate it before committing.
A "chunk" is a unit of work that can be described in one sentence and stands on its own —
typically one plan section or a tightly related cluster of tasks.

9. Launch two review subagents **in parallel** using the Agent tool.
   - Both subagents must use `model: "sonnet"` for speed and cost efficiency.
   - Each Agent invocation starts with fresh context — include all necessary details in the prompt.
   - Build each subagent prompt by reading the corresponding template, then filling in the specifics of the current chunk.
   - **Completeness review** — Read `${CLAUDE_SKILL_DIR}/assets/completeness-review-prompt.md` for the prompt framework. Fill in the plan path, the tasks completed in this chunk, and the list of changed files. The subagent reads the plan and all changed files, then evaluates whether the work is genuinely complete with no omissions, hacks, disabled warnings, or workarounds.
   - **Test quality review** — Read `${CLAUDE_SKILL_DIR}/assets/test-quality-review-prompt.md` for the prompt framework. Fill in the plan path, the tasks completed, the test files, and the implementation files. The subagent reads the implementation and tests, then evaluates whether the tests verify real behavior, cover edge cases, and would actually catch bugs.
10. Act on review findings.
    - If either reviewer reports issues worth fixing: fix them, re-run the relevant quality checks, then re-launch both subagents to confirm the fixes.
    - If the second round still finds issues, report the remaining findings to the user and ask how to proceed rather than looping further.
    - If both reviewers pass, proceed to commit.
11. Commit each validated chunk using the 7 rules of great commit messages AND WITH NO CLAUDE CODE ATTRIBUTION.
    - **Commit when:** the chunk is complete, tests pass, both reviews are clean, and it can be described in one sentence.
    - **Do not commit when:** work is half-finished, tests are failing, reviews flagged unresolved issues, or unrelated changes are mixed in.
    - Do not wait until the end to commit. Small, meaningful commits build momentum and make review easier.

Repeat steps 7–11 for each chunk until the plan is complete.

### Handoff

12. Run the full suite before handoff.
    - Run tests, lint, formatter, and type checks as applicable.
    - Use the commands from `CLAUDE.md`, not ad hoc substitutes.
13. Commit any remaining validated work.
14. Report what shipped, quality-check results, commit summary, branch name, and any follow-up work.
15. Then offer `/verify`.

## Principles

- **Start clean** — verify the repo state before touching anything.
- **The plan is your guide** — follow its references, don't reinvent.
- **Test as you go** — continuous testing prevents big surprises.
- **Validate before committing** — subagent review catches what you miss.
- **Commit frequently** — small, tested, validated, logical chunks.
- **Ship complete features** — don't leave things 80% done.
