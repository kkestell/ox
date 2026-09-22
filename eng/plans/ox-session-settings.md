# Ox session settings

Status: implemented. Establishes how a user-selected setting reaches the agent
loop, using the model and the effort level. Plan, ask, and auto modes are
deliberately left out and will reuse this pattern later.

## 1. Goal

Let the user pick the model and the effort level from their ACP client, and
have both choices reach the OpenRouter request. Two settings with different
lifetimes:

- The model is chosen before the session's first user message and is then
  fixed for the life of the session. Switching models mid-session would send
  one model's `reasoning_details` to another, so the protocol surface stops
  offering the choice once the session has started.
- The effort level may change between turns and applies to every model request
  in the turns that follow.

Both are stored as transcript entries, so the transcript remains the single
source of truth for how any part of the session was produced.

## 2. Models and effort levels

The model list is hardcoded for this version. No model discovery, no user
configuration file, and no per-workspace default.

| Model ID                          | Label                      |
| --------------------------------- | -------------------------- |
| `deepseek/deepseek-v4.1-flash`    | DeepSeek V4.1 Flash        |
| `z-ai/glm-5.3-flash`              | GLM 5.3 Flash              |
| `meta/muse-spark-1.3-contributor` | Muse Spark 1.3 Contributor |

The first entry is the default model, replacing `openrouter::DEFAULT_MODEL`,
which currently names `openai/gpt-5.6-luna`.

Ox offers four effort levels: Default, Low, Medium, and High. Default sends no
reasoning parameter at all and accepts whatever the model does on its own. The
other three are Ox's own names, not OpenRouter's, and each model maps them to
one of the efforts that model actually accepts.

## 3. What OpenRouter reports about these models

Read from `https://openrouter.ai/api/v1/models` on 2026-09-21. Each model
record carries a `reasoning` object describing its effort support.

| Model                             | Supported efforts                      | Default effort | Reasoning mandatory |
| --------------------------------- | -------------------------------------- | -------------- | ------------------- |
| `deepseek/deepseek-v4.1-flash`    | max, high, low                         | high           | no, on by default   |
| `z-ai/glm-5.3-flash`              | max, high, low                         | max            | yes                 |
| `meta/muse-spark-1.3-contributor` | max, xhigh, high, medium, low, minimal | medium         | yes                 |

All three list `reasoning`, `reasoning_effort`, `tools`, and `tool_choice` in
`supported_parameters`, so they work with Ox's existing tool calling and with
the reasoning parameter described below.

Two models do not accept a medium effort, and two treat reasoning as
mandatory, which rules out a future Off level for them. This is exactly why the
mapping is per model rather than a single shared string.

## 4. Effort mapping

Each model maps Ox's three explicit levels onto efforts it accepts. Default is
absent from the table because it sends nothing.

| Model                             | Low   | Medium   | High   |
| --------------------------------- | ----- | -------- | ------ |
| `deepseek/deepseek-v4.1-flash`    | `low` | `high`   | `max`  |
| `z-ai/glm-5.3-flash`              | `low` | `high`   | `max`  |
| `meta/muse-spark-1.3-contributor` | `low` | `medium` | `high` |

The two models without a medium effort spread Low, Medium, and High across
their three accepted values, so every Ox level stays distinct. Muse Spark maps
straight through and leaves its `xhigh`, `max`, and `minimal` efforts unused.

The request body gains one field, omitted entirely for Default:

```json
{ "reasoning": { "effort": "high" } }
```

Hold the catalog and the mapping together in `src/openrouter.rs`. `ModelChoice`
maps an effort level with an exhaustive match, so the storage module does not
know the catalog's layout:

```rust
pub struct ModelChoice {
    pub id: &'static str,
    pub name: &'static str,
    /// The OpenRouter effort sent for Low, Medium, and High.
    efforts: [&'static str; 3],
}
```

A model that accepts no effort at all would need a different shape. None of
the three do, so do not build for it.

## 5. Transcript entries

`TranscriptEntry::Model` already records a setting that governs everything
after it. Generalize that rather than adding a settings table:

```rust
pub enum TranscriptEntry {
    Model(String),
    Effort(EffortLevel),
    UserMessage(String),
    AssistantMessage(AssistantMessage),
    ToolResult(ToolResult),
}
```

The settings in force at any point are a fold over the transcript. That gives
persistence across restarts, correct values after `session/load`, and a record
of when each change took effect, with no second place to keep state.

Validation in `src/sessions.rs` changes to:

