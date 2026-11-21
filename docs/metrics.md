# Metrics

Written by `make metrics` and the pre-commit hook from the files staged for commit.

## Size

Lines counted by cloc. Files, Comment, and Code cover production files; Tests is the code in `*_test.go` and `cmd/ox/web/e2e`.

| Language | Files | Comment | Code | Tests |
|---|---:|---:|---:|---:|
| Go | 23 | 455 | 4,130 | 5,425 |
| TypeScript | 17 | 134 | 2,090 | 1,608 |
| CSS | 1 | 23 | 341 | 0 |

## High complexity

Production functions with cognitive complexity over 15, scored by gocognit for Go and Biome for TypeScript.

| Complexity | Function | Location |
|---:|---|---|
| 47 | `(*Agent).handleOutput` | `internal/claude/agent.go:169` |
| 29 | `createSession` | `internal/server/mcp.go:331` |
| 21 | `(*Server).configureSession` | `internal/server/methods.go:488` |
| 17 | `deliverOutput` | `internal/claude/claude.go:290` |
| 16 | `checkConfig` | `cmd/oxd/main.go:117` |
| 16 | `listSessions` | `internal/server/mcp.go:233` |
| 16 | `(*Server).handleMCP` | `internal/server/mcp.go:50` |
| 16 | `(*Server).createSession` | `internal/server/methods.go:308` |

## Long files

Production files with over 500 lines of code.

| Code | File |
|---:|---|
| 590 | `internal/server/mcp.go` |
| 529 | `internal/server/methods.go` |

## Lint suppressions

Comments that silence a linter, in production and test files.

| Language | Comment | Suppressions |
|---|---|---:|
| Go | `nolint` | 0 |
| TypeScript | `biome-ignore` | 1 |
| CSS | `biome-ignore` | 0 |

## Known issues

2 entries in `docs/known-issues.md`.
