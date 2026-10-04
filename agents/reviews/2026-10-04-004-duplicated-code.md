# Duplicated code in the Ox server

## Scope and coverage

Reviewed every Go source file under `server/` for duplicated code:
`cmd/ox-server` and the `acp`, `agent`, `catalog`, `keyring`, `openrouter`,
`openroutertest`, `server`, `settings`, `shellproc`, `skills`, `store`,
`sysprompt`, `textfile`, `tools`, and `transcript` packages. The Rust client,
documentation, and examples are out of scope.

I used `golangci-lint` with only `dupl` enabled at thresholds of 20, 40, and 60
tokens to locate syntactic clones, then read each hit and its callers to judge
whether the repetition was worth removing. `dupl` finds only token-identical
blocks, so semantic duplication was found by reading. I left the idiomatic
repetitions alone: the tagged-union `MarshalJSON`/`UnmarshalJSON` methods and
the one-line `Completed`/`Failed`/`Cancelled` constructors in `transcript`, the
parallel content and reasoning blocks in `openrouter/stream.go`, and the
send-and-map-error blocks in `agent.go`. Removing those would cost more
indirection than the duplication does.

## Fixed

- **Saved-session read and not-found mapping**
  (`server/internal/server/server.go:231` and `:400`): `loadSession` and
  `setConfigOption` both read the store and mapped `store.ErrNotFound` to a
  resource-not-found error with the same six lines. A changed mapping, or a new
  store error, had to be handled twice. Extracted `storedSession`.
- **Active-session construction** (`server/internal/server/server.go:194` and
  `:247`): `newSession` and `loadSession` both built the process state from
  `sysprompt.ForWorkspace` plus `loadSkills` plus a `shellproc.Processes`, with
  the same error mapping. Extracted `newActiveSession`; each caller still sets
  the selections.
- **Settings file reading and obsolete-provider check**
  (`server/internal/settings/settings.go:67` and `:98`): `Load` and
  `ForWorkspace` both read the file, treated a missing file specially, rejected
  the obsolete `provider` key, and applied the file. Extracted `readSettings`,
  which reports whether the file exists. The two functions keep their different
  missing-file and `models` behavior.
- **Background-process state wording** (`server/internal/tools/shell.go:242` and
  `:263`): `listProcesses` and `renderProcess` both chose between `exited` and
  `stopped` from a `shellproc.State`. The two could disagree in the same process
  list. Extracted `stateVerb`.
- **A copy of a standard-library helper**
  (`server/internal/tools/shell.go:328`): `isRuneStart` was byte-for-byte
  `utf8.RuneStart` (`b&0xC0 != 0x80`). Removed it and used `utf8.RuneStart` in
  `trimFront`.
- **Termination signal context** (`server/cmd/ox-server/main.go:141` and
  `:214`): `serveACP` and `runHeadless` started `signal.NotifyContext` with the
  same three signals. A changed signal set had to be changed twice. Extracted
  `signalContext`.

## Findings

### low

#### simplicity

- **Duplicated title truncation** (`server/internal/store/store.go:286`,
  `server/internal/tools/tools.go:310`): `store.titleFromPrompt` and
  `tools.shorten` both cut a title to `maxTitleChars` (80) runes and mark the
  cut with `…`, and each package declares its own `maxTitleChars = 80`. They
  differ only in that `shorten` trims trailing spaces and tabs first. Changing
  the display width or the ellipsis rule means changing both, and the two
  results can drift for the same title text. `dupl` does not flag it because the
  surrounding loops differ. Suggested fix: share one truncation helper. This
  needs a decision, because `store` does not import `tools`: a shared helper
  needs a home (a small shared package, `transcript`, or keeping the two rules
  separate).
- **Duplicated effort and mode application**
  (`server/internal/settings/settings.go:134` and `:141`): the two blocks in
  `Settings.apply` parse a string, reject an unknown value, and assign the
  result; they differ only in the parsed type and the error text. A third key
  repeats the block. `dupl` flags them at a 20-token threshold. Suggested fix: a
  small type-parameter helper such as `applyChoice`. This needs a decision,
  because the helper adds a type parameter and function values where the plain
  blocks are simpler to read.

## Checks run

- `gofmt -l server` reported nothing.
- `cd server && go build ./...`, `go vet ./...`, and `go test ./...` passed.
- `make check` passed: documentation check, Go build/vet/tests, Rust formatting,
  tests, build, and Clippy.
- `golangci-lint` with only `dupl` enabled (thresholds 20, 40, 60) located the
  clones; every remaining hit was reviewed and is listed above or left as
  idiomatic.
- `make e2e` was not run: the change does not affect terminal behavior.

## Verdict

Six duplications were removed without changing behavior. Two low-severity
duplications remain; both are safe to leave and each needs a choice about the
shared form before it can be fixed.
