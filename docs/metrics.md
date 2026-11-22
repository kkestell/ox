# Metrics

Written by `make metrics` and the pre-commit hook from the files staged for
commit.

## Size

Lines counted by cloc. Files, Comment, and Code cover production files; Tests is
the code in `*_test.go` and `cmd/ox/web/e2e`.

| Language   | Files | Comment |  Code | Tests |
| ---------- | ----: | ------: | ----: | ----: |
| Go         |    26 |     589 | 4,668 | 7,098 |
| TypeScript |    18 |     198 | 2,490 | 2,298 |
| CSS        |     1 |      37 |   373 |     0 |

## High complexity

Production functions with cognitive complexity over 15, scored by gocognit for
Go and Biome for TypeScript.

| Complexity | Function                     | Location                         |
| ---------: | ---------------------------- | -------------------------------- |
|         47 | `(*Agent).handleOutput`      | `internal/claude/agent.go:172`   |
|         29 | `read`                       | `internal/ur/stream.go:94`       |
|         28 | `checkConfig`                | `cmd/oxd/main.go:127`            |
|         21 | `(*Server).configureSession` | `internal/server/methods.go:485` |
|         18 | `(*Server).serveConn`        | `internal/server/server.go:96`   |
|         17 | `deliverOutput`              | `internal/claude/claude.go:299`  |
|         16 | `(*Server).createSession`    | `internal/server/methods.go:294` |

## Long files

Production files with over 500 lines of code.

| Code | File                         |
| ---: | ---------------------------- |
|  553 | `internal/server/mcp.go`     |
|  531 | `internal/server/methods.go` |

## Lint suppressions

Comments that silence a linter, in production and test files.

| Language   | Comment        | Suppressions |
| ---------- | -------------- | -----------: |
| Go         | `nolint`       |            0 |
| TypeScript | `biome-ignore` |            4 |
| CSS        | `biome-ignore` |            0 |

## Known issues

2 entries in `docs/known-issues.md`.
