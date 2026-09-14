# Versioned binary releases

## Goal

Publish installable Ox binaries without adding a packaging framework or release
logic to the agent runtime.

## Desired outcome

Pushing a stable `vMAJOR.MINOR.PATCH` tag after the required gate passes creates
one GitHub Release containing CGO-free archives for macOS and Linux on amd64 and
arm64, plus a checksum file covering every archive. Each released binary reports
the tag-derived version through `ox --version`, and the user documentation
explains how to select, verify, install, and configure an archive.

## Summary of approach

Keep the executable's development version in `cmd/ox` and replace it at link
time for releases. Add an informational `--version` path that exits before
configuration, credentials, or ACP startup. One tag-triggered GitHub Actions
workflow validates the tag, installs pinned check tools, runs `make check`,
cross-builds the four targets directly with the Go toolchain, archives them,
verifies their checksums and the runnable host artifact, and publishes the
assets only after all preceding work succeeds.

This slice publishes standalone archives only. It does not add auto-update,
Homebrew or other package-manager publishing, Windows builds, installers,
containers, universal macOS binaries, code signing, or notarization.

## Related code

- `cmd/ox/main.go` - Owns command parsing and the version supplied during ACP
  initialization.
- `internal/e2e` - Provides the shipped-process boundary for informational
  command coverage.
- `Makefile` - Defines the required repository gate that releases must run.
- `.github/workflows/` - Will own tag validation, artifact construction, and
  GitHub Release publication.
- `README.md`, `docs/zed.md` - Currently describe installation only from a
  checkout.
- `docs/spec.md` - Owns the observable distinction between informational output
  and ACP server stdout.

## Current state

- `version` is fixed at `0.0.1`; it is logged and returned during ACP
  initialization, but users cannot inspect it without starting an ACP
  connection.
- `make install` builds the current checkout into `$HOME/.local/bin`; there is
  no CI or release workflow.
- The command and all production dependencies build with `CGO_ENABLED=0` for
  `darwin/arm64`, `darwin/amd64`, `linux/arm64`, and `linux/amd64`.
- Ox's Unix-only contract already excludes Windows. The ACP client owns process
  supervision, so distribution does not add a launcher.
- The closest local prior art tests `--version` at the shipped-process boundary,
  but none of the reference agents has release automation worth carrying into
  Ox.

## Structural considerations

- **Hierarchy:** `cmd/ox` continues to own process identity; repository
  automation owns packaging and publication.
- **Abstraction:** Direct Go builds and standard archive/checksum tools are
  sufficient for four fixed targets, so no GoReleaser configuration or release
  package is introduced.
- **Modularization:** One workflow keeps an unprivileged verification job
  separate from its write-enabled publication job without introducing reusable
  workflow layers.
- **Encapsulation:** Link-time injection changes the existing version value and
  does not expose release concerns to agent or protocol packages.
- **Testability:** Informational command behavior is covered through the built
  executable, while the workflow verifies the exact version and checksums it is
  about to publish.

## Test plan

- Prove that `ox --version` exits successfully, writes exactly one version line
  to stdout, writes nothing to stderr, and does not require a home directory,
  settings, credentials, stdin, or provider access.
- Keep malformed flags and positional commands on the existing usage-error path.
- In the release job, run the Linux amd64 artifact on its native runner and
  compare its output with the version derived from the tag.
- Check all four archives are present, contain one executable named `ox`, and
  are covered by a `SHA256SUMS` file that verifies successfully.
- Do not test GitHub's release service or execute cross-compiled binaries under
  emulation.

## Implementation plan

- Replace the misleading fixed development version with `devel`, add `--version`
  to command parsing and usage, and return its conventional `ox <version>`
  output before any server initialization. Keep the same version value as the
  source for logs and ACP initialization.
- Add shipped-process coverage for the informational path and its independence
  from runtime prerequisites.
- Add one release workflow triggered by version tags. Validate an exact stable
  semantic-version tag, use the Go version declared by `go.mod`, pin the dprint
  and staticcheck versions needed by `make check`, and pin referenced actions to
  immutable revisions. Keep validation, tests, builds, and packaging in a job
  with read-only repository access.
- After `make check`, build each target with `CGO_ENABLED=0`, `-trimpath`, and
  stripped linker flags that inject the version without the leading `v`. Package
  each binary as `ox_<version>_<goos>_<goarch>.tar.gz` and generate `SHA256SUMS`
  over the four archives.
- Pass the staged artifacts to a publication job, verify their checksums again,
  then create the GitHub Release from the existing tag with generated release
  notes and all five assets. Grant write access to repository contents only to
  that job.
- Add an installation guide for archive selection, checksum verification,
  placement on `PATH`, `ox --version`, `ox login`, upgrades by binary
  replacement, the Unix-only target, and the unsigned/not-notarized status of
  this first release path. Point the README and Zed guide to that owner instead
  of duplicating the instructions.
- Run one completeness and simplification review of the release item, fix its
  findings, rerun affected gates, and only then mark the todo item complete.

## Documentation updates

- `docs/spec.md` distinguishes informational commands from server mode's
  ACP-only stdout contract.
- A focused installation guide owns released-binary installation and manual
  upgrade instructions.
- `README.md` links the released installation path; `docs/zed.md` consumes it
  and retains only Zed-specific configuration.
- `AGENTS.md` gains a release command only if implementation adds a local
  developer-facing command rather than keeping packaging entirely in the
  workflow.

## Impact assessment

- Code paths affected: Top-level argument parsing and startup only.
- Data, protocol, or schema impact: Released builds advertise a tag-derived ACP
  agent version; no durable format or ACP shape changes.
- Dependency or API impact: No production dependency. Release automation uses
  pinned development tools and GitHub's existing release service.

## Validation

- Run the focused `cmd/ox` and shipped-process tests while implementing.
- Run `make check` before staging artifacts and again after review findings that
  affect behavior.
- Exercise the workflow's packaging commands locally against a temporary
  directory, inspect the archives, and verify `SHA256SUMS`; do not publish a
  test release from the implementation branch.
