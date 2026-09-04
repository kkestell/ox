# 2026-08-17-007. Configuration precedence

## Goal

Ox reads exactly one setting from exactly one place: `OX_MODEL`, plucked out of
the process environment in `main.go` and handed to `agent.New` as a bare string.
There is no file to put a model in, so the only way to change models is to
restart Ox with a different environment, and every session in the process gets
the same one.

That is wrong in two ways. A person wants to set a model once, not in whatever
launches their editor. And a workspace wants its own model: the repository where
a cheap model is fine and the repository where it is not are two different
directories, and Ox already learns which directory a session belongs to when
`cwd` arrives with `session/new`.

Neither is reachable without deciding, once, where configuration comes from and
which layer wins.

## Desired outcome

Ox reads a model from a global configuration file, from a per-workspace
configuration file, or from `OX_MODEL`, and when more than one of them names a
model the more specific one wins: `OX_MODEL` over the workspace file over the
global file.

The global file is `$XDG_CONFIG_HOME/ox/config.json`, falling back to
`$HOME/.config/ox/config.json`. The workspace file is `<cwd>/.ox/config.json`,
where `cwd` is the canonical directory the client named in `session/new`. Both
are optional. An absent file is not a problem; a present file that cannot be
read, cannot be parsed, or names a key Ox does not know is a problem, reported
with the path of the offending file.

Each session resolves its own configuration when it is created, and keeps it for
its lifetime. Two sessions on two workspaces in one Ox process send different
models, and editing a configuration file after a session exists does not change
that session.

When nothing names a model, `session/new` fails with `-32603` and a message that
says how to fix it: set `OX_MODEL`, or set `"model"` in the files Ox actually
consulted, named by path.

`OX_LOG_LEVEL` and `OX_OPENROUTER_BASE_URL` stay environment-only. They
configure the process and its transport, not a session, and a configuration file
that could redirect Ox at another host would be a file that could exfiltrate a
prompt.

## Summary of approach

A new `internal/config` package owns the whole question: where the files are,
how one file decodes, and which layer wins. It exports four things — an
`Environment` value carrying what the process environment contributed, a
`GlobalPath` function, a `Resolved` value, and a `Resolve` function that folds
them together. Everything else — the workspace path, the per-file loader — stays
unexported, because nothing outside the package needs it and the vocabulary is
one key.

The vocabulary is one key, `model`, and that is the whole file. Ox's request
carries a model and messages and nothing else, so a richer vocabulary would be
keys with nowhere to go. The keys that belong to reasoning, provider routing,
compaction, tool selection, and the system prompt arrive with the work that
consumes them; each is a field on `Config` and a case in `Resolve`, and the
shape here is built so that is all they are.

Precedence is one `switch` rather than a merge pass. With one key, per-field
merging would be a fold over a single field, and the rule reads better stated
once: the override, then the workspace layer, then the global layer, then the
error. A second key is a second `switch`; a merge helper is what to write when
nested objects appear, not before.

Resolution happens in `NewSession` and its result is frozen onto the session.
Both files are read there rather than at startup, because Ox does not learn a
workspace until `cwd` arrives, and reading both in one place keeps the two
layers on one rule with no cache to invalidate. Freezing is what makes two
workspaces in one process coherent: `Prompt` reads the session's model, not the
agent's, and `Agent` stops holding a model at all.

`Resolved` also records which layer supplied the model. That is what lets the
not-configured error name the files it consulted, and what lets the
session-created log line answer "why that model" without the reader guessing.

The end-to-end harness gains the ability to seed files into the scratch
directory before Ox starts, which is the only new thing it needs: it already
points `HOME` and `XDG_CONFIG_HOME` at per-test scratch paths, and the mock
model already records the `model` field of every request it receives.

## Related code

- `~/src/references/repos/personal/alpha/runtime/internal/settings/settings.go:1-9`
  — the package doc states the boundary this plan adopts: the vocabulary is a
  deliberate allowlist of wire keys, so "a settings file cannot name a base URL,
  a header, or a credential". `:80-97` is `GlobalPath`/`WorkspacePath` as pure
  functions over environment values rather than readers of the environment,
  which is what makes them testable. `:103-124` is the per-layer loader: absent
  file yields no layer, whitespace-only file yields an empty layer, unknown key
  is a hard error, and every failure names the file.
- `~/src/references/repos/personal/alpha/runtime/internal/settings/resolve.go:65-83`
  — the precedence switch, override then workspace then global then
  `ErrNoModel`, with the winning layer recorded. Ox's `Resolve` is this switch
  with the merge step folded in.
