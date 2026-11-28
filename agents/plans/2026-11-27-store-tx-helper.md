# Tidy store transactions and Workspaces row closing

## Goal

Resolves #206. `internal/store/store.go` repeats the same `Begin` /
`defer Rollback` / `Commit` sequence in five functions, and `Workspaces()`
holds its first result set open, by its deferred `Close`, while it runs a
second query on a store limited to one connection. That works only because
`database/sql` closes a result set itself once `Next` reaches its end. After
the change, each transaction goes through one helper, and each result set in
`Workspaces()` is closed by the function that reads it before the next query
starts. The store's exported interface and its behavior do not change.

## Related code

- `internal/store/store.go:197` `upgrade` — takes a `*sql.DB`, not a `*Store`,
  because `Open` runs it before the `Store` exists.
- `internal/store/store.go:230` `Workspaces` — two queries, both deferred
  `rows.Close()` in one function.
- `internal/store/store.go:291` `AddSession`, `:348` `SetArchived`, `:464`
  `AddImages`, `:491` `DeleteImages` — the other four transactions.
- `internal/store/store_test.go` — `TestChildren`, `TestWorktrees`,
  `TestTitles`, `TestOpenRetiresPermissionModes`, and
  `TestUpgradeAddsSessionDirectory` read through `Workspaces()` and write
  through the transactions.

## Decisions

- **Construction:** the invariant "a transaction commits only when every
  statement succeeded, and is rolled back on every other path" lives in one
  function, `withTx(db *sql.DB, f func(*sql.Tx) error) error`: it begins,
  defers `Rollback`, runs `f`, and commits only when `f` returns nil. It is a
  package function on `*sql.DB` so `upgrade` can use it. The invariant "no
  result set is open when the next query starts" is enforced by giving each
  query in `Workspaces()` its own function whose deferred `Close` runs on
  return: a new unexported method, `loadWorkspaces`, reads the workspaces
  table, and `Workspaces()` keeps the sessions query, starting it only after
  that method has returned.
- `upgrade`'s early return for an already-upgraded database stays outside
  `withTx`; only the `ALTER`/`UPDATE` statements run inside it.
- `AddSession`'s argument checks stay before `withTx`, so a refused session
  opens no transaction.
- The `UserPrompts` iterator also holds a result set open across its caller's
  code; its doc comment already states the rule, and the issue does not cover
  it, so it is left as is.

## Test plan

No new test. The change alters no behavior any caller can observe: the old
`Workspaces()` already releases the connection on every path that reaches the
second query, since its first loop runs to the end of its result set, and
every error path returns before that query. A test that fails on the old code
would need a fault injected between the first loop and the second query, which
no input reaches. The existing tests guard the refactor:

- `TestChildren` stores a second child with a taken name, whose `children`
  insert fails after its `sessions` insert, and asserts `Workspaces()` reads
  two sessions, so it fails if `withTx` commits on an error from `f`.
- `TestUpgradeAddsSessionDirectory` covers `upgrade` through `withTx`.
- `TestImagesDeletedWithSession` covers `AddImages`; the server tests cover
  `SetArchived` and `DeleteImages`.
- Every `Workspaces()` test above covers the split loading.

## Implementation plan

1. In `internal/store/store.go`, add `withTx` beside `upgrade`, and rewrite
   `upgrade`, `AddSession`, `SetArchived`, `AddImages`, and `DeleteImages` to
   run their statements in it.
2. In the same file, move the workspaces query and its loop out of
   `Workspaces()` into `loadWorkspaces() ([]*Workspace, error)`, with its
   own `defer rows.Close()`. `Workspaces()` calls it, builds `byID` from the
   result, then runs the sessions query as today.
3. Run `make test`.

## Requirement changes

None.
