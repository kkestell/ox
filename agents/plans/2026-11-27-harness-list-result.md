# Serve only the catalog from harness/list, and correct the protocol's configuration, settings, label, and Changes base passages

## Goal

Sweep finding F6 (`agents/audits/2026-11-25-ox-sweep.md`, "F6. The protocol misstates configuration, settings changes, labels, and the Changes base"), tracked by issue #108, which stays open for its other findings.

`harness/list` and the MCP tool `list_models` marshal the whole loaded `config.Config`, so they also serve `titling`, `max_child_depth`, and `tailscale_ip`. Both serve a result holding only `harnesses` and `defaults` once this is done.

`docs/protocol.md` documents the configuration file, with an example `config.Load` refuses. The file's format, location, startup rules, and a loadable example move to a new `docs/configuration.md`; `protocol.md` keeps what the protocol needs. The protocol's passages on `session/configure`, `session/labeled`, and the Changes base state what the code does.

## Related code

- `internal/server/methods.go:48` — `harness/list` replies with `s.config`.
- `internal/server/mcp.go:226` — `listModels` marshals `s.config`; `docs/protocol.md` says it returns what `harness/list` returns, and `cmd/ox-mcp/main_test.go:303` asserts the two are equal.
- `internal/config/config.go:22` — `Config`, with `Titling`, `MaxChildDepth`, and `TailscaleIP` beside `Harnesses` and `Defaults`.
- `cmd/ox/web/src/protocol.ts:115`, `cmd/ox/web/src/ox.tsx:758` — the page's only `harness/list` caller; it reads `harnesses` and `defaults`. Nothing in the page, `ox-mcp`, or the server reads `max_child_depth` or `tailscale_ip` from either result; `mcp.go:472` reads `s.config.MaxChildDepth` directly.
- `internal/server/methods.go:691` — a model given without an effort takes `harness.Effort(model, current)`: the current effort when the model offers it, else the model's first effort level, else empty. Held by `TestModelChangeKeepsCurrentEffort` (`methods_test.go:2230`) and `TestEffort` (`config_test.go:199`).
- `internal/server/session.go:767` — `labelFrom` labels a top-level session from the first prompt the user sent it whose text has a nonblank line; an image-only prompt leaves it unlabeled. Held by `TestSessionLabel` (`methods_test.go:784`).
- `internal/server/changes.go:203` — `forkPoint`: the base is `git merge-base HEAD <workspace HEAD>`, or the empty tree when the workspace has no commits.
- `cmd/oxd/main.go:167` — startup refuses a Claude effort level Claude does not take.

## Decisions

- **Construction:** one function, `(*Server).catalog()`, builds the result for both `harness/list` and `list_models`, so the two cannot drift. Its type, `catalog` in `internal/server/methods.go`, holds `Harnesses map[string]config.Harness` (`json:"harnesses"`) and `Defaults config.Defaults` (`json:"defaults"`). `listModels` marshals `s.catalog()`. Agents calling `list_models` lose `titling`, `max_child_depth`, and `tailscale_ip`; nothing reads them, and `create_session` states the depth limit in its refusal.
- **Requirements:** "Session labels" and "Effort across models" are corrected to the behavior the code has, with the user's approved text under Requirement changes; "Setting changes keep the rest" stands, qualified by "Effort across models". The code is unchanged.
- **Protocol layout:** `## Configuration` becomes `## Harness methods`, holding `### harness/list` alone, matching `## Workspace methods` and `## Session methods`. The `#harnesslist` anchor is unchanged.
- **Concurrent edit:** the `session/labeled` row shares a dprint-aligned table with the `session/created` row that worktree `VqqY6nTRNGT` edits. Changing the label row's length realigns the whole table, so whichever change lands second reruns `make docs` after rebasing. The parent tells that session.

## Check your assumptions