- An empty transcript is valid. A new session has no entries until its first
  turn.
- A `Model` entry appears at most once, at index 0. A nonempty transcript has
  exactly one.
- An `Effort` entry appears only directly before a `UserMessage`, never
  between an assistant message and its tool results.

`chat_messages` skips `Effort` the way it already skips `Model`. The model is
not told its own effort level.

`SessionStore::create` loses its `model` argument and writes no entries. The
settings fold replaces the separate `StoredSession::model()` path.

## 6. Settings reach the transcript at the start of a turn

Selections arrive over `session/set_config_option` at any time, including
while a turn is running. `ServerState` keeps the current selection per session
in memory and answers the request immediately, so the client's UI never waits
on a running turn.

Prompt startup takes an optional snapshot of the current selection, reads and
validates the session, and folds the transcript once. An existing transcript
supplies the model because it is the single source of truth; when a selection
is present, it supplies the effort level. Without a selection, saved settings
supply both values. An empty transcript without a selection uses the defaults.
Startup appends changed setting entries before saving the user message. Three
consequences fall out of that single rule:

- A change made mid-turn takes effect on the next turn, never the current one.
- The marker sits immediately before the first user message it governs, which
  is where replay wants it.
- Nothing writes to the transcript concurrently with a prompt run, so the
  operation guard keeps doing its existing job unchanged.

The in-memory selection is seeded with the defaults on `session/new` and from
the transcript fold on `session/load`. A prompt does not seed a missing
selection. A process restart between `session/new` and the first prompt loses
an unused selection and returns to the defaults; nothing has been produced by
then, so this is acceptable. A started session instead falls back to the model
and last effort saved in its transcript.

`prompt::run` takes the optional selection snapshot as a parameter. Its
synchronous start opens the prompt and saves the user message while the ordered
ACP handler still defines the turn boundary, then it returns the future that
runs the model loop. Headless `ox run` explicitly passes the defaults.

## 7. ACP surface

Both settings are `select` configuration options, replacing the three dummy
selectors now in `src/acp.rs`.

| Option ID | Name   | Category        | Choices                       |
| --------- | ------ | --------------- | ----------------------------- |
| `model`   | Model  | `model`         | the three models in section 2 |
| `effort`  | Effort | `thought_level` | Default, Low, Medium, High    |

They are returned from `session/new` and `session/load`, and the full list
comes back from every `session/set_config_option` response.

Once the session has a model entry, the model is fixed:

- `session/set_config_option` for `model` fails with `invalid_params`.
- The model option is still reported, with the chosen model as its only
  choice, so the client keeps showing what the session runs on. ACP has no
  disabled or read-only flag for a configuration option, so a single-choice
  selector is how the lock is expressed.
- The first prompt run sends a `config_option_update` session notification
  when it writes the model entry, so the client narrows the selector without
  having to reload the session.

Effort is never restricted, and its value survives a reload because it lives
in the transcript.

## 8. Out of scope

Plan, ask, and auto modes. Model discovery from the OpenRouter API. Per-user
or per-workspace defaults. Token budgets through `reasoning.max_tokens`.
Turning reasoning off. Changing the model of an existing session by any route,
including a fork or copy.

## 9. Testing

- Fold and validation: unit tests in `src/sessions.rs` for an empty
  transcript, a model entry out of position, an effort entry inside an
  assistant batch, and the folded value after several effort changes.
- Request encoding: extend the existing mock OpenRouter server tests to assert
  the `reasoning.effort` string per model and its absence for Default.
- Turn boundary: an ACP test asserting that a selection made while a turn is
  running appears in the transcript before the next user message and not
  before the current one.
- Model lock: an ACP test asserting `invalid_params` after the first user
  message, and the single-choice selector in the response.
- Live: start Ox with `OX_DATA_DIR` set to a temporary directory and use an ACP
  client to create a session, select each model and effort through
  `session/set_config_option`, and submit a prompt. Confirm a real completion
  and inspect the saved transcript entries. Headless `ox run` exercises only
  the default settings.

## 10. Documentation

Update the `AGENTS.md` source map for `src/acp.rs`, `src/sessions.rs`, and
`src/openrouter.rs`, and add glossary entries:

- Session settings: the model and effort level in force for a turn.
- Effort level: one of Ox's four levels, Default, Low, Medium, or High.
- Effort mapping: the per-model table turning an effort level into the
  OpenRouter effort string, or into no reasoning parameter at all.
