# Metrics

Written by `make metrics` and the pre-commit hook from the files staged for
commit.

## Size

Lines counted by cloc. Files, Comment, and Code cover production files; Tests is
the code in `*_test.go` and `cmd/ox/web/e2e`.

| Language   | Files | Comment |  Code | Tests |
| ---------- | ----: | ------: | ----: | ----: |
| Go         |    26 |     542 | 4,570 | 6,633 |
| TypeScript |    17 |     142 | 2,138 | 1,700 |
| CSS        |     1 |      24 |   344 |     0 |

## High complexity

Production functions with cognitive complexity over 15, scored by gocognit for
Go and Biome for TypeScript.

| Complexity | Function                     | Location                         |
| ---------: | ---------------------------- | -------------------------------- |
|         47 | `(*Agent).handleOutput`      | `internal/claude/agent.go:172`   |
|         29 | `read`                       | `internal/ur/stream.go:94`       |
|         28 | `checkConfig`                | `cmd/oxd/main.go:127`            |
|         21 | `(*Server).configureSession` | `internal/server/methods.go:497` |
|         18 | `(*Server).serveConn`        | `internal/server/server.go:95`   |
|         17 | `deliverOutput`              | `internal/claude/claude.go:299`  |
|         16 | `(*Server).createSession`    | `internal/server/methods.go:295` |

## Long files

Production files with over 500 lines of code.

| Code | File                         |
| ---: | ---------------------------- |
|  535 | `internal/server/methods.go` |
|  524 | `internal/server/mcp.go`     |

## Lint suppressions

Comments that silence a linter, in production and test files.

| Language   | Comment        | Suppressions |
| ---------- | -------------- | -----------: |
| Go         | `nolint`       |            0 |
| TypeScript | `biome-ignore` |            1 |
| CSS        | `biome-ignore` |            0 |

## Known issues

2 entries in `docs/known-issues.md`.
