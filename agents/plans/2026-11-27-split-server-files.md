# Split internal/server creation, reporting, titling, archiving, and transcripts by concern

## Goal

`internal/server/methods.go`, `session.go`, and `mcp.go` each mix several
concerns, and session creation is split across `methods.go` and `mcp.go`, which
call back and forth. Move each concern into its own file so `methods.go` holds
the RPC methods, `session.go` the session goroutine and lifecycle, and `mcp.go`
the MCP connection and its tools. While moving, put rune truncation in one
helper and stop `takeReports` from computing and sorting the reporters twice.
No behavior changes. Resolves
[#204](https://github.com/kkestell/ox/issues/204).

## Related code

- `internal/server/session.go` — session goroutine and lifecycle, plus
  reporting (`reporting`, `report`, `told`, family delivery), labeling and
  titling, and family archiving.
- `internal/server/mcp.go` — MCP handshake and tools, plus half of session
  creation (`addChild`, `launch`, `discardUnpublished`, `checkChildLocked`,
  `checkStarterLocked`, `checkTitle`, `startedRecord`), transcript rendering,
  and `messageInput`/`reportsInput`.
- `internal/server/methods.go` — RPC methods, plus the other half of creation
  (`createTopLevel`, `storeSession`, `abandonWorktree`, `admitSessionLocked`,
  `existingLocked`, `publishLocked`).
- `internal/server/server.go:83` — calls `firstLine` when loading sessions;
  unchanged.
- Rune truncation today: `cut` (`mcp.go:766`), `shorten` (`session.go:133`),
  the report cut in `report` (`session.go:690`), and the titling input cut in
  `generateTitle` (`session.go:835`).
- `takeReports` (`session.go:787`) calls `readyLocked`, which builds and sorts
  `reportersLocked`, then calls `reportersLocked` again.
- `docs/architecture.md` ("Delivery") names `readyLocked`, `reportersLocked`,
  and `familyChangedLocked`; all three keep their names.

## Decisions

- **Scope.** Moves are verbatim apart from the two named tidies. The RPC
  handlers in `methods.go` (including the `session/create` handler
  `Server.createSession`, `onSession`, and the parked-session checks) stay as
  they are for #203. `changes.go` and `worktree.go` are untouched (#208). The
  MCP tool handlers `createSession`, `createTopLevelSession`, and
  `readSession` stay in `mcp.go`, beside the tool table; they parse arguments
  and call into `create.go` and `transcript.go`.
- **`archive.go`: taken.** Family archiving (`free`, `held`, `hold`,
  `archive`, `unarchiveLocked`, `setArchived`, `setArchivedLocked`,
  `isArchived`) is a self-contained concern of about 130 lines with its own
  locking story (the breadth-first hold). Moving it leaves `session.go` as the
  goroutine and its lifecycle, at the cost of a move.
- **Grouping `Server.mu`-guarded fields on `session`: not taken.** The struct
  already groups its fields under "Fixed at creation", "Guarded by
  Server.mu", and "Owned by run". A nested struct would add a prefix to every
  access without the compiler enforcing the lock.
- **Separating `reporting`'s lock-guarded and run-owned fields: not taken.**
  The same reason: `reports` and the turn-tracking fields are already split
  by comment inside the struct, and a second type would only move the
  comment.
- **One truncation helper.** `firstRunes(text string, n int) (string, bool)`
  returns text unchanged and false when it has at most n runes, else
  `string([]rune(text)[:n])` and true. It keeps the current `[]rune` slicing,
  so output is byte-for-byte the same. Each truncation becomes a caller:
  - `cut(text, n)` returns `first + "…"` when `firstRunes` cut, else text.
  - `report` keeps its count `n` and writes
    `fmt.Sprintf("%s\n(first %d of %d characters; read the rest with read_session on %s.)", cut(text, reportRunes), reportRunes, n, sess.id)`,
    the same text it writes today.
  - `shorten` returns line when it has at most `titleRunes` runes, else
    `strings.TrimRightFunc(first, unicode.IsSpace) + "…"` where `first` is
    `firstRunes(line, titleRunes-1)`.
  - `generateTitle` sets `prompt, _ = firstRunes(prompt, titleInputRunes)`.

  `firstRunes` and `cut` live in `transcript.go`, beside their largest user.
- **One reporters computation.** `readyLocked` returns
  `(reporters []*session, ready bool)`: the reporters from
  `reportersLocked` and whether sess takes their reports now, under the same
  conditions as today. `familyChangedLocked` reads `ready`; `takeReports`
  ranges over the returned reporters instead of calling `reportersLocked`
  again. Its doc comment says what it returns. The name stays, so
  `docs/architecture.md` ("Delivery") stays accurate.
- **Construction / Ownership.** No new state, lock, or invariant. Locking and
  goroutine ownership move with the functions unchanged.

## Check your assumptions

None.

## Test plan

- No behavior changes, so no new tests. Existing server tests cover every
  moved path and both tidies:
  - Truncation: `TestReadSession` (tool input cut, page cut including
    multibyte), `TestChildReports` (report cut to 400 runes),
    `TestSessionLabel` (label shortened to 47 runes plus ellipsis, titling
    input cut to `titleInputRunes`).
  - Delivery through `readyLocked`/`takeReports`: `TestChildReports`,
    `TestRefusedDeleteKeepsReports`.
  - Creation, archiving, and titling: the existing `mcp_test.go` and
    `methods_test.go` cases.
- `make test` passes. `git diff -M --color-moved=zebra` shows every moved
  function unchanged except `readyLocked`, `familyChangedLocked`,
  `takeReports`, `report`, `shorten`, `generateTitle`, and `cut`.

## Implementation plan

1. Create `internal/server/create.go` with, in this order: `created`,
   `createTopLevel`, `admitSessionLocked`, `existingLocked`, `storeSession`,
   `abandonWorktree`, `publishLocked` (from `methods.go`), then `addChild`,
   `launch`, `discardUnpublished`, `checkChildLocked`, `checkStarterLocked`,
   `checkTitle`, `startedRecord` (from `mcp.go`).
2. Create `internal/server/report.go` with `reporting`, `newReporting`,
   `reportsTo`, `reportRunes`, `report`, `told`, `reportsToLocked`,
   `reportersLocked`, `readyLocked`, `familyChangedLocked`, `deliverReports`,
   `takeReports` (from `session.go`), and `messageInput`, `reportsInput` (from
   `mcp.go`). Apply the `readyLocked` change and the `report` truncation
   change from Decisions.
3. Create `internal/server/title.go` with `titleRunes` (from `mcp.go`), and
   `firstLine`, `leadingLine`, `shorten`, `cleanTitle`, `labelLocked`,
   `broadcastLabelLocked`, `labelFrom`, the `titleInstructions` /
   `titleInputRunes` / `titleLimit` constants, `generateTitle`, `setTitle`
   (from `session.go`). Rewrite `shorten` and the cut in `generateTitle` on
   `firstRunes`.
4. Create `internal/server/transcript.go` with `pageRunes`, `toolInputRunes`,
   `timeLayout`, `transcriptPage`, `renderEntry`, `stateLine` (from `mcp.go`),
   the new `firstRunes`, and `cut` rewritten on it.
5. Create `internal/server/archive.go` with `free`, `held`, `hold`,
   `archive`, `isArchived`, `unarchiveLocked`, `setArchived`,
   `setArchivedLocked` (from `session.go`).
6. Remove the moved code from `session.go`, `mcp.go`, and `methods.go`, and
   trim each file's imports. `childName` and `errNotWorkspace` stay in
   `mcp.go`; `codedError` and `errWorkspaceNotFound` stay in `methods.go`;
   the `errArchived`/`errSessionNotFound`/`errDeleting`/`errShuttingDown`
   block and `newSession` stay in `session.go`.
7. Update the Source Map in `AGENTS.md` under `internal/server`, in
   alphabetical order:
   - `archive.go` — `# archiving a session and its family`
   - `create.go` — `# session creation and publication`
   - `report.go` — `# reports to a session's parent or creator`
   - `title.go` — `# session labels and generated titles`
   - `transcript.go` — `# read_session transcript pages`

   Keep the comment column aligned with the neighboring entries.
8. Run `make test` and the moved-code diff check above.

## Requirement changes

None.

## Documentation updates

- `AGENTS.md`: the Source Map entries in step 7.