1. **A session whose first prompt is only an image.** Kept: the user approved correcting "Session labels" to name the first prompt with text, as under Requirement changes.
2. **Choosing a model that lacks the current effort.** Kept: the user approved correcting "Effort across models" to say the effort otherwise becomes the new model's first effort level, as under Requirement changes.

## Test plan

- `harness/list` returns exactly `harnesses` and `defaults`: server layer. New `TestHarnessListServesCatalog` in `internal/server/methods_test.go`, built like `TestSettingsValidation` (`newServer` with a `config.Config` that sets `Titling`, `MaxChildDepth`, and `TailscaleIP`, then `answer`). It unmarshals the result into `map[string]json.RawMessage` and requires the key set `{harnesses, defaults}`. No existing test inspects the result's fields. It fails on the current code.
- `list_models` matches `harness/list`: `cmd/ox-mcp/main_test.go:303` already asserts it, and keeps passing once both use `catalog()`.
- The example in `docs/configuration.md` loads: the implementer passes it to `config.Load` once, through a temporary test that it removes. The example below already loads.
- The `session/configure`, `session/labeled`, and Changes base passages change only documentation; their behavior is held by the tests named under Related code.

## Implementation plan

1. `internal/server/methods.go`: add the `catalog` type and `(*Server).catalog()`; `harness/list` replies with `s.catalog()`.
2. `internal/server/mcp.go`: `listModels` marshals `s.catalog()`.
3. `internal/server/methods_test.go`: add `TestHarnessListServesCatalog`.
4. Create `docs/configuration.md`: a `# Configuration` title, then the configuration material from `docs/protocol.md`'s `## Configuration`, short and declarative, with no framing beyond the title and no subheadings:
   - Location: `$XDG_CONFIG_HOME/ox/config.json`, default `~/.config/ox/config.json`; `-config /absolute/path/to/config.json` chooses another file; a missing file is written with the built-in Claude Code and Ur catalog, creating its directory, then read; `ox` reads the same file for `tailscale_ip`.
   - `harnesses`: each lists at least one model with its effort levels and at least one permission mode; every choice has a nonempty `id`, unique in its list and passed to the harness, and a nonempty `name`; Claude takes `manual` rather than `default`; Ur's permission modes `manual` and `auto` and effort levels `max` through `none`, sent to OpenRouter as the reasoning effort; an Ur model's `providers`; Claude's `thinking_summaries`, linking `protocol.md#session-updates` for `reasoning_delta`; a model with no effort levels omits `efforts` and takes an empty `effort`.
   - Required `defaults` (the harness, model, effort, and permission mode a new session starts with), required `titling` (the harness, model, and effort that write session titles, each a configured choice, on a harness that can title sessions), required `max_child_depth` (how many levels of child sessions a top-level session can have beneath it, at least 1; built-in 2), optional `tailscale_ip` (this machine's Tailscale IPv4 address, within `100.64.0.0/10`; link `architecture.md#web-ui`).
   - Startup: `oxd` refuses to start when a default or titling choice is not configured, or when a Claude effort level is one Claude does not take (link `harnesses/claude-code.md#initialize-first`).
   - This example, as a fenced `json` block:

     ```json
     {
       "harnesses": {
         "claude": {
           "models": [
             {
               "id": "claude-haiku-5-5",
               "name": "Haiku 5.5",
               "efforts": [
                 { "id": "low", "name": "Low" },
                 { "id": "high", "name": "High" }
               ]
             }
           ],
           "permission_modes": [
             { "id": "manual", "name": "Manual" },
             { "id": "plan", "name": "Plan" }
           ]
         }
       },
       "defaults": {
         "harness": "claude",
         "model": "claude-haiku-5-5",
         "effort": "low",
         "permission_mode": "plan"
       },
       "titling": {
         "harness": "claude",
         "model": "claude-haiku-5-5",
         "effort": "low"
       },
       "max_child_depth": 2,
       "tailscale_ip": "100.101.102.103"
     }
     ```

5. `README.md`: in "Configuration and state", keep the location bullets and replace the closing link to `docs/protocol.md#configuration` with a link to [docs/configuration.md](docs/configuration.md) for the configuration file's fields. In the Documentation list, add `docs/configuration.md` ("the configuration file") and describe `docs/protocol.md` as "`oxd` JSON-RPC protocol and MCP tools".
6. `docs/protocol.md`:
   - Replace `## Configuration` and its body with `## Harness methods`. `### harness/list` says it returns `harnesses`, the configured harnesses with their models, effort levels, and permission modes as the [configuration file](configuration.md) lists them, and `defaults`, the harness, model, effort, and permission mode a new session starts with. It keeps the paragraph that clients offer these choices, that `session/create` and `session/configure` accept only these, that they reach the harness as they are so a value the harness refuses surfaces as its error, usually after the next `session/send`, and that a session stored with a Claude effort level Claude does not take fails to start. Drop `max_child_depth` from the example result.
   - Line 59: link `max_child_depth` to `configuration.md`.
   - Line 293 (`session/create`): "A setting [`harness/list`](#harnesslist) does not offer returns `-32602`."
   - `session/configure`: "Changes the settings given and keeps the others, preserving the conversation, and stores them for the session's later processes. A model given without an effort keeps the session's effort when the new model offers it, and otherwise takes the model's first effort level, or none for a model without effort levels." The rest of the paragraph stays.
   - `session/labeled` row: "A top-level session took the first prompt the user sent it with text, and `label` is that text's first nonblank line, shortened to 48 characters; sent again with the title the titling model generated from that prompt once it arrives." Leave the `session/created` row as it is.
   - `list_sessions`: "the first prompt the user sent it" becomes "the first prompt with text the user sent it" in both places in the `label` description.
   - `workspace/list_changes`: "The base is the merge base of the directory's `HEAD` and the workspace's `HEAD`: the newest commit both branches contain, where the directory's branch last left the workspace's branch. For a session in the workspace directory, and for a workspace, it is `HEAD` itself, so only uncommitted edits and untracked files show." Remove "For a worktree session it is the commit the worktree forked from".
7. `docs/architecture.md:460`: link `tailscale_ip` to `configuration.md`.
8. `docs/functional-requirements.md`: apply the two approved texts under Requirement changes.
9. Run `make docs` and `make test`. Confirm `grep -rn 'protocol.md#configuration\|(#configuration)' docs README.md` finds nothing.

## Requirement changes

Approved by the user:

- **Changed:** "Session labels." Old text: "A child session is labeled with the title it was created with. Any other session is labeled with a short title the configured titling model writes from the first prompt the user sent it, which replaces the label everywhere it shows once it arrives and lasts through restarts. Until then, or if titling fails, the session is labeled with that prompt's first line, shortened, or a short identifier before it has one." New text: "**Session labels.** A child session is labeled with the title it was created with. Any other session is labeled with a short title the configured titling model writes from the first prompt with text the user sent it, which replaces the label everywhere it shows once it arrives and lasts through restarts. Until then, or if titling fails, the session is labeled with that prompt's first line, shortened, or a short identifier before it has one."
- **Changed:** "Effort across models." Old text: "Choosing a model keeps the effort when the new model offers it." New text: "**Effort across models.** Choosing a model keeps the effort when the new model offers it, and otherwise takes the new model's first effort level."

## Documentation updates

- `docs/configuration.md` (new): the configuration file's location, fields, startup rules, and example.
- `README.md`: the link to `docs/configuration.md`, and the documentation list.
- `docs/protocol.md`: `harness/list`'s result, `session/configure`, `session/labeled`, `list_sessions`'s `label`, and the Changes base.
- `docs/architecture.md`: the `tailscale_ip` link.
- `AGENTS.md`, as workflow work the top-level agent lands on `main` after the pull request merges, not part of this change: a "Documentation" entry, "`docs/configuration.md`: the configuration file `oxd` and `ox` read: its location, fields, and the rules `oxd` checks at startup", and a source-map entry for `docs/configuration.md`.
