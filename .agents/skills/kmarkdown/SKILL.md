---
name: kmarkdown
description: Reformat Markdown documents with dprint using this skill's shipped config (hard-wrap prose at 80 columns). Use only when the user requests Markdown formatting or repository instructions explicitly require it.
argument-hint: "[markdown file or directory paths]"
---

## Overview

This skill reformats Markdown with [dprint](https://dprint.dev) using the config shipped alongside it at `assets/dprint.json`. The config uses the dprint Markdown plugin with `textWrap: "always"` and `lineWidth: 80`, so prose is normalized and hard-wrapped at 80 columns.

Do not invoke this skill merely because Markdown files were edited. Use it only
for an explicit user request or an explicit repository instruction.

## Workflow

### 1. Resolve the targets

Resolve target files from `<input_document> $ARGUMENTS </input_document>`. Accept one or more file or directory paths. **If no paths are given, ask the user which files or directories to format and stop.**

Expand directories to the Markdown files within them (`*.md`, `*.markdown`). Skip anything that is not a Markdown file.

### 2. Check that dprint is available

Run `command -v dprint`. If dprint is not installed, tell the user it is required (`brew install dprint` or see https://dprint.dev/install) and stop — do not attempt to reformat by hand.

### 3. Format each file

The config lives at this skill's `assets/dprint.json`. dprint resolves file patterns relative to its base directory and will not touch files outside it, so format each file from its own directory:

```bash
( cd "$(dirname "<file>")" && dprint fmt --config "<skill-dir>/assets/dprint.json" "$(basename "<file>")" )
```

Format every resolved Markdown file this way.

### 4. Report

Summarize which files were reformatted. If dprint left a file unchanged, say so. Surface any files that were skipped because they were not Markdown.

## Principles

- **Markdown only** — never reformat non-Markdown files; skip and report them.
- **Use the shipped config** — always pass `--config assets/dprint.json`; do not rely on any ambient dprint config.
- **Don't hand-format** — if dprint is unavailable, stop and tell the user rather than reformatting manually.