- `~/src/references/repos/personal/alpha/runtime/internal/agent/agent.go:723-762`
  — `resolveSettings`, and the comment explaining the timing decision this plan
  copies: "Both files are read here rather than at startup: the runtime does not
  learn a workspace until cwd arrives, and re-reading per session keeps the two
  layers on one rule with no cache to invalidate." It also accumulates the list
  of consulted paths so the not-configured message can name them.
- `~/src/references/repos/personal/alpha/runtime/cmd/amber-runtime/main.go:26` —
  the command reads the environment and hands the resolved path down; the
  settings package never calls `os.Getenv`.
- `~/src/references/repos/personal/theta/internal/xdg/xdg.go:26-42` — `OxDir`,
  the XDG rule stated once: the `XDG_*_HOME` variable is honored only when
  absolute, otherwise `$HOME/<fallback>`, and `""` when neither yields an
  absolute base so the caller skips the file rather than writing a relative
  path. Theta earns a package for it by having three callers, config, data, and
  cache; Ox has one, so the rule lives in `internal/config` until the session
  log and the model catalog want it.
- `~/src/references/repos/personal/theta/internal/config/workspace.go:16-21` —
  the workspace path is `<workspace>/.ox/config.json` with no upward walk,
  because "a parent's .ox would belong to a different workspace". Same file
  name, same rule.
- `~/src/references/repos/personal/theta/internal/config/config.go:203-219` —
  decoding with `DisallowUnknownFields` and treating a whitespace-only file as
  empty, with the path in every error.
- `~/src/references/repos/personal/beta/src/config.rs:1-5` — states why an ACP
  agent's configuration is startup-shaped rather than task-shaped: "there are no
  per-task inputs, since a prompt and its cwd arrive per session over the ACP
  channel, not on the command line." Ox has no command line at all, which
  removes the CLI layer Beta has and leaves the three layers above.
- `~/src/references/repos/personal/beta/src/xdg.rs:12-26` — the same XDG rule in
  Rust, written once and shared by three base directories. Recorded as the shape
  to extract into when Ox has more than one caller.
- `~/src/references/repos/third-party/protocol/agent-client-protocol/docs/protocol/v1/session-config-options.mdx`
  — `configOptions` is how an agent offers a client a live model selector, with
  `session/set_config_option` to change it. That is a separate roadmap item and
  this plan does not build toward it beyond the thing it already needs: a
  session's configuration is one value, resolved once, owned by the session.

## Current state

- Relevant existing behavior: `main.go` reads `OX_MODEL` and passes it to
  `agent.New`, which stores it as `Agent.model`. `NewSession` rejects a session
  with `-32603` and `"no model is configured: set OX_MODEL"` when it is empty,
  and rejects one with the same code when `a.client.APIKey` is empty. `Prompt`
  reads `a.model` for the request and for its "prompt started" log line. Those
  four sites in `cmd/ox/main.go`, `internal/agent/session.go`, and
  `internal/agent/prompt.go` are every use of the model.
- Existing patterns to follow: `internal/acp` validates external input at the
  boundary and returns human-readable problems; the agent turns those into
  `jrpc2.Errorf`. `session` holds `id` and `cwd` as fields set once at creation
  and read without the mutex, which is exactly the shape a frozen configuration
  wants.
- Constraints from the current implementation: `canonicalDirectory` resolves
  symlinks and confirms the directory before a session exists, so the workspace
  file's path must be built from its output rather than from the raw request.
  `session/new` is the first point at which Ox knows a workspace, so nothing
  workspace-scoped can be resolved at startup.
- The harness already sets `HOME` to a per-test scratch directory and
  `XDG_CONFIG_HOME` to `<scratch>/config`, and runs Ox with the scratch
  directory as its working directory, so both configuration paths already land
  inside the test's own temporary space. What it cannot do is put a file there
  before Ox starts: `startConfig` carries environment overrides and nothing
  else.
- `modelRequest` in `internal/e2e/model_test.go` already decodes the `model`
  field of every request the mock model receives, and `requestFor(prompt)`
  already finds a request by its final message, so asserting which model a
  particular session sent needs no new harness machinery.

## Structural considerations

- **Hierarchy:** `cmd/ox` reads the environment and owns the knowledge that
  `XDG_CONFIG_HOME`, `HOME`, and `OX_MODEL` exist. `internal/config` owns file
  layout, decoding, and precedence, and never calls `os.Getenv`.
  `internal/agent` owns when resolution happens and what a failure looks like on
  the wire. `internal/config` depends on nothing of Ox's.
- **Abstraction:** the config package returns a value, not a policy. It does not
  log, does not decide an error code, and does not know that a session exists.
  The agent decides that a configuration failure is `-32603`, because the agent
  is what speaks JSON-RPC.
