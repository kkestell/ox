# Small deslop fixes

## Goal

Fix five small findings from the deslop audit as one change:

- [F9](../audits/2026-11-20-002-deslop.md#f9-claude-startup-and-configuration-edge-outcomes), first item: a start that times out drops the plain text Claude printed about why it failed.
- [F18](../audits/2026-11-20-002-deslop.md#f18-the-catalog-accepts-choices-with-no-name): the configuration loads choices without a `name`, so the pickers show blank rows.
- [F19](../audits/2026-11-20-002-deslop.md#f19--is-cleaned-before-symlinks-resolve): `oxd -socket` and `workspace/list_folder`'s link classification remove `..` lexically before symlinks resolve.
- [F20](../audits/2026-11-20-002-deslop.md#f20-make-test-fails-on-a-fresh-checkout-and-the-metrics-hook-fails-on-duplicate-content): `make test` fails on a fresh checkout, and the metrics hook fails when two staged files are identical.
- [F23](../audits/2026-11-20-002-deslop.md#f23-small-dead-code-and-retained-state): small dead code and retained state.

All were rechecked against `0d2ee01`. One part is already fixed: the double approval withdrawal in F23's first bullet went with `ca73f2c` (`withdrawApprovals` is now called only in `sessionState` and `end`). Everything else still holds.

## Related code

- `internal/claude/agent.go:145-155` — `plainText` stores `starting.reason`; `startupTimeout` calls `giveUp` with the fixed timeout text only.
- `internal/claude/agent.go:31-34`, `:390`; `internal/claude/prompt.go:53-64`, `:152` — `prompting.input` holds a whole `agent.Input`, and its readers use only `MessageID`.
- `internal/config/config.go:79-115` — `Harness.check`, `checkOptions`, and `addChoice` check IDs only.
- `cmd/oxd/main.go:36-41` — `filepath.Abs` cleans the `-socket` path, while `ox` dials it as given.
- `internal/server/files.go:54-56` — `listFolder` stats a link at `path.Join(folder, e.Name())`, which removes `..` before `os.Root` resolves the path.
- `internal/server/methods.go:123-126` — `workspace/create` already keeps its path as given, for the same reason.
- `Makefile:12-20` — `test: lint web`; `lint`'s `go fix -diff ./...` compiles `cmd/ox`, whose `//go:embed web/dist` needs `web` built first.
- `scripts/metrics.py:105-117` — reads `by_file[path]` for every measured path; `cloc` merges files with identical contents unless told not to.
- `internal/server/server.go:105-125`, `mcp.go:29-33`, `:51-58`, `methods.go:30-38` — the first line on a connection is parsed by `attach` and again by `handle`; `handle` and `handleMCP` each parse and drop requests without an ID.
- `cmd/ox/web/src/SettingsFields.tsx:219-229`, `NewSessionView.tsx:22-30` — `defaultSettings` builds model and effort that its only caller ignores.
- `cmd/ox/web/src/poll.ts:7` — `Polled` is exported and imported nowhere.
- `cmd/ox/web/src/App.tsx:33-53`, `NewSessionView.tsx:9-14`, `ox.tsx:297-321` — a draft takes a control from `ox.control(id)`. Removing its workspace hides the draft through `liveDrafts`, but the draft stays in `drafts` and its control stays in `controls` until the page reloads.

## Decisions

- **Startup timeout text.** When Claude printed a reason before the timeout, the exited error is `no startup response within one minute`, a newline, then Claude's text, the same shape `exit` uses for stderr. A lost conversation still wins through `startFailed`.
- **Nameless choices.** `oxd` refuses to start, naming the kind of choice and its id, the same way it refuses a missing id. It does not fall back to showing the id.
- **Relative `-socket`.** Still supported. `oxd` prefixes the working directory without cleaning. An absolute path is used exactly as given.
- **Duplicate files in metrics.** `cloc --skip-uniqueness` counts every tracked file, so identical files each count toward the totals, the way `git ls-files` lists them.
- **Parsing at the boundary.** `serveConn` parses each line once, replies to an invalid one, and drops one without an ID. `attach`, `handle`, and `handleMCP` take the parsed `jsonrpc.Request`. Behavior stays the same: an `mcp/attach` without an ID was already dropped by `handle`.
- **Draft cleanup.** `Layout` drops the drafts of removed workspaces and releases their controls through a new `ox.release(id)`, which deletes the control from `controls`. `liveDrafts` stays as the rendering filter, so a removed workspace's draft disappears in the same render that removes the workspace. A create in flight for that workspace finishes against a released control, and nothing shows it.
- **Requirements:** **Failed startup**: a timed-out start now shows Claude's words when it printed any. **Configuration checks** and **New-session choices**: a configuration whose pickers would show blank rows is refused at startup. **File locations**: `-socket` names the file the filesystem resolves, the same file `ox` dials. **File tree**: unchanged for the page, which never sends `..`. Each fix affects a single startup or a single reply, with no request lifecycle of its own.
- **Construction:** Every catalog choice has a nonempty `id` and `name` once `config.Load` returns. A request past `serveConn` is a parsed `jsonrpc.Request` with an ID. `prompting` holds only the message ID that its readers use.
- **Trust boundaries:** `-socket` comes from whoever starts `oxd`. The change makes `oxd` bind the path that `ox` and `ox-mcp` dial. `listFolder` still goes through `os.Root`, so no listed or classified path leaves the workspace.

## Check your assumptions

None.

## Test plan

- Startup timeout after plain text: Claude harness. Add cases to the table in `TestStartupFailureKeepsClaudesReason` (`internal/claude/claude_test.go`). `plainText("Not logged in · Please run /login")` then `startupTimeout{}` reports `no startup response within one minute\nNot logged in · Please run /login`. `gone` then `startupTimeout{}` reports `lostConversation`. `TestStartupLimit` keeps the bare timeout text when Claude printed nothing.
- Nameless choices: config. Extend `TestChoiceIDs` into `TestChoiceIDsAndNames`, adding a model, an effort level, and a permission mode with `"name":""` and with `name` omitted. Each must fail to load.
- `-socket` with `..` after a symlink: `cmd/oxd`. Add `TestSocketPathResolvesLinksBeforeDotDot` in `cmd/oxd/main_test.go`. In a short `os.MkdirTemp` dir, create `other/inner` and `link -> other/inner`. Pass `dir/link/../a.sock` through the new path function into `listen`, and assert that `dir/other/a.sock` exists and `dir/a.sock` does not. Then `t.Chdir(dir)` and do the same with `link/../b.sock`. No existing test runs the path handling without starting the whole daemon.
- Link classification under `..`: server. Extend the `workspace/list_folder` test in `internal/server/methods_test.go` (around `:1100-1160`). Create `src/nested/`, `src/sibling -> nested`, and `alias -> src/nested`. Add `alias: true` to the root's expected map, and update `list("src")` to `{"main.go": false, "nested": true, "sibling": true}`. Assert `list("alias/..")` equals the same map as `list("src")`. Before the fix, `sibling` is classified against the root and lists as `false`.
- `make test` on a fresh checkout, and the hook with a duplicate file: no test layer covers the Makefile or `scripts/metrics.py`. Check by hand: in a `git worktree add` of the change with no `node_modules` or `web/dist`, `make test` passes. Then stage a copy of a Go file and run `python3 scripts/metrics.py`, which completes and counts both files. Remove the worktree afterward.
- Parse-once, `prompting` message ID, `defaultSettings`, `Polled`, draft release: refactors and cleanup of state the page cannot observe. Existing coverage carries them: the MCP and invalid-request tests in `internal/server` and `cmd/ox-mcp`, the prompt and approval tests in `internal/claude`, and the new-session and draft specs in `make e2e`.

## Implementation plan

1. `internal/claude/agent.go`: in the `startupTimeout` case, build the reason as `"no startup response within one minute"`, and append `"\n" + s.reason` when `s.reason` is set. Replace `prompting.input agent.Input` with `messageID string // the latest input written, which the releasing update names`, and update `end` (`p.messageID`). In `prompt.go`, `write` sets `p.messageID = input.MessageID`, and `sessionState` reads `p.messageID`.
2. `internal/claude/claude_test.go`: add the two table cases.
3. `internal/config/config.go`: `addChoice(seen, kind, id, name string)` also returns `every %s needs a name` (with the id) for an empty name. Pass `m.Name` and `o.Name`, and update `check`'s comment to say every choice needs an id and a name. Extend `TestChoiceIDs` in `config_test.go` as above.
4. `cmd/oxd/main.go`: replace `filepath.Abs` with a small `absolute(path string) (string, error)`. It returns an absolute path unchanged, and otherwise returns `os.Getwd()` + separator + path. Its comment says it keeps `..` so the filesystem resolves symlinks first, as `ox` does when it dials. Add the test to `cmd/oxd/main_test.go`.
5. `internal/server/files.go`: stat a link at `folder + "/" + e.Name()` (`folder` is `"."` for the root) and drop the `path` import. Extend the `list_folder` test.
6. `internal/server/server.go`, `mcp.go`, `methods.go`: in `serveConn`, parse each line with `jsonrpc.Parse`, `reply` an invalid one, and skip one with no ID. Then call `attach(c, reply, request)` on the first line, and `handleMCP(reply, sessionID, request)` or `handle(reply, request)` after that. Remove the parsing and ID checks from those three, and move the comment about requests needing an ID to `serveConn`.
7. `Makefile`: `lint: web` (and drop `cmd/ox/web/node_modules` from `lint`, since `web` depends on it). `test: lint`.
8. `scripts/metrics.py`: add `--skip-uniqueness` to the `cloc` call.
9. `cmd/ox/web/src/SettingsFields.tsx`: replace `defaultSettings` with `defaultPermissionMode(name, harness, defaults?)`, which returns `defaults.permission_mode` when `defaults.harness === name`, else the harness's first permission mode's id or `""`. Update `NewSessionView.tsx`, and drop imports that become unused.
10. `cmd/ox/web/src/poll.ts`: make `Polled` a module-local type.
11. `cmd/ox/web/src/ox.tsx`: add `release(id)`, which deletes the page's control of a session that will never exist. Export it next to `control`. `cmd/ox/web/src/App.tsx`: add a `createEffect` whose compute returns the drafts whose workspace is no longer in `ox.state.workspaces`. Its apply removes them from `drafts` and calls `ox.release(d.id)` for each. Reword the `liveDrafts` comment to say it hides a removed workspace's draft until the effect drops it.
12. `docs/protocol.md` (Configuration): "and a `name` to show" becomes "and a nonempty `name` to show".
13. `docs/todo.md`: remove the entries for F9 (first item), F18, F19, F20, and F23.

## Requirement changes

None.

## Documentation updates

- `docs/protocol.md`: every choice's `name` is required to be nonempty.
- `docs/todo.md`: remove the five entries this change fixes.
