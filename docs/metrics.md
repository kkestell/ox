# Metrics

Written by `make metrics` and the pre-commit hook from the files staged for commit.

## Size

Lines counted by cloc. Files, Comment, and Code cover production files; Tests is the code in `*_test.go` and `cmd/ox/web/e2e`.

| Language | Files | Comment | Code | Tests |
|---|---:|---:|---:|---:|
| Go | 22 | 381 | 3,496 | 4,664 |
| TypeScript | 13 | 89 | 1,514 | 1,171 |
| CSS | 1 | 11 | 262 | 0 |

## High complexity

Production functions with cognitive complexity over 15, scored by gocognit for Go and Biome for TypeScript.

| Complexity | Function | Location |
|---:|---|---|
| 47 | `(*Agent).handleOutput` | `internal/claude/agent.go:169` |
| 18 | `(*Server).configureSession` | `internal/server/methods.go:394` |
| 17 | `deliverOutput` | `internal/claude/claude.go:290` |
| 16 | `checkConfig` | `cmd/oxd/main.go:117` |
| 16 | `(*Server).handleMCP` | `internal/server/mcp.go:42` |
| 16 | `(*Server).createSession` | `internal/server/methods.go:222` |

## Long files

Production files with over 500 lines of code.

None.

## Lint suppressions

Comments that silence a linter, in production and test files.

| Language | Comment | Suppressions |
|---|---|---:|
| Go | `nolint` | 0 |
| TypeScript | `biome-ignore` | 1 |
| CSS | `biome-ignore` | 0 |

## Known issues

2 entries in `docs/known-issues.md`.
