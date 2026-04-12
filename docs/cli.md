# CLI

Ox supports the following CLI options (parsed in `src/Ox/OxBootOptions.cs`):

| Flag | Argument | Description |
|---|---|---|
| `--fake-provider <scenario>` | Scenario name or path to JSON | Registers a fake LLM provider that replays a canned scenario instead of calling a live model API. Used for testing/evaluation. |
| `--headless` | *(none)* | Runs Ox without a TUI — drives the agent loop from the CLI, printing responses to stdout. Requires at least one `--turn`. |
| `--yolo` | *(none)* | Auto-grants all tool permission requests without prompting. Only meaningful in headless mode (the TUI always uses interactive prompts). |
| `--turn <message>` | User message text | Adds a user message to send to the LLM. Can be repeated. At least one is required in headless mode. |
| `--model <model>` | Model identifier (e.g. `openai/gpt-4o`) | Overrides the configured model for this run, without rewriting settings files. |

**Constraints:**
- `--headless` requires at least one `--turn` (otherwise Ox exits with an error).
- Any unrecognized arguments are collected into `RemainingArgs` and forwarded to the host builder.