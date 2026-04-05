---
name: review
description: >
  Review code for violations of the PHAME design principles (hierarchy,
  abstraction, modularization, encapsulation). Use this skill whenever the user
  asks for a design review, architecture review, code quality audit, PHAME
  review, or wants feedback on the structural health of their codebase. Also
  trigger when the user asks about coupling, cohesion, encapsulation issues,
  abstraction leaks, or layering problems — these are all PHAME concerns.
argument-hint: "[file, directory, or glob pattern to review]"
---

## Overview

PHAME stands for the Principles of **Hierarchy**, **Abstraction**,
**Modularization**, and **Encapsulation** — the four fundamental software design
principles identified by Grady Booch. They are not independent rules but
interlocking facets of the same discipline: building systems that humans can
understand, change, and trust.

This skill reviews code through all four lenses in parallel and produces a
single consolidated violations report.

## Workflow

1. **Determine scope.**
   - If the user provided `$ARGUMENTS`, review that file, directory, or glob.
   - If no target was given, review the most structurally significant code in
     the working directory — focus on source files, not generated code, vendor
     directories, or test fixtures.
   - For large codebases, focus on the files changed in recent commits or the
     area the user has been working in rather than trying to review everything.

2. **Read the reference material.** Each principle has a reference file in
   `${CLAUDE_SKILL_DIR}/references/`:
   - `hierarchy.md` — layering, inheritance depth, containment, dependency direction
   - `abstraction.md` — interface clarity, level mixing, leaky abstractions
   - `modularization.md` — cohesion, coupling, module boundaries, god modules
   - `encapsulation.md` — data hiding, invariant protection, public surface area

   Spawn four subagents in parallel, one per principle. Each subagent:
   - Reads its reference file to calibrate what to look for
   - Reviews the target code through that single lens
   - Returns findings as a JSON array (schema below)

3. **Subagent prompt template.** Use this prompt for each subagent, substituting
   the principle name and reference path:

   ```
   You are reviewing code for violations of the **{PRINCIPLE}** design principle.

   First, read the reference guide:
   {CLAUDE_SKILL_DIR}/references/{principle}.md

   Then review the following code:
   {target files/directories}

   For each violation you find, return a JSON array of objects with these fields:
   - "principle": "{PRINCIPLE}"
   - "severity": one of "Critical", "High", "Medium", "Low"
   - "location": file path and line number or range (e.g., "src/auth/handler.go:45-78")
   - "anti_pattern": the name of the anti-pattern from the reference (e.g., "Leaky abstraction")
   - "finding": a concise description of the specific violation (1-2 sentences)
   - "suggestion": a concrete, actionable recommendation to fix it (1-2 sentences)

   Calibrate severity using the guide in the reference file. Be specific — quote
   code, name symbols, cite line numbers. Do not flag things that are merely
   stylistic preferences or that tooling (linters, formatters) already handles.
   Focus on structural design issues that affect comprehensibility, changeability,
   or correctness.

   If you find no violations for this principle, return an empty array: []

   Return ONLY the JSON array, no surrounding text.
   ```

4. **Collect and merge results.** Parse the JSON arrays from all four subagents
   into a single list. Deduplicate findings that overlap (e.g., the same code
   flagged as both a hierarchy and modularization issue — keep the more specific
   or higher-severity one and note the overlap).

5. **Format the report.** Present the results as a table grouped by principle,
   sorted by severity (Critical first) within each group. Use this format:

   ```
   ## PHAME Review: {target}

   {summary — 2-3 sentences on overall structural health}

   ### Hierarchy ({N} findings)

   | Severity | Location | Anti-Pattern | Finding | Suggestion |
   |----------|----------|--------------|---------|------------|
   | Critical | path:L42 | Circular packages | ... | ... |
   | High     | path:L99 | Upward dependency | ... | ... |

   ### Abstraction ({N} findings)

   | Severity | Location | Anti-Pattern | Finding | Suggestion |
   |----------|----------|--------------|---------|------------|
   | ...      | ...      | ...          | ...     | ...        |

   ### Modularization ({N} findings)
   ...

   ### Encapsulation ({N} findings)
   ...

   ### Summary

   | Principle      | Critical | High | Medium | Low | Total |
   |----------------|----------|------|--------|-----|-------|
   | Hierarchy      | ...      | ...  | ...    | ... | ...   |
   | Abstraction    | ...      | ...  | ...    | ... | ...   |
   | Modularization | ...      | ...  | ...    | ... | ...   |
   | Encapsulation  | ...      | ...  | ...    | ... | ...   |
   | **Total**      | ...      | ...  | ...    | ... | ...   |
   ```

   If a principle has no findings, include the section header with "No issues
   found" rather than omitting it — this signals the review was thorough.

6. **Closing guidance.** After the table, add a brief prioritization note:
   address Critical and High findings first, as they represent structural
   problems that tend to worsen over time. Medium and Low findings are worth
   tracking but may not warrant immediate action.
