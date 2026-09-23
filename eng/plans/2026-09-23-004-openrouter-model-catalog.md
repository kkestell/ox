# OpenRouter model catalog

## Goal

Load the model catalog from OpenRouter's public `GET /api/v1/models` when the
ACP server or the headless entry point starts, instead of from `models` in
`~/.config/ox/settings.json`. Keep models OpenRouter added within the last 183
days that are not `:batch` variants, accept tools, take text input, produce text
output, and have a context limit above 8,000.

Remove effort mappings. Each model's effort levels become exactly the efforts
OpenRouter lists for it, plus Default. The ACP `effort` option shows only the
selected model's effort levels, and changing the model updates that list.

Caching the fetched catalog is later work.

Follow [architecture](../architecture.md), [code style](../code-style.md),
[glossary](../glossary.md), and [testing](../testing.md).

## Related code

- `src/openrouter.rs`: `CatalogModel`, `EffortMapping`, `openrouter_effort`,
  `validate`, `CATALOG`, `install_catalog`, `catalog`, `test_catalog`,
  `default_model`, `catalog_model`, and `ordinary_body` and `summary_body`,
  which send the mapped effort.
- `src/sessions.rs`: `EffortLevel`, stored in effort entries and hook input by
  its `id`.
- `src/settings.rs`: `Settings`, `Loaded`, and model validation in `load_from`.
- `src/main.rs`: `run_command` parses `--effort`, `resolve_model`, and
  `load_settings` installs the catalog.
- `src/acp.rs`: `validate_settings`, `config_options`, and the `model` and
  `effort` branches of `set_config_option`.
- `src/acp/prompt.rs`: the model catalog check when a prompt run starts.
- `src/compaction.rs`: summarizer requests through `summary_body`.
- `examples/settings.json`, `README.md`: the `models` list and effort mapping
  description.

## Decisions

### Catalog filter

A model is in the model catalog when all of these hold for its entry in the
OpenRouter response:

- `created` is within the last 183 days
- `id` does not end in `:batch`, because chat completions do not serve batch
  variants
- `supported_parameters` contains `tools`
- `architecture.input_modalities` and `architecture.output_modalities` both
  contain `text`
- `context_length` is greater than 8,000, because compaction reserves 8,000
  tokens below the context limit

The catalog is sorted by model name. `name` and `context_length` become the
model's name and context limit. The response is external input: parse only the
fields above plus `reasoning.supported_efforts` into a private
`OpenRouterModel` without `deny_unknown_fields`. A missing or malformed required
field, a failed request, or an empty filtered catalog fails startup with a clear
error. The request needs no API key and has a 15-second total timeout so a
stalled response cannot hold startup indefinitely.

### Effort levels

`EffortLevel` stays a closed, `Copy` enum. It gains every effort OpenRouter uses
today, in ascending order: `Default`, `None`, `Minimal`, `Low`, `Medium`,
`High`, `XHigh`, and `Max`. Each `id` is the OpenRouter string (`none`,
`minimal`, `low`, `medium`, `high`, `xhigh`, `max`), and `default` for
`Default`. `name` gives `XHigh` the display name `Extra high`. `EffortLevel`
gets `pub fn openrouter_effort(self) -> Option<&'static str>`: `None` for
`Default` and `Some(self.id())` otherwise.

`CatalogModel` replaces `effort_mapping` with `efforts: Vec<EffortLevel>`:
`Default` followed by the model's supported efforts in `EffortLevel` order.
Parsing drops an effort string Ox does not know, so a new OpenRouter effort
appears only after it is added to `EffortLevel`. A model without
`reasoning.supported_efforts` has only `Default`.

### Default model

Settings gain a required `model` key naming the default model id and lose
`models`:

```json
{
  "model": "deepseek/deepseek-v4.1-flash",
  "hooks": { "after_run": { "command": "python3 scripts/report.py" } }
}
```

The settings file stays required. A blank `model` fails loading as today's blank
fields do. After fetching, `install_catalog(models, default_model)` returns an
error when the default model is not in the catalog; `main.rs` prefixes that
error with the settings path. `OX_IN_HOOK` still suppresses only global hooks.

### Selecting a model or effort level

`config_options` lists the effort levels of the model in `settings.model`.
Selecting a model whose effort levels do not include the current effort level
resets the effort selection to `Default`. Selecting an effort level the model
does not list is an invalid-params error, like any unknown choice.

`validate_settings` also rejects saved settings whose effort level the session
model no longer lists, with the same kind of error as a model outside the
catalog. The prompt run's model check in `src/acp/prompt.rs` calls
`validate_settings` instead of repeating it.

