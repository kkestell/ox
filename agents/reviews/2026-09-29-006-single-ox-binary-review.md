# Single ox binary review

## Scope and coverage

Commit `3418219`, which merges `ur` into one `ox` binary with `run`, `acp`, and
`auth` subcommands and one global settings file. Reviewed the new
`crates/ox-server/src/lib.rs` and `crates/ox/src/main.rs`, the client settings
changes in `crates/ox/src/config.rs`, the renamed server modules against their
`crates/ur` originals, the tmux test setup, the Makefile, the release workflow,
`scripts/run.py`, and the edited docs, against the plan
`agents/plans/2026-09-29-005-single-ox-binary.md` and its work log. Searched the
tree for leftover `ur`, `Ur`, `UR_`, and `XDG_CONFIG_HOME` names; none remain.

Lenses: correctness, simplicity, error-handling, documentation.

Not covered: Zed's terminal authentication through `ox acp auth login`, which
the work log already lists as untested.

## Fixed findings

- **The headless run resolved its directory by hand**
  (`crates/ox-server/src/lib.rs:77`): `absolute_dir` joined a relative `--dir`
  onto the current directory before calling `canonicalize`, which already
  resolves relative paths, and `run` took `Option<&Path>` to mean the current
  directory. `ox run --dir` now defaults to `.`, like the client's `--dir`, and
  `run` canonicalizes the path it is given. `absolute_dir` is deleted.
- **A missing `ox run --dir` did not name the directory**
  (`crates/ox-server/src/lib.rs:84`): `ox run --dir /typo Hi` printed only
  `ox: No such file or directory (os error 2)`. It now prints
  `ox: opening /typo: No such file or directory (os error 2)`, as the client
  does for its `--dir`.

## Findings

### Low

#### Documentation

- **OX-0013: AGENTS.md says install targets recreate the database**
  (`AGENTS.md:62`): The Backwards Compatibility section ends "local install
  targets do this", but `make install` and `make install-release` only install
  the binary and copy the example settings file; no Makefile target has removed
  the database since an earlier commit dropped it. An agent that relies on the
  sentence will leave an old `ox.db` in place. Either delete the clause or add
  the removal back to `do-install`.

## Checks run

- `make check` — passed.
- `target/debug/ox run --dir /nonexistent-dir Hi` — prints the directory in the
  error after the fix.
- `target/debug/ox acp auth --help` — lists `login` and `logout`.

## Verdict

The change meets its plan. Two small fixes were applied; one low documentation
issue is open.
