# Configuration

`oxd` reads `ox/config.json` beneath `$XDG_CONFIG_HOME`, or beneath `~/.config`
when that is unset; `-config /absolute/path/to/config.json` chooses a different
file. A missing file is written with the built-in catalog of Claude Code and Ur,
creating its directory, and then read. `ox` reads the same file, for its
optional `tailscale_ip`.

`harnesses` holds the harnesses by name: `claude` for Claude Code and `ur` for
Ur. Each lists at least one model, each with the effort levels it takes, and at
least one permission mode. Every choice has a nonempty `id`, unique within its
list and passed to the harness, and a nonempty `name` to show. For Claude, use
`manual`, not its alias `default`. Ur takes the permission modes `manual`, where
each shell command asks for approval first, and `auto`, where every command runs
without asking, and the effort levels `max`, `xhigh`, `high`, `medium`, `low`,
`minimal`, and `none`, which it sends to OpenRouter as the reasoning effort. An
Ur model may list `providers`, the OpenRouter providers that serve it, in order;
its requests go to those alone. Claude's optional `thinking_summaries`, off
unless set to `true`, has Claude stream a summary of its thinking as
[`reasoning_delta`](protocol.md#session-updates). A model with no effort levels
omits `efforts` or lists none, and takes an empty `effort`.

The required `defaults` name the harness, model, effort, and permission mode a
new session starts with, each one of the configured choices. The required
`titling` names the harness, model, and effort that write session titles, each a
configured choice, on a harness that can title sessions. The required
`max_child_depth`, at least 1, is how many levels of child sessions a top-level
session can have beneath it; the built-in configuration sets 2. The optional
`tailscale_ip` is this machine's Tailscale IPv4 address, within `100.64.0.0/10`;
see [Web UI](architecture.md#web-ui) for what setting it does.

`oxd` refuses to start when `harnesses` names any other harness, or when a
default or titling choice is not configured. When the configuration offers
Claude, `oxd` runs Claude at startup to read its model list, and refuses to
start when it cannot run Claude, when a Claude model is one Claude does not
list, or when an effort level is one Claude does not list for that model; see
[Claude's model list](harnesses/claude-code.md#initialize-first).

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
