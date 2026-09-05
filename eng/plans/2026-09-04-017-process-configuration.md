# Process Configuration

## Sources

- `docs/spec.md#process-configuration-transition` — process settings, CLI
  precedence, credential input, and environment-removal contract
- `docs/spec.md#authentication` — credential verification and secrecy contract
- `eng/roadmap.md#process-configuration` — slice scope and completion gates
- `eng/architecture.md#configuration-and-credentials` — ownership of process,
  settings, credential, and activation inputs
- `cmd/ox/main.go` and `internal/settings/{settings,resolve}.go` — current
  startup wiring and file-based model resolution
- `internal/credentials/credentials.go` — current cached keyring-backed
  credential boundary
- `internal/e2e/harness_test.go`, `internal/e2e/browser/harness.ts`, and
  `evals/internal/eval/client.go` — process harnesses that currently depend on
  private environment seams

## Goal

Move all shipped process controls to validated global settings and public CLI
flags, and make an explicit secure credential file the only alternative to the
OS keyring. Migrate every process harness and guide so the Ox binary no longer
uses `OX_*` or `OPENROUTER_*` variables for configuration or authentication.

## Implementation

- `internal/settings` — add the global-only `process` object and resolved
  process defaults for log level, OpenRouter base URL, and trace. Decode global
  and workspace layers through distinct entry points so the global file accepts
  `process` while a workspace file rejects it. Preserve per-activation rereads
  of model settings and rename the model override source from environment to
  CLI.
- `cmd/ox` — replace positional special cases and environment reads with one
  standard-library flag parser for `--log-level`, `--openrouter-base-url`,
  `--trace`, `--model`, `--credential-file`, and `--no-keyring`, followed by the
  optional `login` command. Resolve CLI values over the global process object
  over defaults before creating logging, trace, credentials, provider, or agent
  state. Report flag and global process failures on stderr before serving ACP.
- `internal/credentials` — construct a store from immutable startup inputs
  instead of reading environment variables. Add credential-file loading that
  rejects symlinks and non-regular files, files not owned by the current user,
  group/other permission bits, and blank or multi-line values. Keep the loaded
  secret only in memory. A file credential wins over the keyring and cannot be
  replaced by `login` or cleared by ACP `logout`; `--no-keyring` prevents all
  keyring reads and writes while still allowing a file credential.
- `internal/agent` and `cmd/ox/login.go` — update authentication, missing-model,
  login, and logout messages for public flags, credential files, and keyring
  behavior without exposing paths or credential contents where they are not
  needed.
- `internal/e2e`, `integration`, and `cmd/ox` tests — migrate helpers from
  private environment configuration to CLI flags and owner-only temporary
  credential files. Keep only platform environment needed for standard path
  lookup and subprocess behavior.
- `internal/e2e/browser/harness.ts` and `evals/internal/eval` — launch Ox with
  the same public flags and private credential files used by real clients. The
  live entry points may read the repository `.env`, but only the harness writes
  its value to a temporary file before starting Ox.
- `docs/settings.md`, `docs/zed.md`, and `evals/README.md` — document the
  shipped process object, flag precedence, secure credential-file/keyring
  workflows, and the harness-owned `.env` handoff.

## Tests

- Real-binary tests cover defaults and every CLI/global precedence path, invalid
  flags and process values, global parse failures at startup, workspace
  rejection of `process`, and the absence of configuration behavior from legacy
  environment variables.
- Credential tests cover an owner-only regular file, blank and multi-line
  content, permissive modes, wrong ownership, directories, symlinks, missing and
  unreadable paths, file-over-keyring precedence, keyring-disabled operation,
  login/logout refusal, provider use, and no secret leakage through stderr,
  trace, or durable records.
- Browser and evaluation smoke tests prove their fake-provider paths use public
  flags. Focused live-harness tests prove `.env` is consumed outside Ox and
  converted to a credential file without printing or recording the secret; no
  real provider request is made.

## Decisions

- Accept `debug`, `info`, `warn`, and `error` log levels case-insensitively.
  Require the provider override to be an absolute HTTP(S) URL with a host and no
  user information, query, or fragment. Empty explicit flag values are errors.
- Treat a credential as one trimmed nonempty line. Enforce current-user
  ownership and no group/other permissions before accepting it, with
  platform-specific metadata checks kept inside the credential boundary.