For `ox run`, `--effort` accepts any effort level id. After the catalog is
installed, `main.rs` rejects an effort level the chosen model does not list,
naming that model's effort levels.

### Summarizer effort

`CatalogModel::summary_effort` returns the model's lowest effort level other
than `Default` and `None`, or `Default` when it has none. `summary_body` sends
it through `openrouter_effort` and omits `reasoning` for `Default`.

## Naming

- **Effort level**: Default or one of the OpenRouter efforts in `EffortLevel`.
  A model's effort levels are Default plus the efforts OpenRouter lists for it.
- **Model catalog**: The OpenRouter models from `GET /api/v1/models` that pass
  the catalog filter, fetched once at startup. `catalog()` in code.
- **Default model**: The model named by `model` in
  `~/.config/ox/settings.json`, used for a new session and for `ox run` without
  `--model`. `default_model()` in code.
- **Catalog filter**: The rules above that decide whether an OpenRouter model
  enters the model catalog. Used in this plan and in the `parse_catalog` doc
  comment.
- Remove **Effort mapping** everywhere.

## Test plan

- Replace `test_catalog` with an OpenRouter `/models` response fixture parsed by
  `parse_catalog`, so every test uses the real parser. Keep the three current
  models first, with efforts that include `low` and `max` for the default model,
  and add rows the catalog filter drops: no `tools`, image-only output, a
  context limit of 8,000, a `:batch` variant, and a model older than 183 days.
  One model lists an unknown effort and one has no `reasoning`.
- Add one test, `catalog_filter_keeps_usable_models_and_known_efforts`, that
  asserts the parsed ids, a model's efforts in ascending order with the unknown
  one dropped, a model with only `Default`, and `summary_effort` for a model
  without `low`. Rewrite `requests_map_each_effort_for_each_model` to send each
  model's own effort levels and check that each id is sent verbatim, with
  `reasoning` omitted for `Default`.
- Extend `acp::tests::configuration_selections_validate_and_restore_saved_values`:
  effort options match the selected model, selecting a model without the current
  effort level resets it to `Default`, and an effort level the model does not
  list is rejected.
- In `acp::tests::setting_changes_apply_to_the_next_turn_while_the_system_prompt_stays_captured`,
  select `max` instead of `high` and expect `EffortLevel::Max`.
- In `settings::tests::loads_settings_and_suppresses_global_hooks_inside_hooks`,
  replace the model rows with rows for a valid `model`, a missing `model`, and a
  blank `model`. Update the settings file written by
  `acp::tests::headless_signals_clean_up_and_save_even_when_repeated`.
- Update `main::tests::parses_run_commands` for the new effort ids.

## Implementation plan

1. In `src/sessions.rs`, extend `EffortLevel` and `ALL`, and add
   `openrouter_effort`.
2. In `src/openrouter.rs`:
   - Replace `EffortMapping` and `CatalogModel::validate` with `efforts`,
     `supports(effort)`, and `summary_effort`.
   - Add `OpenRouterModel`, `parse_catalog(text) -> io::Result<Vec<CatalogModel>>`,
     and `fetch_catalog() -> io::Result<Vec<CatalogModel>>`, which sends
     `GET {ENDPOINT}/models` with a new `reqwest::Client` and a 15-second total
     timeout.
   - Store the catalog and default model id together in `CATALOG`. Make
     `install_catalog(models, default_model)` return `io::Result<()>`.
   - Use `EffortLevel::openrouter_effort` in `ordinary_body` and
     `summary_effort` in `summary_body`.
   - Replace `test_catalog` with the fixture.
3. In `src/settings.rs`, replace `models` with `model: String`, check it is not
   blank, and return `default_model` in `Loaded`.
4. In `src/main.rs`, make `load_settings` async: load settings, fetch the
   catalog, and install it. Check `--effort` against the chosen model after
   `resolve_model`.
5. In `src/acp.rs`, build effort options from the selected model, reset the
   effort selection on a model change when needed, validate the effort level in
   `set_config_option` and `validate_settings`. Call `validate_settings` from
   `src/acp/prompt.rs`.
6. Replace `models` with `model` in `examples/settings.json`.
7. Update the tests listed in the test plan.

## Documentation updates

- `README.md`: settings example and text; `--effort` values.
- `AGENTS.md`: the `src/settings.rs` and `src/openrouter.rs` entries.
- `eng/architecture.md`: the Settings section describes the fetched catalog,
  the catalog filter, the `model` key, and startup failure without network
  access; Compaction uses the summarizer effort instead of Low.
- `eng/glossary.md`: update **Effort level**, **Model catalog**, **Default
  model**, and **Context limit**; remove **Effort mapping**.
- `eng/testing.md`: `--effort` values.
- `examples/skills/careful/README.md`: the settings description.