- **Modularization:** one new package with one file. Alpha splits loading,
  merging, and resolution across three files because its vocabulary has nested
  reasoning and provider objects; Ox's is one key, and three files for it would
  be a nano-module each. The split arrives with the vocabulary that needs it.
- **Encapsulation:** the exported surface is `Environment`, `GlobalPath`,
  `Source`, `Resolved`, and `Resolve`. The workspace path and the per-layer
  loader stay unexported, so no caller can consult one layer and skip the other,
  and no caller can construct a `Resolved` that skipped precedence.
- **Testability:** `GlobalPath` takes the two environment values as arguments,
  so its rule is unit-testable without touching the process environment.
  `Resolve` takes a directory, so layering is unit-testable against temporary
  directories. Everything the user sees — which model reaches the provider,
  which file an error names — is observable at the process boundary, because the
  harness can now seed both files and already records every request's model.

## Refactoring

- `Agent.model` becomes `Agent.environment config.Environment`, and `agent.New`
  takes that value where it took the model string. The parameter count does not
  change and the two environment-derived inputs stop being loose strings that
  can be passed in the wrong order.
- `session` gains a `configuration config.Resolved` field, set at creation and
  read without the mutex like `id` and `cwd`. `Prompt` reads
  `value.configuration.Model`. Sequence this with the field change above; they
  are one edit.
- `testAgent` in `internal/agent/agent_test.go` constructs the agent with a
  `config.Environment` whose `ModelOverride` is `"test/model"`, preserving what
  it asserts today.

## Test plan

- **Key behaviors to verify:**
  - Precedence, one layer at a time: a model in the global file alone is used; a
    workspace file overrides it; `OX_MODEL` overrides both. Each case asserts
    the model the mock model actually received, not a log line.
  - Where the files are. `$XDG_CONFIG_HOME/ox/config.json` is read when
    `XDG_CONFIG_HOME` is absolute, and `$HOME/.config/ox/config.json` is read
    when it is unset. `<cwd>/.ox/config.json` is read for the directory
    `session/new` named, and a `.ox/config.json` in that directory's parent is
    not.
  - Two sessions, two workspaces, one process. Two directories with different
    workspace files, a session in each, a prompt in each, and each request
    carries its own model. This is the reason the configuration is per session
    rather than per process.
  - Freezing. Rewriting the workspace file after `session/new` does not change
    the model that session sends, and a session created afterwards picks up the
    new value.
  - Nothing configured. `session/new` fails `-32603` with a message naming
    `OX_MODEL` and both consulted paths.
  - Bad files. Malformed JSON, an unknown key, and a wrong-typed `model` each
    fail `session/new` with `-32603` and a message naming the offending file,
    from either layer. Ox keeps answering afterwards.
  - Harmless files. An absent file, an empty file, and a whitespace-only file
    are all not-a-layer rather than errors, and fall through to the layer below.
  - Blank values. A `"model"` of `""` or `"   "` in a file is rejected naming
    the file; a blank or whitespace-only `OX_MODEL` is not an override and falls
    through to the files, matching how an unset variable behaves.
  - No usable base directory. With neither an absolute `XDG_CONFIG_HOME` nor a
    `HOME`, there is no global layer, and the not-configured message names only
    the workspace file rather than a relative path.
- **Test levels:** `GlobalPath`, the loader's file-shaped cases, and the
  precedence switch get unit tests in `internal/config` against temporary
  directories. Everything about which model reaches the provider, which error
  code reaches the client, and two sessions differing is end-to-end, because the
  thing under test is what the process does with the files on disk and the
  environment it was started in.
- **Edge cases and failure modes:** a workspace file under a `cwd` that is a
  symlink, which must resolve against the canonical directory; a `.ox` that is a
  file rather than a directory, and a `config.json` that is a directory, both of
  which are read failures naming the path; a session in a workspace with a bad
  file while another session in a good workspace is mid-turn, which must fail
  only the new session.
- **What not to test:** that `encoding/json` decodes JSON; the XDG specification
  beyond the one rule `GlobalPath` states; the wording of log lines; file
  permissions, since Ox only reads.

## Implementation plan

1. Add `internal/config/config.go`: the package doc stating the three layers and
   their order; `Config` with `Model *string` and a comment on why the field is
   a pointer; `Source` with the three constants whose values read as message
   fragments (`OX_MODEL`, `workspace configuration`, `global configuration`);
   `Environment` with `GlobalPath` and `ModelOverride`; `Resolved` with `Model`
   and `ModelSource`.
