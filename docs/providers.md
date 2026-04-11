# Providers

## Model Discovery

Providers that support model discovery will list their available models when
you type `?` at the model selection prompt.

| Provider       | Discovery | Notes                                                            |
| -------------- | --------- | ---------------------------------------------------------------- |
| **openrouter** | Yes       | Lists all models from the OpenRouter catalog                     |
| **openai**     | Yes       | Lists known models (gpt-4o, gpt-4.1, o3, o4-mini, etc.)          |
| **zai-coding** | Yes       | Lists known GLM Coding Plan models                               |
| **ollama**     | Yes       | Lists models installed on your local Ollama instance             |
| **google**     | Yes       | Lists known Gemini models (gemini-3.1-pro, 3-flash, etc.)       |

## Context Window Resolution

Each provider resolves the context window size (max input tokens) for the
selected model. This powers the context usage percentage shown in the TUI
status line.

| Provider       | Source                                             | Behavior when unknown                     |
| -------------- | -------------------------------------------------- | ----------------------------------------- |
| **openrouter** | Model catalog (fetched at startup, cached locally) | Omits percentage                          |
| **openai**     | Built-in table of known models                     | Omits percentage for unrecognized models  |
| **zai-coding** | Built-in table of known models                     | Omits percentage for unrecognized models  |
| **ollama**     | Queries local Ollama instance per model            | Omits percentage if Ollama is unreachable |
| **google**     | Built-in table of known models                     | Omits percentage for unrecognized models  |
