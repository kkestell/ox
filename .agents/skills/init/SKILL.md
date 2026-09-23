---
name: init
description: Create or update AGENTS.md for the workspace.
---

Create or update `AGENTS.md` at the repository root.

Inspect the repository first. Read the existing `AGENTS.md`, if present, along
with the root documentation, build files, and the main source and test entry
points. Record only facts you can verify from the repository. Do not invent
commands, conventions, architecture, document names, or development processes.

If `AGENTS.md` already exists, preserve its useful instructions and overall
structure. Add newly discovered information to the relevant existing sections,
and remove something only when the repository shows that it is stale. Do not
replace a project-specific document with a generic one.

If `AGENTS.md` does not exist, use the template below as a starting point. Keep,
rename, add, or omit sections according to what is useful and supported by the
repository. Remove all placeholder text from the finished file. Do not modify
any other file.

```markdown
{A brief description of the project, if the repository establishes one.}

## Repository Map

- `{path}`: {What this part of the repository contains or does.}

## Commands

- Build: `{command}`
- Test: `{command}`
- Run: `{command}`
- Format: `{command}`

## Conventions

- {Project-specific guidance supported by the repository.}
```