2. Add `GlobalPath(xdgConfigHome, home string) string` implementing the XDG
   rule, returning `""` when neither input yields an absolute base, and the
   unexported `workspacePath(dir string) string` returning
   `<dir>/.ox/config.json` with the comment recording that there is deliberately
   no upward walk.
3. Add the unexported `load(path string) (*Config, error)`: `""` and a missing
   file yield `nil`, a whitespace-only file yields an empty layer, decoding uses
   `DisallowUnknownFields`, and every error names the path.
4. Add `Resolve(environment Environment, cwd string) (Resolved, error)`: load
   the global layer when `environment.GlobalPath` is non-empty, load the
   workspace layer, collect the paths actually consulted, then the precedence
   switch. A blank model in a file is an error naming that file. Nothing
   anywhere produces the not-configured message, built from `OX_MODEL` and the
   consulted paths.
5. Add `internal/config/config_test.go` covering the unit-level items in the
   test plan.
6. Replace `Agent.model` with `Agent.environment config.Environment`, change
   `agent.New`'s third parameter to match, and update `testAgent`.
7. In `NewSession`, resolve after `canonicalDirectory` and before the API key
   check, map a resolution failure to `jrpc2.InternalError`, store the result on
   the session, and extend the "session created" log line with the model and its
   source. Delete the `a.model == ""` check, which `Resolve` now owns.
8. In `Prompt`, read `value.configuration.Model` for the request and the log
   line.
9. In `cmd/ox/main.go`, build the `config.Environment` from
   `config.GlobalPath(os.Getenv("XDG_CONFIG_HOME"), os.Getenv("HOME"))` and
   `os.Getenv("OX_MODEL")`, log the resolved global path at startup, and pass
   the value to `agent.New`.
10. Extend the harness: `startConfig` gains a map of scratch-relative paths to
    contents, `start` writes them (creating parent directories) after applying
    every option and before launching Ox, and `withFile`, `withGlobalConfig`,
    and `withWorkspaceConfig` are the options that populate it.
11. Add `internal/e2e/config_test.go` with the end-to-end items in the test
    plan, and update `TestNewSessionRequiresAModelAndAnAPIKey` so its `OX_MODEL`
    case also asserts that the message names both consulted files.

## Documentation updates

- `AGENTS.md`: check off the configuration precedence roadmap item.
- `AGENTS.md`: add a Configuration section stating the two file paths, the
  precedence order, that `model` is the only key, that an unknown key is an
  error, and that `OX_LOG_LEVEL` and `OX_OPENROUTER_BASE_URL` are
  environment-only and deliberately not settable from a file. This is a new
  user-facing surface and `AGENTS.md` is where Ox's owned facts live.
- `AGENTS.md` Tests section: note that the harness can seed files into the
  scratch directory before Ox starts, which is how a test supplies a
  configuration file.

## Impact assessment

- Code paths affected: `cmd/ox/main.go`, `Agent`'s fields and constructor,
  `NewSession`, `Prompt`'s request construction and log line, and the e2e
  harness. The OpenRouter client, the ACP types, the session's locking, and the
  turn are untouched.
- Data, protocol, or schema impact: no ACP message changes shape and no method
  is added. A new on-disk format appears — a JSON object with one optional key —
  and it is read only, never written. The observable protocol difference is that
  `session/new` can now fail with a configuration message it could not produce
  before, and that two sessions in one process can send different models.
- Dependency or API impact: none. `encoding/json`, `os`, and `path/filepath` are
  standard library, and no dependency is added for JSON, TOML, or dotenv.
- Deliberately deferred, and none of it half-built here: the API key, which is
  the credentials item and which a configuration file must never carry; `.env`
  loading, which belongs with credentials; any key beyond `model`, each of which
  arrives with the work that reads it; writing a configuration file, which needs
  a caller and has none; watching a file for changes, since a session's
  configuration is frozen by design; and exposing the model as an ACP
  `configOption`, which is its own roadmap item and needs a model catalog to
  enumerate values.

## Validation

- Tests to write and run: `go test -race -count=1 ./...`. The e2e harness builds
  Ox with `-race` and `GORACE=halt_on_error=1`, so the two-workspace test also
  checks that a session's frozen configuration is safe to read from a concurrent
  turn.
- Static checks: `gofmt`, `go vet ./...`, `staticcheck ./...`, `dprint check`.
- Manual verification: with a real `OPENROUTER_API_KEY`, no `OX_MODEL`, and
  `{"model": "gpt-5.6-luna"}` in `~/.config/ox/config.json`, drive `initialize`,
  `session/new`, and `session/prompt` by hand and confirm the answer streams.
  Then add a `.ox/config.json` naming a different model in the working
  directory, create a second session, and confirm from the logs that the two
  sessions resolved different models from different layers.
