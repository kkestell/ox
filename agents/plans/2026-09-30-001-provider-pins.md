# Provider pins

## Goal

OpenRouter chooses which provider serves each model request. The user wants to
choose the providers for some models in the global settings file:

```json
"models": {
  "deepseek/deepseek-v4.1-flash": { "providers": ["deepseek"] }
}
```

Once this works, every model request for a pinned model goes only to the listed
providers, tried in the listed order. That includes main agent turns, subagent
turns, summarizer requests, and headless runs. Models without a pin keep
OpenRouter's routing.

## Related code

- `crates/ox-server/src/settings.rs` — `SettingsFile`, `load_from`, and
  `Settings::for_workspace` read and validate both settings files.
- `crates/ox-server/src/lib.rs` — `load_settings_and_catalog` fetches the model
  catalog, loads the global settings against it, and installs it.
- `crates/ox-server/src/openrouter.rs` — `CatalogModel`, `parse_catalog`, and
  the two request body builders, `ordinary_body` and `summarizer_body`. Every
  model request, and compaction's size estimate, goes through one of them.
- `examples/settings.json` and `README.md` (Configuration) — the example
  settings file and the user-facing description of settings keys.

## Decisions

- **Only the global settings file can set pins.** The global settings file is
  read once at startup, before the model catalog is installed, so the pins can
  be stored on each `CatalogModel`. Both body builders already receive the
  catalog model, so main turns, subagents, compaction, and headless runs all use
  the pins without new plumbing. A `models` key in a workspace settings file is
  an error that names the file, the same way the Ox client treats `favorites`
  and `servers` as global-only.
- **Pinning uses `order` with `allow_fallbacks: false`.** OpenRouter's provider
  routing documentation says `order` tries the listed provider slugs in sequence
  and `allow_fallbacks: false` makes the request fail instead of moving to other
  providers. A pinned body gets
  `"provider": {"order": [...], "allow_fallbacks": false}`. An unpinned body has
  no `provider` field.
- **A bad pin stops startup with a clear message, like a bad `model` does.** A
  pinned model ID that is not in the model catalog and an empty `providers` list
  are errors naming the global settings file. Ox does not check provider slugs;
  OpenRouter receives them as written.
- **Pins are not session settings.** They are not saved in the transcript or
  shown to the ACP client. A loaded session's next model request uses the pins
  of the current process.

## Naming

- **Provider pin** — the provider slugs the global settings file lists for one
  model. It appears in the plan, the README, and the error messages.
- `CatalogModel::providers` — the provider pin of that catalog model, empty when
  the model has none.
- `ModelSettings` — the settings-file type of one entry under `models`, with the
  field `providers`. It is named for the file key so later per-model keys fit
  the same entry.

## Test plan

- `openrouter.rs`: new test `requests_route_pinned_models_to_their_providers`.
  It builds a catalog model with `providers` set to two slugs and one with none,
  and checks that `ordinary_body` and `summarizer_body` for the pinned model
  contain `provider.order` in the listed order with `allow_fallbacks` false, and
  that the unpinned model's bodies have no `provider`. `ModelRequestParameters`
  needs a `'static` catalog model, so the test leaks the pinned one.
- `settings.rs`: new test `global_settings_pin_providers_for_models`, table
  driven over the global settings file. A valid pin sets `providers` on the
  matching catalog model only. A pin for a model outside the model catalog and
  an empty `providers` list each fail with a message that starts with the file
  path and names the model.
- `settings.rs`: add a case to the error table in
  `workspace_settings_override_the_default_model` for a workspace file with
  `models`, expecting the global-only error.
- Existing `settings.rs` tests that pass `openrouter::catalog()` to `load_from`
  switch to an owned catalog parsed from `fixture::CATALOG`, since `load_from`
  now mutates it. Their guarantees do not change.

## Implementation plan

1. `openrouter.rs`: add `pub providers: Vec<String>` to `CatalogModel`, with a
   doc comment saying that model requests go only to these providers in order
   and that an empty list lets OpenRouter choose. `parse_catalog` sets it empty.
   Add one private helper used by `ordinary_body` and `summarizer_body` that
   adds the `provider` object when `providers` is not empty.
2. `settings.rs`: add `models: Option<BTreeMap<String, ModelSettings>>` to
   `SettingsFile` and the `ModelSettings { providers: Vec<String> }` type.
   Change `load` and `load_from` to take `&mut [CatalogModel]`. After the
   existing validation, `load_from` sets each pinned catalog model's
   `providers`, failing on an unknown model ID or an empty list. In
   `for_workspace`, return the global-only error when `models` is present.
   Update the module comment to name `models` as global-only.
3. `lib.rs`: in `load_settings_and_catalog`, make the fetched catalog mutable
   and pass it to `settings::load` before `install_catalog`.
4. Update the `settings.rs` tests and add the tests above.
5. `examples/settings.json`: add the `models` entry from the goal.
6. `README.md` Configuration: add `models` to the example and one bullet saying
   it maps OpenRouter model IDs to a `providers` list of OpenRouter provider
   slugs, that requests for that model go only to those providers in order, and
   that it can be set only in the global file.

## Documentation updates

- `README.md` Configuration section, as in step 6.
