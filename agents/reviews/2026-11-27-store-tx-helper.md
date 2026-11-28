# Review: Tidy store transactions and Workspaces row closing

## Scope and coverage

Reviewed the uncommitted change to `internal/store/store.go` on branch
`store-tx-helper`, against base `9abac7624816c392605abea365b70bdbf6ee31e4`
and the plan `agents/plans/2026-11-27-store-tx-helper.md` (issue #206). The
working tree also holds the untracked plan; nothing else.

The change touches persistence but alters no schema, query, or caller-visible
behavior, so it was reviewed in this session without concern reviewers.
Checked: Intent, Construction, Correctness, Testing, Simplicity, Conventions,
Documentation.

Gaps:

- None material.

## Summary

No findings.

The change matches the plan's Decisions:

- `withTx(db *sql.DB, f func(*sql.Tx) error) error` begins, defers
  `Rollback`, and commits only when `f` returns nil. `upgrade`, `AddSession`,
  `SetArchived`, `AddImages`, and `DeleteImages` all use it, and no other
  `Begin` remains in the file.
- `upgrade`'s already-upgraded check stays outside the transaction, and
  `AddSession`'s argument checks run before it, so a refused session opens no
  transaction.
- `loadWorkspaces` owns the workspaces query and closes its result set on
  return. `Workspaces()` starts the sessions query only after that, so it no
  longer depends on `database/sql` closing rows at EOF under
  `SetMaxOpenConns(1)`.
- `UserPrompts` is left as is, as the plan decides.

No requirement changes, and `docs/functional-requirements.md` is untouched.
`docs/architecture.md` ("a child's row is written in the same transaction as
its session row") still holds.

## Checks run

- `make test`: passed (Go with the race detector, and the Node suites).
- `go vet ./internal/store/`: clean.
- Spot-check: changed `withTx` to commit even when `f` fails.
  `TestChildren` ("Workspaces read" three sessions) and
  `TestUpgradeAddsSessionDirectory` ("a failed upgrade changed the database")
  fail, so the commit-only-on-success invariant is protected. Reverted.
- No new test, as the plan's Test plan explains: the split in `Workspaces()`
  changes nothing a caller can observe without an injected fault. Accepted.
- `make e2e` and `make live`: skipped, because neither can observe a refactor
  of the store.

## Verdict

Ready to commit.
