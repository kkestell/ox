# Utility model: live harness experiments

> Imported Hypercube research; see [provenance and scope](README.md).

## Conclusions

All three installed harnesses answered without a saved native conversation using
existing credentials. The main corrections to [issue #66](https://github.com/kkestell/hypercube/issues/66)
are:

- **Claude subscription authentication does not work with `--bare`.** Use
  `--safe-mode` instead, plus explicit tool/MCP restrictions and no persistence.
- **OpenCode's generation endpoint is stateless, but its server is not free of
  startup side effects.** A project MCP server started during a generation test.
  A directory outside any repository avoids project configuration, not global
  configuration. A temporary user-level configuration also started an MCP server
  there. The existing plugin-style server can handle generation requests.
- **Pi's proposed flags still load a global `APPEND_SYSTEM.md`.** Add
  `--append-system-prompt ''` to suppress that discovery. This was demonstrated
  with a local dummy endpoint, not inferred only from help.
- A roughly 100 KB prompt worked through stdin for Claude/Pi and an HTTP request
  body for OpenCode. OpenCode CLI `--data -` does **not** read stdin.
- Plain successful output was clean. Error text is not consistently on stderr.
  Claude `SIGINT` can exit **0 with no answer**; an invalid Claude effort can
  also exit 0 while using the default effort. Exit status alone is insufficient.

## Scope and method

**Observed:** macOS, 2026-11-10; exactly Claude Code **2.1.319**, OpenCode
**2.2.6**, Pi **1.4.0**, checked with their version commands. These are local
observations, not a compatibility guarantee for other versions/providers.

Started with `gh issue view 66`, the existing
[harness notes](harnesses/), and the current plugin discovery/launch code in
`plugins/{claude,opencode,pi}`. `reviewSettings` in `internal/ui/actions.go`
uses the existing harness/model/effort selection; no application code changed.

Experiments ran under `.agents/scratch/utility-model/`. The requested neutral
working-directory checks used empty temporary directories outside repositories,
then removed them. Prompts were short synthetic requests, except for synthetic
transcript/input-limit tests. No user transcript was sent. No credentials,
`HOME`, or existing global/project settings were changed. Alternate configuration
roots were per-launch environment overrides containing only test configuration.
Scratch scripts/captures are not committed; the commands and relevant results
are recorded below.

Timing used Python `time.monotonic()` around subprocess launch/communication or
HTTP requests. “Cold” means a new harness process, **not** cold filesystem/model
caches. For the tiny `OK` answers, process-start-to-completed-answer is reported,
not a separately instrumented streaming first-token benchmark.

File snapshots compared `(path, size, mtime_ns)` before/after sequential runs in
`~/.claude`, `~/.claude.json`, `~/.pi/agent`, OpenCode data/state directories,
and the scratch directory. OpenCode table counts came from read-only SQLite
queries before/after. Other user agents were running: unrelated transcript
changes were excluded from conclusions, and ambiguous cache writes are identified
as such. No Linux runs were performed.

## Claude Code

### Recommended command and authentication

Run from a neutral directory; feed the prompt on stdin:

```sh
claude -p --safe-mode --no-session-persistence \
  --tools '' --disallowedTools 'mcp__*' --strict-mcp-config \
  --disable-slash-commands \
  --system-prompt 'You answer briefly.' \
  --model haiku --effort low < prompt.txt
```

Substitute the selected model/effort, omitting effort for a model that does not
support it. This remains a Claude Code run, not a direct completion API.

**Observed authentication:** `claude auth status --json` reported
`loggedIn:true`, `authMethod:"claude.ai"`, `subscriptionType:"team"`.
No API-key environment variable was set.

```text
# Same restricted print command, but --bare instead of --safe-mode:
exit 1; 1.051 s
stdout: Not logged in · Please run /login
stderr: empty

# --safe-mode, --model haiku, prompt supplied through stdin:
exit 0; 2.062 s
stdout: OK\n
stderr: empty
```

The issue's bare-mode command therefore fails with this real subscription
login. Installed help explicitly says bare authentication is
`ANTHROPIC_API_KEY` or an explicit `apiKeyHelper`, never OAuth/keychain.
Safe mode retains normal login, but gives up bare's avoidance of keychain reads
and some runtime/background machinery. API-key, Bedrock, Vertex, Foundry, named
profile and gateway authentication were not live-tested.

### Models and effort

**Observed:** the same `initialize` control request the plugin uses returned
model values including `default`, `opus`, `sonnet`, `haiku` and full model IDs.
For example, `haiku` resolved to `claude-haiku-5-5`; its advertised efforts were
`low,medium,high,xhigh,max`. These values are the plugin's stored model IDs and
are directly usable with `--model`; do not substitute a display label.

- `--model sonnet --effort low`: exit 0, clean `OK`, 2.269 s.
- `--model haiku --effort low --disable-slash-commands`: exit 0, clean answer;
  the stream initialization reported `tools:[]`, `mcp_servers:[]`, `skills:[]`.
- `--effort minimal`: **exit 0**, `OK` on stdout, but stderr said:

  ```text
  Warning: Unknown --effort value 'minimal' — ignoring it and using the default
  effort. Valid values: low, medium, high, xhigh, max.
  ```

Use the catalog's effort values, not a common cross-harness “minimal” value.

### Project/global discovery and disk writes

**Observed project checks:** scratch `CLAUDE.md` and `AGENTS.md` demanded
`PROJECT_INSTRUCTIONS_LOADED`; a project `SessionStart` hook touched a marker.
The safe-mode command answered `OK` and did not create the hook marker.
No conversation file for the scratch working directory appeared, and
`~/.claude.json` had no changed keys or new project entry in the measured run.
No new history file was observed.

During that window, remote-settings/policy cache mtimes and a built-in plugin
metadata file changed. Because other Claude agents were alive, attribution of
those particular writes is uncertain. **No session persistence is not a
promise of no global cache/telemetry/policy writes.**

**Observed global checks:** the user's global config contains a stdio
`context7` MCP server launched with `npx`, and a `PreToolUse` hook. In the
outside-repository test, the recommended restriction flags produced
`mcp_servers:[]` and `tools:[]`; process-group sampling saw no `npx` or hook
process. The existing hook cannot trigger without a tool call, so this is not
an independent positive test of every possible global hook event.

An important reporting detail: safe-mode initialization **still listed installed
plugin metadata and built-in skills** before `--disable-slash-commands` was
added. With that flag, skills and slash commands were empty, while plugin
metadata remained listed. The list is not proof those plugins executed.
Installed help says built-in features/managed policy survive safe mode; arbitrary
plugin behavior was not comprehensively tested.

### Structured output

The restricted safe-mode command also worked with:

```sh
--output-format json --json-schema \
'{"type":"object","properties":{"title":{"type":"string"},"count":{"type":"integer","const":1}},"required":["title","count"],"additionalProperties":false}'
```

**Observed:** three concurrent Haiku calls returned exit 0, empty stderr and
schema-valid `structured_output`, including a prompt explicitly asking to ignore
formatting and write prose. Example, trimmed:

```json
{
  "type": "result",
  "subtype": "success",
  "is_error": false,
  "num_turns": 2,
  "stop_reason": "tool_use",
  "structured_output": {"title": "Debugging a Flaky Test", "count": 1}
}
```

Each result had one reported API usage iteration despite `num_turns:2`.
Schema output works with an empty ordinary tool catalog. This small **3/3**
sample does not establish universal reliability or a single-request guarantee.
Validate `is_error`, termination and `structured_output`; do not read only the
`result` string. Invalid schemas, exhausted schema retries and refusals were not
tested. Structured-output validation does not enforce title quality/length
unless the caller supplies corresponding constraints.

### Errors, input, latency and cancellation

| Test | Observed result |
| --- | --- |
| Unknown model `utility-research-nonexistent` | Exit 1; stdout says selected model may not exist/be accessible; stderr warns about an unrecognized model and assumed 200k context. 1.496 s. |
| No usable auth via `--bare` | Exit 1; login error on **stdout**, empty stderr. |
| Unreachable proxy | Exit 1; stdout `API Error: Connection refused … (ECONNREFUSED)`; empty stderr. 1.068 s. |
| About 100 KB on stdin | Exit 0, correct one-sentence synthetic transcript summary, empty stderr; 5.038 s. |
| Three processes concurrently | All three schema calls succeeded in 2.084, 2.275 and 2.860 s individually. No observed serialization. |
| SIGTERM while awaiting network | Exit 143; stopped in 0.006 s; no surviving process-group members. |
| SIGINT while awaiting network | **Exit 0, empty stdout**; stopped in 0.007 s; no surviving process-group members. |

For the proxy test only, set `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY` to
`http://127.0.0.1:1`, `NO_PROXY=127.0.0.1,localhost,::1`,
`CLAUDE_CODE_MAX_RETRIES=0`, and `API_TIMEOUT_MS=5000` for that child.
This simulates transport unavailability, not a provider returning an HTTP error.

**Unknown:** true near-context-limit behavior for the selected Claude models.
The observed Haiku catalog/result advertised a 1M-token context, and no
million-token paid saturation test was attempted. Stdin acceptance at 100 KB
does not establish safe handling at the model limit.

## OpenCode V2

### Recommended request and authentication

Prefer the HTTP endpoint over `run`. With an owned server, launch from a neutral
directory using exactly the existing plugin's server form:

```sh
OPENCODE_PASSWORD="$per_launch_secret" opencode serve --stdio --port 0
```

Keep stdin open. The first stdout line is `{"url":"http://127.0.0.1:PORT"}`.
Use Basic authentication with username `opencode` and that password:

```http
POST /api/experimental/generate/text
Authorization: Basic <base64(opencode:per_launch_secret)>
Content-Type: application/json

{"prompt":"Reply with exactly OK.","model":{"providerID":"openai","id":"gpt-5.6-terra","variant":"low"}}
```

```json
{"data":{"text":"OK"}}
```

For a CLI invocation with an owned private runtime:

```sh
opencode --standalone api post /api/experimental/generate/text \
  --data '{"prompt":"Reply with exactly OK.","model":{"providerID":"openai","id":"gpt-5.6-terra","variant":"low"}}'
```

**Observed:** this succeeded with the user's stored **OpenAI OAuth** credential,
not an API key. `opencode auth list` distinguished OpenAI from API-key
integrations; a read-only query of only `json_extract(value,'$.type')` for the
active OpenAI credential confirmed `oauth`. No secret fields were printed.
Other stored providers were not authentication-tested beyond the OpenRouter
small-context experiment below.

**Observed existing-server reuse:** a separately started
`serve --stdio --port 0` process accepted both direct HTTP generation and:

```sh
OPENCODE_PASSWORD="$per_launch_secret" \
  opencode --server "$url" api post /api/experimental/generate/text \
  --data '<same JSON body>'
```

The CLI call returned clean JSON, exit 0, in 1.918 s. Thus the **same server
interface** already owned by a Hypercube session plugin can serve this request.
The experiment did not submit requests to another user's active session server
or test generation simultaneously with a coding turn on that server.

### Model and effort contract

**Observed installed OpenAPI**, fetched from the running 2.2.6 server:

- Request allows only `prompt` and optional `model`.
- `model` is `{providerID,id,variant?}`; no JSON Schema field exists.
- Response is `{data:{text:string}}`.
- `/api/model` exposed enabled `openai/gpt-5.6-terra` with variants
  `none,low,medium,high,xhigh,max`.

Split the plugin's stored `provider/id` on the **first** slash: the remainder
can itself contain a slash, for example `openrouter/openai/gpt-3.5-turbo-0613`.
Use its returned `id`, not `modelID` or a display label; the catalog can contain
several configured IDs mapping to the same upstream model.

`variant:"low"` worked. `variant:"minimal"` returned HTTP 400 immediately:

```json
{"_tag":"InvalidRequestError","message":"Variant unavailable for openai/gpt-5.6-terra: minimal","field":"model.variant"}
```

Omit `variant` only when that is the intended model default. Prompted JSON would
need caller-side validation; arbitrary schema enforcement was not available in
the installed request contract.

### Startup, project/global discovery and disk writes

**Observed:** `--standalone` required no pre-existing server and returned its
answer in 2.593 s. It used a private runtime in the CLI process, with no surviving
server process after exit. Explicit stdio servers announced their URL in about
0.09–0.15 s. Closing their stdin shut them down with exit 0.

Without `--standalone` or `--server`, `opencode api get /api/info` attempted to
start/discover a background service. This was not reliable on this machine:
requests timed out after 15/20 s and were terminated. An isolated-config log
showed child launches of `serve --service`, followed by an existing-listener
conflict on managed port 49374. No existing user service was stopped or
reconfigured. This confirms an **auto-start attempt**, not a successful fresh
background-service round trip; use explicit server ownership here.

**Observed project side effect:** scratch configuration contained:

```json
{
  "instructions": ["CLAUDE.md"],
  "mcp": {"servers": {"probe": {
    "type": "local", "command": ["sh", "-c", "touch <scratch-marker>; exit 1"]
  }}}
}
```

The stateless generation answered `OK`, ignoring the instruction file's demand
for `PROJECT_INSTRUCTIONS_LOADED`, **but the MCP startup marker appeared**.
A stateless model request does not mean an inert server bootstrap. Project
plugins/other startup code were not individually instrumented.

**Observed neutral-directory follow-up:** from an empty temporary directory
outside any repository, normal config gave `/api/mcp` data `[]`, a successful
`OK` generation, and no files in the working directory. The user's current
OpenCode global config contains no MCP servers. To test global behavior without
changing it, an alternate `XDG_CONFIG_HOME` contained
`opencode/opencode.json` with a harmless global MCP marker command. After
`/api/model` initialized the location, the marker appeared even in that neutral
directory, and `/api/mcp` reported the test server's expected exited-process
failure. **Global MCP startup still applies.** An initial empty MCP list can
precede asynchronous initialization; do not treat it as a final absence check.

A live `opencode debug config` check still discovered project configuration when
launched with legacy-looking `OPENCODE_CONFIG_CONTENT` and
`OPENCODE_DISABLE_PROJECT_CONFIG=true`. Those variables were not a working
V2 isolation mechanism in this test. Do not infer V2 support from embedded
binary strings or V1 documentation.

**Observed disk state:** across scoped stateless-generation runs, no table's row
count changed, including native session/message tables. The DB, WAL and log
files nevertheless changed mtimes. This establishes no added conversation in
these runs, **not** that SQLite/config/cache initialization performs no writes
or updates. Default credentials/config are shared with normal OpenCode.

### Why not `opencode run`?

This command worked:

```sh
opencode --standalone run --archived \
  --model 'openai/gpt-5.6-terra#low' 'Reply with exactly OK.'
```

However, it returned `PROJECT_INSTRUCTIONS_LOADED`, loaded project instructions,
wrote a native session and messages, and printed a colored agent/model banner to
stderr (`> build · gpt-5.6-terra`). `--archived` hides a session; it does not make
it ephemeral or remove tools. The CLI help had no no-session/no-tools equivalent.
A separate custom-agent attempt failed with `Agent not found`, yet **still added
a native session, messages and an inbox row**. The cause of that custom-agent
lookup failure was not pursued; `run` is not the recommended utility surface.
Both test-created native conversations were subsequently removed through the
session DELETE API, not by editing the database.

### Errors, input, latency and cancellation

| Test | Observed result |
| --- | --- |
| Unknown model | HTTP 404 `ModelNotFoundError`; CLI exit 1, error JSON on stdout and `HTTP 404 Not Found` on stderr; 0.408 s. |
| No stored auth in alternate data/config/state roots, `OPENCODE_DB=:memory:` | Known OpenAI model became unavailable: same HTTP 404/CLI exit 1, rather than a distinct login-required response; 0.607 s. |
| Unreachable proxy | HTTP 502 `ProviderRequestError` with `ECONNREFUSED`; CLI exit 1, JSON stdout, `HTTP 502 Bad Gateway` stderr; 13.203 s with default retry behavior. |
| About 100 KB HTTP prompt body | HTTP 200, correct one-sentence summary; 3.346 s. No command-line size issue. |
| CLI `--data -` with JSON on stdin | HTTP 400 `Expected a valid JSON body`; exit 1. `-` was treated as the body, not a stdin selector. |
| Two HTTP calls concurrently, one server | Both succeeded in 3.346 and 3.673 s; combined wall time 3.675 s. No observed serialization. |
| SIGTERM to standalone CLI awaiting network | Exit 143; 0.028 s to stop; no surviving process-group members. |
| SIGINT to standalone CLI awaiting network | Exit 130; 0.028 s to stop; no surviving process-group members. |

For the unavailable-network test, set the same unreachable-proxy variables as
above on the **server-owning process**, keeping loopback in `NO_PROXY`.
Environment changes on a client do not reconfigure an existing server.

For large prompts, send JSON directly in an HTTP body, for example
`curl --data-binary @request.json`, or use the backend's HTTP client. The Python
HTTP probe sent the 100 KB body without putting it in argv.

**Context-limit probe:** enabled OpenRouter model
`openai/gpt-3.5-turbo-0613` advertised 4,095 context tokens. A roughly 35 KB
repeated transcript expected to exceed that metadata limit still returned HTTP
200 and `OK`. This does not establish whether upstream transformed/truncated it
or advertised metadata was stale. It did not test retention of every transcript
segment, and no true hard-limit rejection was established. There is no observed
session compaction step in the stateless API.

**Unknown:** whether canceling only an HTTP request on a shared server aborts
upstream generation promptly. Killing an owned standalone process was tested;
killing an existing session's server is not a suitable substitute.

## Pi

### Recommended command and authentication

```sh
pi -p --no-session --no-tools --no-extensions --no-mcp \
  --no-skills --no-prompt-templates --no-context-files --no-themes \
  --no-approve --offline \
  --system-prompt 'You answer briefly.' --append-system-prompt '' \
  --model openai-codex/gpt-5.6-terra --thinking minimal < prompt.txt
```

Use the selected `provider/id` and its advertised thinking level. Credentials
and provider configuration remain in their normal locations; do **not** change
`PI_CODING_AGENT_DIR` for ordinary calls merely to avoid resources, because that
also changes authentication/model configuration.

**Observed:** `pi auth check --provider openai-codex --json --no-refresh` returned
`{"status":"ready","provider":"openai-codex","authType":"oauth"}`. The stripped
print command returned exit 0, `OK\n`, empty stderr, in 3.545 s. This confirms
normal ChatGPT OAuth works without a persisted Pi session. API-key OpenRouter
also authenticated in the small-context tests below. Other provider-specific
authentication was not live-tested.

### Models, effort and output

The plugin stores `provider/id`; either `--model 'provider/id'` or separate
`--provider provider --model id` is the relevant CLI mapping. Pi accepts fuzzy
matches/custom IDs, so pass the exact catalog ID and do not rely on CLI local
validation.

**Observed:** RPC `get_available_thinking_levels` for the selected Codex model
returned `off,minimal,low,medium,high,xhigh,max`; `get_state` confirmed
`thinkingLevel:"minimal"`. The successful live call used `minimal`. Installed
docs say unsupported thinking levels are clamped to model capabilities: on the
non-reasoning GPT-3.5 test, JSON output reported `thinkingLevel:"off"` despite
the requested `minimal`.

Successful print output contained just the answer and a newline. `--mode json`
produced JSONL, including a session header even with `--no-session`, message and
settling events. That header is **not** evidence of a file being saved. There is
no built-in CLI schema flag in the installed help; arbitrary structured-output
reliability was not tested. The lower-level `pi-ai` path from the issue was not
live-tested and should not be assumed to inherit CLI credentials/configuration.

### Project/global discovery and disk writes

**Observed:** no file under `~/.pi/agent` changed in the scoped recommended-flags
run, and no native session/history file appeared for the scratch directory.
The project instruction canaries were ignored; project extension and MCP marker
commands did not run. Outside a repository, the same stripped CLI returned `OK`
and left the temporary working directory empty.

The user's actual global Pi config has no global MCP file, extensions or
packages. A positive-control test used an alternate agent root, not the user's
settings: a user-level extension's `session_start` hook and a global MCP marker
both fired in RPC startup when their loading was enabled, then **neither fired**
with `--no-extensions --no-mcp`. This tests suppression at the global resource
layer without needing a model call or copying credentials.

**Additional instruction-loading trap:** installed `resource-loader.js`
discovers `APPEND_SYSTEM.md` separately from AGENTS/CLAUDE context discovery.
A local dummy OpenAI-compatible endpoint, a dummy credential and an alternate
agent root containing `APPEND_SYSTEM.md` demonstrated:

```text
--no-context-files --system-prompt 'You answer briefly.'
  outgoing request contained GLOBAL_APPEND_CANARY
same flags plus --append-system-prompt ''
  outgoing request did not contain GLOBAL_APPEND_CANARY
```

This was a local request-capture experiment, not a live model answer. It is why
the recommended command adds an explicit empty append prompt. `--no-approve`
also prevents trust-gated project settings/resources; it does not replace
`--no-context-files`. `--offline` reduces catalog/update startup networking;
it demonstrably does **not** disable model-provider networking.

**Compatibility caveat:** disabling extensions also disables providers/models or
authentication implemented by extensions. Only built-in providers were exercised.
A model offered by normal plugin discovery may therefore be unavailable to this
stripped launch. No general stripped-mode solution for extension-defined
providers was demonstrated. Credential refreshes may still write authentication
state; no refresh was needed in these measured calls.

### Errors, input, latency and cancellation

| Test | Observed result |
| --- | --- |
| Unknown Codex model | Warning on stderr that Pi is using a custom model ID, followed by remote `model is not supported … with a ChatGPT account`; exit 1, empty stdout; 2.972 s. |
| Missing auth through an empty per-run `PI_CODING_AGENT_DIR` | Exit 1; stderr `No API key found for openai-codex` plus login guidance, empty stdout; 0.247 s. |
| Unreachable proxy | Exit 1, stderr `fetch failed`, empty stdout; 14.324 s with default retry behavior. |
| About 100 KB on stdin | Exit 0, correct one-sentence summary, empty stderr; 3.787 s. |
| Two processes concurrently | Long/short calls succeeded in 3.787/3.468 s; combined wall time 3.792 s. |
| SIGTERM awaiting network | Exit 143, 0.004 s to stop; no surviving process-group members. |
| SIGINT awaiting network | Terminated by signal 2 (shell status 130), 0.002 s; no surviving process-group members. |

For unavailable network, the same per-run unreachable proxy variables used for
OpenCode/Claude worked. Do not use `--offline` to simulate network failure.

**Small-context probe:** `openrouter/openai/gpt-3.5-turbo-0613`, advertised 4.1K
context, received the same roughly 35 KB repeated transcript as OpenCode. One
print run returned **exit 0 with empty stdout/stderr**. A second run in JSON
mode returned `OK`, `stopReason:"length"`, output usage 1 token and input usage
805 tokens, then `agent_settled` with `aborted:false`. That input count is far
below the supplied transcript estimate. There was no emitted compaction event;
the JSON stream did include a later `context_edit` entry.

The reason for the reduced accounting/empty first answer was not established;
do not label this a confirmed provider hard-limit error or assume the entire
input was preserved. It demonstrates why successful exit and advertised context
size alone are insufficient checks. True saturation of the selected Codex model
was not attempted.

## Shared cancellation method and remaining gaps

Cancellation used an owned local HTTP proxy that accepted a CONNECT request but
never contacted an upstream provider. After observing the connection, the probe
sent SIGTERM or SIGINT to the **main process**, waited up to five seconds and
checked its process group with `ps`. All six stopped in under 30 ms and left no
members. This tests cancellation during pending network I/O, not active token
streaming, remote billing cessation, or arbitrary extension/tool subprocesses.
No processes belonging to existing user sessions were signaled.

Remaining live coverage gaps:

- Other operating systems, cloud/gateway/provider-specific authentication and
  extension-defined Pi providers.
- True hard context limits and transcript retention when the provider or harness
  transforms/truncates input; only 100 KB acceptance is established.
- Cancellation of active streaming responses and shared-server HTTP request
  cancellation, rather than owned-process cancellation.
- Broader schema/refusal/retry behavior, rate-limit behavior and sustained/high
  concurrency. The experiments establish small parallel calls, not a safe pool
  size or a global rate limit.
- Complete suppression of OpenCode global plugins/MCP while retaining all of
  the user's provider configuration. A neutral directory is only partial
  isolation, and no reliable V2 launch override for that full combination was
  established here.

## References

Observed CLI help and the **running installed OpenCode server's** OpenAPI were
used rather than treating current web documentation as version-pinned evidence.
Relevant existing research: [Claude Code](harnesses/claude-code.md),
[OpenCode](harnesses/opencode.md), [Pi](harnesses/pi.md).

Installed Pi documentation inspected: `README.md`, `docs/cli.md`,
`docs/environment-variables.md`, `docs/providers.md`, `docs/models.md`,
`docs/settings.md`, `docs/configuration.md`; the append-prompt check also
inspected installed `dist/core/resource-loader.js`. Those docs/source findings
are distinguished above from live-provider and dummy-endpoint observations.
