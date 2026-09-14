---
name: kwork
description: Execute a settled Ox repository plan with focused checks, then complete the integrated feature gate when its top-level TODO item finishes.
argument-hint: "<eng/plans/... plan path>"
---

## Workflow

### Pre-flight

1. Resolve `<input_document> $ARGUMENTS </input_document>`.
   - The selected work must have an explicit plan in `eng/plans/`. A
     specification or TODO entry supplies scope, but does not replace the
     required settled plan.
   - If no plan is supplied or the requested work has none, stop and ask the
     user to choose or create a plan. Do not infer a plan from repository order.
2. Check `git status`. Preserve unrelated changes and continue prior work. Ask
   only when overlapping changes leave ownership or intended behavior unclear.
3. Read the work document completely, then inspect the related code and nearby
   patterns needed to execute it.

### Implement

4. Turn the plan's implementation and test bullets into a short working
   checklist when useful.
5. Implement the selected plan. An intermediate task may leave a feature
   partially implemented; preserve supported behavior without inventing new
   scope. Follow the plan and repository guidance, write focused tests through
   public boundaries, and ask the user only when authoritative sources leave a
   material decision unresolved.

### Validate

6. Run focused checks while implementing. Choose validation in proportion to
   risk and the plan's completion boundary:
   - Use focused tests and checks for intermediate plans.
   - Run `make check-all` when a top-level feature becomes integrated, or
     earlier when a concrete regression risk warrants it.
   - For documentation-only, filename-only, and test-only changes, inspect the
     diff and run focused checks unless repository guidance requires more. Fix
     regressions and report expected failures from unfinished integration; do
     not claim a feature is complete while they remain.
7. Do not review individual plans or spawn a review agent. When the selected
   work finishes all work under a top-level TODO item, run the one required
   completeness and simplification review with `kreview`, fix its findings, and
   rerun affected gates before treating that top-level item as complete.

### Record completion and hand off

8. When a plan implements an unchecked child task in `eng/todo.md` and required
   checks pass, change only that child's checkbox from `- [ ]` to `- [x]`. Do
   not complete a top-level item until all of its work, the required completion
   review, and any review fixes are complete; then remove its completed subtree
   so the roadmap remains forward-looking. A defect-repair plan with no TODO
   task leaves `eng/todo.md` untouched.
9. Commit only when the user authorizes it. A plan boundary does not require a
   commit.
10. Hand off concisely: what changed, checks run, and unfinished integration.
    Distinguish an intermediate plan from a completed feature.
