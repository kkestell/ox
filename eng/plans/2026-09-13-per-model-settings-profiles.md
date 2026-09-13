# Per-model settings profiles

## Goal

Make model selection choose one complete, explicitly configured OpenRouter
request profile instead of replacing the model ID inside one shared collection
of sampling, reasoning, output-limit, and provider-routing settings.

## Desired outcome

Global and workspace settings define a `models` map keyed by exact OpenRouter
model ID and a `default_model` that names one entry. New sessions select the
default profile unless `--model` chooses another configured entry. ACP offers
only configured models, and changing the model applies that entry's complete
request settings on subsequent turns without carrying provider routing or other
model-specific values across profiles.

The local `$HOME/.config/ox/settings.json` keeps `openai/gpt-5.6-luna` as its
default and contains profiles for:

- `openai/gpt-5.6-luna`
- `z-ai/glm-5.3-flash`
- `deepseek/deepseek-v4.1-flash`
- `google/gemini-3.8-flash`

## Summary of approach

Replace the settings layer's top-level model request fields with this shape:

```json
{
  "default_model": "openai/gpt-5.6-luna",
  "models": {
    "openai/gpt-5.6-luna": {
      "provider": { "only": ["openai"] }
    },
    "z-ai/glm-5.3-flash": {
      "provider": { "only": ["z-ai"] }
    },
    "deepseek/deepseek-v4.1-flash": {
      "provider": { "only": ["deepseek"] }
    },
    "google/gemini-3.8-flash": {
      "provider": { "only": ["google-ai-studio"] }
    }
  }
}
```

Each profile owns the existing `max_tokens`, `temperature`, `reasoning`, and
`provider` fields. Global and workspace `models` maps merge by exact model ID;
profiles present in only one layer survive, and the two definitions of the same
model merge field by field using the existing nested reasoning, provider, and
price rules. The workspace `default_model` overrides the global value.

Resolution validates that at least one profile exists, every model ID is
nonblank, and the effective default or `--model` names a configured profile.
There is no legacy-schema compatibility path: the former top-level `model`,
`max_tokens`, `temperature`, `reasoning`, and `provider` keys become unknown
fields.

Activation resolves every configured ID through the OpenRouter catalog and
rejects the settings if any profile is missing or incompatible with its own
request fields or the effective tool set. Selection rechecks compatibility with
the retained conversation modalities, which can change after activation.

Session activation keeps the resolved profile map frozen beside the OpenRouter
catalog. A model selection copies the chosen profile into the effective request
configuration before applying session overrides and catalog compatibility
checks. The persisted selection remains the model ID rather than a copy of the
profile, so loading a session re-resolves that ID through the current global and
workspace settings. Removing a selected profile therefore makes reactivation
fail clearly.

The reasoning option's `default` value means the selected profile's configured
reasoning, or the OpenRouter provider default when that profile omits reasoning.
An explicit session reasoning effort overlays a copy of the profile reasoning.
Changing models clears that override and restores the new profile's default.

## Related code

- `internal/settings/settings.go` - Defines the strict global and workspace JSON
  schema and the per-model request fields.
- `internal/settings/merge.go` and `internal/settings/resolve.go` - Own profile
  map precedence, independent copies, default selection, and validation.
- `internal/agent/agent.go` - Resolves activation settings, OpenRouter catalog
  metadata, and durable selections when sessions are created or loaded.
- `internal/agent/config_options.go` - Builds the configured-only model picker
  and atomically applies complete profile and reasoning selections.
- `internal/agent/state.go` and `internal/agent/loop.go` - Validate and consume
  the effective frozen turn configuration.
- `internal/e2e/harness_test.go` and `internal/e2e/config_test.go` - Supply the
  default test profiles and prove shipped-process settings precedence.
- `internal/e2e/session_test.go` - Covers durable model selections and
  reactivation against changed settings.
- `docs/settings.md`, `docs/spec.md`, `README.md`, and `eng/architecture.md` -
  Own the settings reference, product behavior, setup summary, and durable
  configuration boundary.
