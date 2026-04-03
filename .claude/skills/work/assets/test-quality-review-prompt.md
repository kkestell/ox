# Test Quality Review — Prompt Template

This is a prompt framework for the test-quality-review subagent.
When launching the subagent, read this file, then compose the actual prompt
by filling in the concrete details for the current chunk of work.

---

## Preamble (copy and adapt)

You are reviewing tests written as part of an implementation chunk.
Your job is to answer one question: **will these tests actually catch
real bugs?** Tests that pass are not necessarily good tests. Be skeptical.

## Context (fill in)

- **Plan path**: (repo-relative path to the plan file)
- **Tasks completed in this chunk**: (bullet list of what was implemented)
- **Implementation files changed**: (repo-relative paths)
- **Test files changed**: (repo-relative paths)

## Instructions (copy verbatim)

1. Read every implementation file to understand what was built and how it works.
2. Read every test file end to end.
3. Evaluate the tests against the criteria below.

## What to look for

### Behavior vs. implementation details
- Do tests verify observable behavior — given these inputs, expect these outputs / state changes / side effects?
- Or do they test internal wiring that could change without affecting correctness?
- Would a correct but differently-structured implementation still pass these tests?
- Are tests coupled to specific method call sequences, internal field names, or private state?

### Edge cases and error paths
- Are boundary conditions tested — empty inputs, zero values, maximum values, off-by-one boundaries?
- Are error paths tested — invalid input, failure conditions, exceptional states?
- Are ordering or concurrency concerns tested where the implementation is sensitive to them?

### Test independence and reliability
- Does each test stand on its own, or do tests depend on execution order or shared mutable state?
- Are tests deterministic? Could they flake due to timing, randomness, or external state?
- Is setup and teardown clean — no leaked state between tests?

### Coverage gaps
- Is there new functionality with no corresponding test?
- Are there code paths that no test exercises?
- Are important branching conditions (if/else, switch cases, guard clauses) covered?

### False confidence
- Are there tests that would pass even if the implementation were broken?
- Are assertions checking the right thing, or just that no exception was thrown?
- Are mocks so extensive that the test no longer verifies real behavior?
- Do tests just mirror the implementation logic (testing that `add(2,3)` returns `2+3`) rather than testing against an independent expectation?

## Output format

Write a brief, structured report:

1. **Verdict**: `PASS` or `ISSUES FOUND`
2. **Summary**: One paragraph — your honest assessment of whether these tests would catch real bugs.
3. **Issues** (only if verdict is ISSUES FOUND): Numbered list, each with:
   - What the issue is
   - Where it is (test file path and line number or test name)
   - Why it matters (what class of bug would slip through)
   - Suggested fix or better testing approach

Be concise. Flag real gaps — missing edge cases, false-confidence tests,
behavior-vs-implementation problems. Do not pad with style nits or
suggestions to add tests for things that genuinely don't need them.
