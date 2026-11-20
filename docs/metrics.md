# Metrics

Written by `make metrics` and the pre-commit hook from the files staged for commit.

## Size

Lines counted by cloc. Files, Comment, and Code cover production files; Tests is the code in `*_test.go` and `cmd/ox/web/e2e`.

| Language | Files | Comment | Code | Tests |
|---|---:|---:|---:|---:|
| Go | 23 | 416 | 3,849 | 5,119 |
| TypeScript | 14 | 107 | 1,704 | 1,340 |
| CSS | 1 | 13 | 281 | 0 |

## High complexity

Production functions with cognitive complexity over 15, scored by gocognit for Go and Biome for TypeScript.

| Complexity | Function | Location |
|---:|---|---|
| 47 | `(*Agent).handleOutput` | `internal/claude/agent.go:169` |
| 27 | `createSession` | `internal/server/mcp.go:331` |
| 18 | `(*Server).configureSession` | `internal/server/methods.go:442` |
| 17 | `deliverOutput` | `internal/claude/claude.go:290` |
| 16 | `checkConfig` | `cmd/oxd/main.go:117` |
| 16 | `(*Server).handleMCP` | `internal/server/mcp.go:50` |
| 16 | `(*Server).createSession` | `internal/server/methods.go:262` |

## Long files

Production files with over 500 lines of code.

| Code | File |
|---:|---|
| 586 | `internal/server/mcp.go` |

## Lint suppressions

Comments that silence a linter, in production and test files.

| Language | Comment | Suppressions |
|---|---|---:|
| Go | `nolint` | 0 |
| TypeScript | `biome-ignore` | 1 |
| CSS | `biome-ignore` | 0 |

## Known issues

2 entries in `docs/known-issues.md`.
