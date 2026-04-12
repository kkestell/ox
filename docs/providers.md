# Providers

Providers are configured via `providers.json`, which is the single source of truth
for which providers exist, what models they offer, and their context window sizes.
The application never writes this file — it is user-authored configuration.

## Provider Types

Each provider entry has a `type` field that determines which SDK is used:

| Type                  | SDK / Protocol     | Providers using it                          |
| --------------------- | ------------------ | ------------------------------------------- |
| `openai-compatible`   | OpenAI Chat API    | OpenAI, OpenRouter, Z.AI, any compatible API |
| `google`              | GeminiDotnet       | Google (Gemini)                             |
| `ollama`              | OllamaSharp        | Ollama (local)                              |

### `openai-compatible`

Uses the OpenAI .NET SDK pointed at the provider's endpoint. Set the `url` field
to the provider's API base (e.g. `https://openrouter.ai/api/v1`). When `url` is
omitted, the standard OpenAI endpoint is used. Requires an API key stored in the
OS keyring.

### `google`

Uses the GeminiDotnet SDK to call Google's Generative AI API. No `url` field is
needed — the endpoint is managed by the SDK. Requires an API key stored in the
OS keyring.

### `ollama`

Uses OllamaSharp to communicate with a local Ollama instance. The `url` field
specifies the Ollama endpoint (e.g. `http://localhost:11434`). No API key is
needed.

## Model IDs

Models are addressed in `provider/model` format — the first slash-delimited
segment is the provider name, and the remainder is the model ID passed to that
provider's SDK. For example:

- `openai/gpt-5.4` → OpenAI provider, model `gpt-5.4`
- `openrouter/anthropic/claude-3.5-sonnet` → OpenRouter provider, model `anthropic/claude-3.5-sonnet`
- `ollama/qwen3:8b` → Ollama provider, model `qwen3:8b`

## Configuration Reference

Each provider entry in `providers.json` has this shape:

```json
{
  "name": "Display Name",
  "type": "openai-compatible | google | ollama",
  "url": "https://optional-endpoint.example.com/v1",
  "models": [
    {
      "name": "Human-Readable Model Name",
      "id": "model-id",
      "context_in": 200000
    }
  ]
}
```

**Fields:**

| Field        | Required | Description                                                    |
| ------------ | -------- | -------------------------------------------------------------- |
| `name`       | Yes      | Human-readable display name (shown in the connect wizard)      |
| `type`       | Yes      | Provider type — determines which SDK is used                   |
| `url`        | No       | Custom endpoint URI (required for `ollama`; optional for `openai-compatible`) |
| `models`     | Yes      | List of models offered by this provider (must be non-empty)    |
| `models[].name` | Yes   | Human-readable model name                                      |
| `models[].id`   | Yes   | Model identifier passed to the provider's SDK                  |
| `models[].context_in` | Yes | Context window size in tokens (must be a positive integer) |

## API Keys

Providers that require an API key (all except Ollama) read it from the OS keyring.
Keys are stored under service `ur` with the provider name as the account. Set them
via the CLI:

```
ur config set-api-key <key> --provider <provider-name>