- [OpenRouter provider routing](https://openrouter.ai/docs/guides/routing/provider-selection) -
  Defines the provider slugs used by each local profile's `only` policy.

## Current state

- One merged `settings.Config` contains a model ID and one shared set of request
  fields. A runtime selection changes only the model ID, retains provider,
  temperature, and output settings, and drops configured reasoning.
- ACP currently offers every entry in the activation's OpenRouter catalog, even
  when no settings profile exists for it.
- Settings are re-read per activation, while durable model and reasoning
  selections are reapplied when a session loads. Running turns already use an
  immutable configuration and remain unaffected by later selections.
- Ox's current settings implementation derives from Alpha's single-profile
  configuration. Eta supplies the established `default_model` naming, and Kappa
  confirms the useful boundary of resolving a keyed profile before model
  construction; Ox retains its own strict OpenRouter schema, layered merge, and
  ACP session semantics.

## Structural considerations

- **Hierarchy:** `internal/settings` continues to own file schema, precedence,
  and profile validation; `internal/agent` owns durable session choices and
  effective turn configuration; `internal/openrouter` remains unaware of
  settings files.
- **Abstraction:** A model profile names the existing group of OpenRouter
  request fields. Resolution returns configured profiles without introducing a
  generic provider or preset framework.
- **Modularization:** The change stays in the existing settings and agent
  configuration paths; no package, dependency, process, or credential surface is
  added.
- **Encapsulation:** The full profile map is activation state, not provider wire
  data or durable turn state. Each effective request receives an independent
  resolved profile so session overrides cannot mutate another profile.
- **Testability:** Pure settings tests cover decoding, merging, and resolution;
  agent tests cover option application; end-to-end requests prove the exact
  profile reaches the existing OpenRouter boundary.

## Test plan

- **Key behaviors to verify:** Decode multiple strict profiles; merge global and
  workspace maps and same-ID profiles without aliasing; require a configured
  default; restrict `--model` and ACP choices to configured IDs; switch model,
  provider, sampling, output, and reasoning together; reset reasoning to the new
  profile default; leave a running turn frozen; and re-resolve durable
  selections on load.
- **Test levels:** Focused `internal/settings` merge and resolution tests;
  `internal/agent` configuration-option and state tests; shipped-process tests
  that inspect requests received by the mock OpenRouter server.
- **Edge cases and failure modes:** Empty profile maps; blank IDs; default, CLI,
  or persisted selections absent from the merged map; an unknown profile field;
  explicit empty lists clearing inherited provider lists; a configured model
  absent from the catalog; model-specific settings incompatible with catalog
  parameters, tools, retained modalities, or context length; and persistence
  failure leaving the prior effective profile untouched.
- **What not to test:** OpenRouter's own routing decision, live completions from
  the four local models, or unchanged request encoding, catalog refresh,
  credentials, and turn-freezing mechanics below the configuration boundary.

## Implementation plan

- Introduce the strict `default_model` plus `models` schema and move the
  existing request fields into a model-profile type. Replace scalar
  configuration merging with map-by-ID profile merging while preserving nested
  per-field overrides, nil-versus-empty list semantics, and result independence.
- Resolve the effective default or CLI choice against the merged configured
  profiles. Keep clear source-aware errors for missing defaults and reject the
  former top-level request schema rather than adding compatibility behavior.
- Freeze resolved profiles at activation and update configuration selection to
  load the whole chosen profile. Build the ACP model options from configured IDs
  decorated with catalog names, retain history/tool/context compatibility
  checks, and make `default` reasoning restore profile reasoning.
- Adapt durable create/load behavior and test fixtures so only the selected
  model ID and explicit reasoning override persist, while each activation
  resolves current profiles and fails clearly when a saved selection no longer
  exists.
- Update unit, integration, and end-to-end coverage, including one mock-provider
  flow that observes distinct model, provider, temperature, token-limit, and
  reasoning fields before and after a runtime model switch.
- Update the shipped documentation and architecture, run the required checks,
  complete one completeness and simplification review, fix its findings, rerun
  affected gates, and mark the roadmap item complete.
- Install the verified binary, then migrate `$HOME/.config/ox/settings.json` to
  the four-profile JSON above while preserving its `process.language_servers`
  object. Confirm through an ACP session that Luna is initially selected and all
  four configured models are offered and can be selected without sending a
  completion request.

## Documentation updates

- Rewrite `docs/settings.md` around `default_model`, keyed profiles, layer merge
  semantics, configured-only selection, CLI precedence, reasoning defaults, and
  provider slugs.
- Update `docs/spec.md` so runtime model selection means complete profile
  selection and durable reload resolves current profile definitions.
- Update `eng/architecture.md` to name activation-frozen profiles and the
  separation between durable profile identity and immutable effective turn
  configuration.
- Update `README.md` setup and feature wording, then mark the roadmap item
  complete after implementation, local migration, review, and validation.

## Impact assessment

- Code paths affected: Settings decoding, merging, resolution, session
  activation and loading, ACP configuration options, test harness defaults, and
  request-configuration validation.
- Data, protocol, or schema impact: This is an intentional breaking JSON
  settings-schema change. ACP method shapes and OpenRouter request shapes do not
  change. Existing durable session logs retain their format, but a saved model
  selection must still exist in current settings when loaded.
- Dependency or API impact: No dependency or new external API. The ACP model
  option shrinks from the complete OpenRouter catalog to configured profiles.
  Credentials and the global-only process endpoint remain unchanged.

## Validation

- Tests to write and run: Focused settings and agent tests while iterating, then
  `make check` and `make test-eval` because the default model fixture and
  shipped evaluation process configuration change.
- Static checks: The formatting, vet, staticcheck, documentation, and race-test
  gates included by `make check`.
- Manual verification: Inspect the final settings and protocol diffs; query the
  public OpenRouter model/provider catalogs for the four IDs and pinned provider
  slugs; install Ox; migrate the local config without changing its language
  servers; and verify the ACP option values and profile switches without making
  live completion requests.
