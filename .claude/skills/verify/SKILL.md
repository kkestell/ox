---
name: verify
description: Verify completed work against a supplied `docs/agents/plans/` plan, `docs/agents/todo/` follow-up doc, spec, or brainstorm doc, run the repo's quality gates, and report readiness for handoff.
argument-hint: "[docs/agents/plans/... path, docs/agents/todo/... path, spec path, or verification target]"
disable-model-invocation: true
---

## Workflow

1. Read `<verification_target> $ARGUMENTS </verification_target>`.
   - If it is empty, ask the user for the plan, spec, or doc path to verify against and stop.
   - Do not make product changes unless the user explicitly asks.
2. Resolve the intent docs with `bash "${CLAUDE_SKILL_DIR}/scripts/find_intent_docs.sh" "<verification_target>"`.
   - The target may be a plan file, brainstorm file, spec file, or a directory containing those docs.
   - Read `primary_path` fully.
   - If `plan_path` or `brainstorm_path` are also resolved and differ from `primary_path`, read them fully too.
   - If the script cannot resolve a usable document, ask the user to point to the exact file and stop.
3. Build the verification surface.
   - Use `repo_root` from the script output as the git root.
   - Check `git status --short` and include any uncommitted changes in the verification surface rather than silently ignoring them.
   - Choose the best available comparison base: prefer `main`; otherwise use the repo's default branch if you can resolve it; if neither is available, report the limitation and review `HEAD` plus uncommitted changes directly.
   - Inspect the full work scope with the chosen base:
     ```
     git log --stat --decorate <base>..HEAD
     git diff --stat <base>...HEAD
     git diff <base>...HEAD
     ```
4. Run the repo's canonical quality gates.
   - Use the build, lint, formatter-check, type-check, and test commands from `CLAUDE.md` or project config. Prefer the repo's own commands over ad hoc substitutes.
   - Do not silently skip a check category — explain why it does not apply if you skip it.
   - Verify appropriate test coverage for new functionality.
   - Treat any failing check as a blocker.
5. Judge completeness against the intent docs.
   - Compare the delivered work against the resolved intent docs, not just the diff.
   - Every planned deliverable should be implemented. No half-implemented features or placeholder stubs.
   - Edge cases called out in the docs should not be skipped without explanation.
   - New functionality should have tests matching the project's conventions.
   - Required documentation, config, or schema updates should be included.
   - Prioritize: broken behavior, risky omissions, false confidence from incomplete tests, merge blockers. List highest-severity issues first with file references.
6. Final cleanliness check.
   - Run `git status --short` again and report whether the repo is clean.
7. Write the final report using `${CLAUDE_SKILL_DIR}/assets/verification-report-template.md`.
   - Report repo root, branch, verification target, companion docs used, comparison base, commits reviewed, checks, findings by severity, cleanliness, test coverage, and readiness.
   - If clean and blocker-free, say the change is ready for the user's next step.
   - Otherwise summarize what must be fixed next.
