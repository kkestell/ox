# OpenCode V2

> Imported Hypercube research; see [provenance and scope](../README.md).

## 1. Overview

- **[verified]** Initial survey: `opencode v2.1.4`, help only. Skills follow-up on 2026-11-10: `opencode v2.2.6`, top-level/serve help and a private `serve --stdio --port 0` process with temporary working directories and databases. Skill listing, prompt admission and activation were tested without model calls or editing user configuration.
- **[docs]** Anomaly's open-source OpenCode; official source https://github.com/anomalyco/opencode ; V2 documentation https://opencode.ai/v2/docs/ . Install mechanisms include native distribution and package managers; exact current repository license text, release schedule and quantitative adoption were not independently checked: unknown.
- **[docs]** This report is explicitly **V2**. V1 HTTP, plugin and config examples are not interchangeable; server/plugin APIs intentionally changed. Source: https://opencode.ai/v2/docs/migrate-v1/

## 2. Interactive terminal interface

- **[docs]** Default full-screen TUI; `opencode mini` is minimal interactive alternative; truecolor recommended, approximate palettes otherwise. Suggested terminals Ghostty/WezTerm/Alacritty/Kitty. Source: https://opencode.ai/v2/docs/cli/
- **[docs]** `cli.json` controls `mouse`, scrolling speed/acceleration, cursor style/blinking, animations, terminal title, selection copy, notifications and sounds. `terminal.title:boolean`; `terminal.copy:manual|select`; `attention.notifications/sound/volume/sound_pack/sounds`. Sound events `default,question,permission,error,done,subagent_done`. Source: https://opencode.ai/v2/docs/cli/config/
- **[docs]** Mini replay can restore history after resize; `mini.replay_limit` defaults 200. Session display sidebar/scrollbar/thinking/grouping/markdown and tab scope/layout/status indicators configurable. Source: same CLI settings reference.
- Exact alternate-screen sequences, OSC 9/9;4/7501, bell versus native notification backend, resize-specific bugs: unknown; no interactive terminal capture performed.

## 3. Launch and configuration

- **[help]** `opencode [directory] --standalone|--server URL --auto --continue --session ID --prompt TEXT`; run adds `--fork --model provider/model#variant --agent --format default|json --file --title --thinking`. Global `--log-level all|trace|debug|info|warn|warning|error|fatal|none`, `--print-logs` (server logs need standalone).
- **[docs]** V2 normally connects all user clients to one shared server. **Changing env on a new TUI does not reconfigure an already-running server**. `--standalone` owns a private server; `--server` connects explicitly. Foreground server inherits its environment; shared service has separate persisted env settings. Sources: https://opencode.ai/v2/docs/cli/ and https://opencode.ai/v2/docs/network/
- **[docs]** Server/project JSON or JSONC: global `~/.config/opencode/opencode.json(c)`; discovered direct `opencode.json(c)` from filesystem root down to cwd, then all `.opencode/opencode.json(c)` in that order. Every discovered `.opencode` config overrides every direct file, even above repository root. System config wins over user/project; MDM preferences win over system. Source: https://opencode.ai/v2/docs/config/
- **[docs]** System directories: macOS `/Library/Application Support/opencode`; Linux `/etc/opencode`; Windows `%ProgramData%\opencode`; also load agents/commands/skills/plugins. macOS managed domain `ai.opencode.managed`: `/Library/Managed Preferences/<user>/ai.opencode.managed.plist` replaces machine-level `/Library/Managed Preferences/ai.opencode.managed.plist`. Policy evaluation has different/reverse precedence from ordinary config values. Source: same config reference.
- **[docs]** CLI-only global `~/.config/opencode/cli.json` or `$XDG_CONFIG_HOME/opencode/cli.json`, **no project-local CLI config**. `OPENCODE_CLI_CONFIG_CONTENT` inline JSON merges over file, nested objects merge, arrays/scalars replace; authoritative while env set. Unknown CLI keys ignored. Source: https://opencode.ai/v2/docs/cli/config/
- **[docs]** Server keys include `model,default_agent,permissions,policies,agents,commands,plugins,providers,mcp,skills,instructions,references,formatter,lsp,snapshots,watcher,compaction,warming,worktree,tool_output,media,shell`. `{env:NAME}` and `{file:path}` string substitutions use empty string when missing; relative files resolve from config file. Source: https://opencode.ai/v2/docs/config/
- **[docs]** Config hot reload; `/reload` or location.reload rebuilds services, cancels pending forms/permissions, active sessions continue at next step boundary. SDK `OpenCode.create({plugins:[...]})` enables entirely programmatic runtime customizations without writing config. Sources: https://opencode.ai/v2/openapi.json and https://opencode.ai/v2/docs/build/sdk/
- **[verified]** Private V2 `serve` accepts `OPENCODE_CONFIG_CONTENT` inline JSON; a temporary `skills` array appeared in `GET /api/config` and affected the skill catalog (section 18). Exclusive config root and the complete server env-variable contract remain unknown.

## 4. Models and reasoning

- **[help]** `opencode models`; `run -m provider/model#variant`, agent selection. Interactive top-level help lacks a model flag; use session/config/API rather than assuming legacy `opencode -m`.
- **[docs]** Config `model:"provider/model"`; root default currently does **not retain #variant**, agents/commands may. Provider definitions include models, `modelID`, name, limit.context/output, settings, headers and variants. `providers.PROVIDER.settings.transport:"websocket"` selects provider session WS. Source: https://opencode.ai/v2/docs/config/
- **[docs]** API `model.list/default`, `provider.list/get`, `session.switchModel {sessionID,model:{providerID,id,...}}`, `session.switchAgent`. Plugins transform providers/model catalog and set defaults; model policy can remove expensive candidates. Source: https://opencode.ai/v2/docs/build/plugins/server/
- Unknown-model error/fallback exact behavior: unknown; not exercised.

## 5. Conversations

- **[help]** `--session ID` resumes or creates if missing; `--continue` selects last; run `--fork` forks first; `session list/delete/export/import`.
- **[docs]** POST `/api/session` accepts optional `id,parentID,title,agent,model,location,metadata,permissions`; parent creates linked child at parent's location. `session.list` defaults newest 50 and supports order/search/parent/archive/directory/project/subpath/cursor. `cursor.next/previous` preserve filters/order. Source: https://opencode.ai/v2/openapi.json
- **[docs]** API get/update/remove/fork/move, model/agent switches, archive timestamp, context/message list/diff, staged revert/clear/commit. Remove stops execution and recursively removes children. Fork metadata records source session/boundary. Source: same OpenAPI and https://opencode.ai/v2/docs/build/plugins/server/
- **[docs]** DB default `~/.local/share/opencode/opencode.db`; `OPENCODE_DB` overrides (relative under data directory; `:memory:` supported). `opencode debug paths db` prints effective path without starting server or opening DB. Sources: https://opencode.ai/v2/docs/troubleshooting/ and https://opencode.ai/v2/docs/cli/
- **[docs]** `session.compact` admits a control item, not completed summary; default next step boundary, `delivery:"queue"` honors queue. Config `compaction.auto`, `keep.tokens` (example 15000), `buffer` (example 15%), provider/model `settings.compaction.type:native|summary`; model overrides provider. Source: https://opencode.ai/v2/docs/config/ and plugin server reference.
- Concurrent clients in same directory share server/location/config; independent sessions do not imply isolated filesystem. Cross-server same-session locking guarantee: unknown.

## 6. Transcript and structured output

- **[help]** `run --format json`; `session export` produces JSON. Exact run JSON record shape not verified.
- **[docs]** API `session.context`, `session.message.list/get`, `session.diff`, `session.inbox.list`, experimental export/import/log provide runtime and stored projections. Export gives full projected transcript, not arbitrary raw DB pages. Source: https://opencode.ai/v2/openapi.json
- **[docs]** Native public SSE `GET /api/event`, optional `types` filter plus initial `server.connected`; envelope contains `event`, JSON-encoded `data`, optional id. **Volatile**: slow consumer overflows/fails stream, disconnected events are missed. Reconcile by rereading session/context/inbox/pending approvals, not assuming replay. Source: same OpenAPI.
- **[docs]** Session events expose execution/steps/text/reasoning/tool arguments/results/retries/compaction and usage. Todo tool `todowrite` is opt-in `experimental.todo:true`, default false. Source: https://opencode.ai/v2/docs/config/ and OpenAPI.

## 7. Hooks and events

- **[docs]** V2 hooks are typed plugin callbacks, registered with domain `.hook(name,callback,scope?)`; disposal removes registration. Registration order chains mutations. No Claude-style command-hook event schema was established. Source: https://opencode.ai/v2/docs/build/plugins/server/
- **[docs]** Session hooks: `prompt`, `context`, `compaction`, `generate`, `title`, `model.request`, `http.request`, `http.response`, `experimental.ws.handshake`, `experimental.ws.send`, `experimental.ws.receive`, `retry`. Permission hook `evaluate`; shell hook `create.before`; tool hooks `execute.before`, `execute.after`. Source: same plugin reference.
- **[docs]** Prompt event owned mutable `prompt:{text,files,agents,skills}`, `metadata`, `delivery:steer|queue`; IDs readonly; agent mentions do not switch active agent. Runs once before attachment/skill resolution and durable admission; canonical modified input persists. Synthetic/shell/compact/move controls skip prompt hook; no typed reject API. Concurrent duplicate admission can execute hook multiple times though only one wins: not exactly-once side effects. Source: same reference.
- **[docs]** Request events expose `system,messages,tools,options`; context handles agent loop, compaction/generate/title separate. Compaction/title `result` can skip model call. Mutations affect outgoing request, not durable history. Provider option objects recursive-merge, arrays/scalars replace; overrides > model > route defaults, raw HTTP overlays after lowering. Deleting option restores configured default. Source: same reference.
- **[docs]** HTTP hooks do not see session-WebSocket traffic; experimental WS hooks are separate. Retry receives attempt/error/decision; initial physical attempt is 1, first retry 2. Set `{retry:false}` or `{retry:true,delay:<ms>}`; overflow recovery compacts separately. Permission evaluate can supply decisions; tool hooks inspect/replace execution input/output, shell hook changes shell creation. Source: same reference.
- Generic hook timeout/output cap/re-entry/loop guarantee: unknown. `tool_output.max_lines/max_bytes` limits tool results, not proven universal plugin callback limits.
- **[docs]** Public event types are listed exhaustively below in section 17 from OpenAPI. Most payloads are opaque in that published OpenAPI (`V2EventEncoded` is JSON string); exact discriminated payload fields beyond documented examples remain unknown. Source: https://opencode.ai/v2/openapi.json

## 8. Plugins and extensions

- **[docs]** `Plugin.define({id,setup(ctx)})`; setup may return cleanup. Package exports server `.`/`./server`, TUI `./tui`, GUI `./gui-experimental`, RPC `./rpc`; SDK package `@opencode/plugin`. Auto local `.opencode/plugins/`; config `plugins` package/version/path/file URL or `{package,options}`. One entry loads parts; TUI-only plugins instead in cli.json. Source: https://opencode.ai/v2/docs/build/plugins/
- **[docs]** Server context is generated client plus transforms/hooks/registration/storage/options/location. `location.directory`, optional workspaceID, project `{id,directory,canonical}` is plugin instance location, **not every observed session's location**. Sources: https://opencode.ai/v2/docs/build/plugins/server/
- **[docs]** Transforms: agent list/get/default/update/remove; provider list/get/add/update/remove plus models set/update/remove; model collection list/get/update/remove/default.set; command.add executor; integration auth methods; mcp list/get/set/update/remove; reference.add/get/remove; skill.add/get/update/remove; tool.namespace/add/update/remove; VCS provider; worktree strategy; websearch provider/default. Each domain can reload; registration.dispose undoes replayable transform. Source: same reference.
- **[docs]** Transform callbacks are synchronous, cheap/repeatable; external data loaded before callback then reload replays in registration order. Later tool registration overrides same effective name; namespace sanitization makes `acme_greeting`. Every model request captures stable executable tool snapshot; later transforms affect future requests, not already captured tools. Tool executor gets AbortSignal and progress callback. Source: same reference.
- **[docs]** Plugin storage `get/set/remove/scan({prefix,after?,limit?})`; generate text/image/decisions; session controls; events; integration OAuth/command/key workflows and active credential resolution. TUI registers commands/routes/slots/Markdown rendering; GUI panels/settings/native integration; typed RPC methods/events callable over `/api/rpc/{rpcID}/{method}`. Sources: server plugin reference and https://opencode.ai/v2/docs/build/plugins/

## 9. Tools, permissions, and approvals

- **[docs]** Ordered V2 rules `{action,resource,effect:allow|deny|ask}`; agent then session rules, last match wins within evaluated rules. Session updates can replace session rules; children inherit at creation. Policies can hard-deny provider/MCP/plugin/skill/tool usage beyond ordinary permission defaults. Sources: https://opencode.ai/v2/docs/config/ and https://opencode.ai/v2/docs/build/plugins/server/
- **[help]** `--auto` autoapproves permissions not explicitly denied. **[docs]** CLI `session.permissions:prompt|autoaccept` controls terminal replies, not removing hard deny. Source: https://opencode.ai/v2/docs/cli/config/
- **[docs]** `permission.assert {sessionID,agent?,source?,action,resources,save?,metadata?}` prompts when required; `save` patterns stored for always-allow. Plain rejection ends current step; denial or rejection feedback produces Tool.Error so model can continue. Source: plugin server reference.
- **[docs]** Pending requests list globally `/api/permission/request` or per session; payload `id,sessionID,action,resources,save?,metadata?,source?,message?`; reply POST `/api/session/{sessionID}/permission/{requestID}/reply {decision,message?}`. Questions/MCP elicitation via session form list/get/reply/cancel; reply uses Form.Reply. Source: https://opencode.ai/v2/openapi.json
- **[docs]** Exact Permission.Reply enum `once|always|reject`; Form.Reply `{answer:Record<string,Form.Value>}`. TokenUsage.Info `{input:number,output:number,reasoning:number,cache:{read:number,write:number}}`. Source: https://opencode.ai/v2/openapi.json
- **[docs]** File write/edit/patch, read/search/shell, MCP resources, code mode, delegation and opt-in todos exist; exact default built-in catalog depends on agents/models/config. This survey did not establish an OS process sandbox guarantee. Source: https://opencode.ai/v2/docs/config/ and https://opencode.ai/v2/docs/mcp-servers/

## 10. Subagents and delegation

- **[docs]** Agents may have `mode:"subagent"`, model/system/permissions. `experimental.subagent_depth` default 1 means subagents cannot spawn further subagents. Child sessions link with parentID and inherit parent session permissions. Source: https://opencode.ai/v2/docs/config/ and plugin server reference.
- Restrict delegation using permissions/tool transforms/agent transforms, not merely hiding UI. Exact built-in delegation tool ID and complete disable recipe not independently established: unknown.
- **[docs]** API session.background and persistent shell/PTY APIs provide background execution distinct from child agent sessions. Source: https://opencode.ai/v2/openapi.json

## 11. MCP

- **[docs]** V2 `mcp.servers.NAME`, not V1 names directly under mcp. Local `{type:"local",command:[exe,...],cwd?,environment?,disabled?,codemode?,timeout?,protocol?}`; remote `{type:"remote",url,headers?,oauth?,disabled?,codemode?,timeout?,protocol?}` uses Streamable HTTP, remote OAuth default. Server object is replaced wholesale at higher config precedence. Source: https://opencode.ai/v2/docs/mcp-servers/
- **[docs]** Timeouts positive integer ms: startup 30s, catalog 30s, execution 12h; `mcp.timeout` defaults, per-server timeout overrides. `protocol:legacy` default up to 2025-11-25, `auto` probes server/discover and falls back, `2026-07-28` requires new revision. Auto local probing may spawn an extra temporary process and wait startup timeout. Source: same MCP reference.
- **[docs]** Remote OAuth PKCE/refresh; method precedence configured client_id > CIMD > DCR. Keys `client_id,client_secret,scope,callback_port,redirect_uri,auth_server_metadata_url`. Callback ephemeral unless pinned; CIMD document https://opencode.ai/oauth/opencode/client.json ; custom redirect_uri disables CIMD. Source: same reference.
- **[docs]** Tools `<server>_<tool>` sanitized, Code Mode grouped by server default; `codemode:false` exposes native tool list. Prompts become `/server:prompt`, argument text positional/quoted. Builtin list_mcp_resources/read_mcp_resource and permission actions `opencode_list_mcp_resources` / `opencode_read_mcp_resource`, resources server or server:uri. Server instructions added under mcp_instructions unless tools entirely denied. Source: same reference.
- **[docs]** Elicitation form and URL supported; dismiss cancels. Calls send `_meta["ai.opencode/sessionID"]` for session correlation over both transports, can be absent, not auth. Server catalog/resource status events are exposed; unsolicited arbitrary user-turn push not established. Source: same reference.
- **[docs]** SDK/plugin per-runtime MCP transform and experimental MCP add/remove/connect/disconnect APIs avoid editing user files. Source: server plugin reference and OpenAPI.

## 12. Control from outside

- **[help]** `serve --hostname --port --cors` (repeatable), `--service`, `--stdio`; `acp` starts Agent Client Protocol; `api` addresses running service. `--server` attaches TUI/run to explicit HTTP server; `--standalone` private.
- **[docs]** `@opencode/client` generated HTTP client and Effect entrypoint; local Service handles discovery/start/auth. `@opencode/sdk` `OpenCode.create()` embeds host with in-memory HTTP router/no listener; `await using` or `.close()` releases resources; aliases `.sessions/.events`. `.plugin(plugin)` adds runtime plugin. Sources: https://opencode.ai/v2/docs/build/client/ and https://opencode.ai/v2/docs/build/sdk/
- **[docs]** `session.prompt {sessionID,id?,text,files?,agents?,skills?,metadata?,delivery?:steer|queue,resume?}` durably admits input; `resume:false` does not schedule loop. `synthetic`, `command`, `shell`, `generate`, `compact`, instruction-entry put/remove, interrupt/wait/background and inbox update/cancel allow steering while live. Source: https://opencode.ai/v2/openapi.json
- **[docs]** Session interrupt `{resume:false}` supported; synthetic messages differ from user prompts; keyed instructions render context blocks and updates announced next step without queuing user turn. Source: https://opencode.ai/v2/docs/build/plugins/server/
- **[docs]** Pairing creates short-lived single-use code, `/auth/connect/{code}` gives browser cookie/redirect or JSON session token used as password. Full endpoint inventory section 17. Source: https://opencode.ai/v2/openapi.json

## 13. Status from outside

- **[docs]** GET `/api/session/active` maps currently process-owned foreground drains to `{type:"running"}`; absent means inactive **on this process**, not proof no other server owns work. Session.Info has outcome `succeeded|failed|interrupted`, time.created/updated and optional busy/idle/viewed/archive timestamps. Source: https://opencode.ai/v2/openapi.json
- **[docs]** Execution start/success/failure/interruption, step/retry events, permission.asked/replied and form.created/replied/cancelled distinguish busy/error/waiting. Read pending lists after reconnection; event stream is non-durable. Source: same OpenAPI.
- **[docs]** `service status` and `/api/info` show server readiness/identity/paths/PID. `opencode api` can start a server as a side effect; `debug paths` does not. Sources: https://opencode.ai/v2/docs/troubleshooting/ and CLI reference.

## 14. Usage, cost, and limits

- **[docs]** Session.Info `cost,tokens`; `session.usage.updated`; experimental session stats and `/api/experimental/usage/limits`. `opencode stats` is CLI aggregate usage. Sources: https://opencode.ai/v2/openapi.json and local help.
- **[docs]** `warming` disabled by default; true defaults four-minute interval and 30-minute active window, transient calls still cost money. Tool output defaults configured example 2,000 lines/51,200 bytes; compaction buffer/retained tokens configurable. Source: https://opencode.ai/v2/docs/config/
- Per-process hard dollar budget and shared rate limiter behavior: unknown.

## 15. Authentication

- **[help]** `auth list/login/logout/export/import/switch`; list inspects integration/credential state, export intentionally prints stored secrets (do not use as health check).
- **[docs]** Integration API list/get, key/external connect, OAuth start/status/complete/cancel, command start/status/cancel; credential list/create/update/remove/activate. Active connection may be resolved by trusted plugin. Credentials are stored outside project config. Source: https://opencode.ai/v2/openapi.json and plugin server reference.
- **[docs]** Explicit server clients can use `OPENCODE_PASSWORD`; service discovery handles local auth automatically. Sources: https://opencode.ai/v2/docs/troubleshooting/ and https://opencode.ai/v2/docs/build/client/
- Exact V2 credential-at-rest files and guaranteed no-network expired-token check: unknown. Do not assume V1 auth.json layout.

## 16. Running many instances

- **[docs]** Default clients share one user service, location runtimes, auth, plugins and DB; `--standalone` isolates server lifetime **not automatically storage**. `OPENCODE_DB` separates DB, XDG paths separate config/state. Sources: CLI/troubleshooting/config references above.
- **[docs]** Service registration `~/.local/state/opencode/service.json`, private service settings `~/.config/opencode/service.json`; changing service config stops shared service. Log `~/.local/share/opencode/log/opencode.log` includes process run ID and role=cli|server. Source: https://opencode.ai/v2/docs/troubleshooting/
- **[docs]** `experimental.location_idle_timeout` default 60 minutes, duration or false; running sessions/background jobs keep location loaded. Worktree destination config resolves from project's canonical checkout; plugins choose strategy, last active registration wins and removal uses recorded owner. Source: config and plugin server references.
- RSS, SQLite contention, maximum active sessions and same-ID multiple-server ownership safety: unknown; not measured.

## 17. Anything else a program driving this harness should know

- **[docs]** Proxy `HTTP_PROXY,HTTPS_PROXY,NO_PROXY=localhost,127.0.0.1,::1`; loopback exclusion required because client/service communicate HTTP. `NODE_EXTRA_CA_CERTS` applies at process startup. Persisted shared-service env differs from shell/standalone env. Source: https://opencode.ai/v2/docs/network/
- **[docs]** Diagnostic SIGPROF captures ten-second CPU profile; SIGUSR1 heap snapshot on macOS/Linux. `debug heap-dump` works across platforms and never starts/replaces server, but pauses/increases memory and can expose secrets. Do not signal these as generic interrupts. Source: https://opencode.ai/v2/docs/troubleshooting/
- **[docs]** OpenAPI below is public moving contract (info.version `0.0.1`), not a contract generated from installed 2.1.4. API route details should be compared to `/openapi.json` on the server actually driven. Source: https://opencode.ai/v2/openapi.json

**[docs]** Complete public SSE filter enum from the reviewed OpenAPI (plus arbitrary `rpc.*` event types); every entry below has source https://opencode.ai/v2/openapi.json . `server.connected` is always initial, even when filtered:

```text
location.shutdown
models-dev.refreshed
credential.updated credential.switched integration.updated provider.updated model.updated agent.updated
session.created session.agent.selected session.model.selected session.moved session.renamed
session.metadata.updated session.permissions session.viewed session.archived session.unarchived
session.usage.updated session.deleted session.forked
session.inbox.delivered session.inbox.enqueued session.inbox.cancelled session.inbox.delivery.changed
session.execution.started session.execution.succeeded session.execution.failed session.execution.interrupted
session.instructions.updated session.synthetic session.skill.activated
session.shell.started session.shell.ended
session.step.started session.step.streamed session.step.ended session.step.failed
session.text.started session.text.delta session.text.ended
session.reasoning.started session.reasoning.delta session.reasoning.ended
session.tool.input.started session.tool.input.delta session.tool.input.ended
session.tool.called session.tool.progress session.tool.success session.tool.failed
session.retry.scheduled
session.compaction.started session.compaction.delta session.compaction.ended session.compaction.failed
session.revert.staged session.revert.cleared session.revert.committed
filesystem.changed reference.updated permission.asked permission.replied plugin.updated project.updated
worktree.updated worktree.resolved command.updated config.updated skill.updated
pty.created pty.updated pty.exited pty.deleted
persistent-pty.added persistent-pty.removed persistent-pty.updated
shell.created shell.exited shell.deleted
form.created form.replied form.cancelled websearch.updated artifact.updated
session.status session.idle
tui.prompt.append tui.command.execute tui.toast.show tui.session.select
installation.updated installation.update-available vcs.branch.updated
mcp.status.changed mcp.resources.changed
```

**[docs]** Additional complete operation-ID inventory by domain, beyond the primary session APIs described above. Operation IDs are accepted by `opencode api`, so they are directly usable without reconstructing routes. Every name below is from https://opencode.ai/v2/openapi.json ; full path/request/response schemas are retained in `sources/opencode-api.txt`:

```text
server.info server.pair server.connect
location.get location.reload
agent.list agent.get
plugin.list plugin.gui plugin.check plugin.update
session.list session.create session.active session.get session.remove session.update session.fork
session.switchAgent session.switchModel session.move session.prompt session.command session.synthetic
session.shell session.compact session.revert.stage session.revert.clear session.revert.commit
session.context session.diff session.inbox.list session.inbox.cancel session.inbox.update session.generate
session.interrupt session.background session.message.get session.message.list
session.form.list session.form.create session.form.get session.form.cancel session.form.reply
session.environment session.view
experimental.session.stats experimental.session.import experimental.session.export experimental.session.skill
experimental.session.wait experimental.session.log
experimental.session.instructions.entry.list experimental.session.instructions.entry.put
experimental.session.instructions.entry.remove
model.list model.default
experimental.generate.text experimental.generate.textLegacy experimental.generate.image experimental.generate.decisions
experimental.voice.models experimental.voice.session
provider.list provider.get
integration.list integration.get integration.connect.key integration.connect.external
integration.oauth.connect integration.oauth.status integration.oauth.cancel integration.oauth.complete
integration.command.connect integration.command.status integration.command.cancel
experimental.integration.wellknown.add
mcp.list mcp.resource.catalog
experimental.mcp.add experimental.mcp.remove experimental.mcp.connect experimental.mcp.disconnect
experimental.mcp.client.token experimental.mcp.client
credential.list credential.create credential.update credential.remove credential.activate
project.list project.update
form.list
permission.request.list permission.saved.list permission.saved.update permission.saved.remove
session.permission.create session.permission.list session.permission.get session.permission.reply
fs.read fs.list fs.find experimental.fs.write experimental.fs.watch experimental.fs.unwatch
command.list skill.list rpc.call event.subscribe
pty.list pty.create pty.get pty.update pty.remove pty.connect.token pty.connect
experimental.persistentPty.read experimental.persistentPty.list experimental.persistentPty.create
experimental.persistentPty.shutdown experimental.persistentPty.handoff experimental.persistentPty.get
experimental.persistentPty.update experimental.persistentPty.remove experimental.persistentPty.snapshot
experimental.persistentPty.connectToken experimental.persistentPty.connect
experimental.media.get
experimental.schedule.create experimental.schedule.list experimental.schedule.get experimental.schedule.delete
experimental.schedule.pause experimental.schedule.resume
experimental.artifacts.providers experimental.artifacts.list experimental.artifacts.create experimental.artifacts.get
experimental.artifacts.update experimental.artifacts.delete experimental.artifacts.view
shell.list shell.create shell.get shell.remove shell.output
reference.list
worktree.list worktree.create worktree.remove worktree.refresh
vcs.init vcs.get vcs.base vcs.status vcs.branch.list vcs.diff
debug.heapDump debug.location.list debug.location.evict
experimental.migration.v1.status
websearch.providers websearch.query experimental.usage.limits
config.get config.shells experimental.config.update
```

**[docs]** Exact hook payload fields learned from the server plugin reference https://opencode.ai/v2/docs/build/plugins/server/ :

- SessionRequestHook: readonly sessionID/model `{providerID,id,variant?}`; mutable system/messages/options `{maxTokens?,temperature?,topP?,topK?,frequencyPenalty?,presencePenalty?,seed?,stop?:string[],...providerOptions}`. Context adds readonly agent and mutable tools record of `{description,input:JsonSchema}`.
- WS handshake: readonly sessionID/agent/model/kind `primary|compaction|title|generate`, mutable url/headers; send/receive same identity/kind plus mutable frame:string.
- Retry: readonly sessionID/agent/model/error `{type,message,status?}`/attempt; mutable decision `{retry:false}|{retry:true,delay:number}`.
- CompactionResult `{summary,native?,metadata?,tokens?}`. PermissionEvaluation readonly sessionID/agent?/action/resources/metadata?/source `{type:'tool',messageID,id}`, mutable effect/message. Configured deny is final and skips hook; allow/ask may become allow/ask/deny.
- ShellCreateBefore mutable command/cwd/timeout/shell/env Record<string,string|undefined>.

## 18. Agent skills

### Discovery, IDs, and frontmatter

- **[docs]** Default sources: `~/.config/opencode/skills`, compatibility `~/.claude/skills` and `~/.agents/skills`, project `.opencode/skills` and compatibility `.claude/skills` / `.agents/skills` from cwd through the project root. System configuration directories also load skills (section 3). Explicit config `skills:[directory-or-catalog-URL,...]` adds sources; all discovered documents contribute their arrays. Relative skill paths resolve from the active working directory, not the config file. Sources: https://opencode.ai/docs/skills and https://opencode.ai/docs/config
- **[docs]** Sources accept root-level `*.md` and nested `<name>/SKILL.md`, recursively through grouping directories to depth six; finding `SKILL.md` stops recursion inside that skill. Hidden entries and `node_modules` are skipped. Frontmatter is optional. `name` is a display label, **not the ID**; `description` enables model advertisement. `metadata.opencode/autoinvoke:false` suppresses advertisement and takes precedence over `disable-model-invocation:true`; explicit loading remains possible. Other portability fields such as `license` and `compatibility` are not interpreted. IDs are case-sensitive path-derived basenames; neither the recommended kebab-case pattern nor matching frontmatter `name` is enforced. Source: https://opencode.ai/docs/skills
- **[docs]** Later sources replace earlier definitions of the same ID. The skills guide orders builtins, Claude compatibility sources (global, then farthest project ancestor to cwd), Agents compatibility sources in the same order, native global, native project root to cwd, then explicit config sources in config/array order. System-directory position relative to compatibility sources is not specified by that table: unknown from the guide. Native config source code scans both `skill/` and `skills/` under configuration directories, then explicit entries. Sources: same skills guide and https://github.com/anomalyco/opencode/blob/v2.2.6/packages/core/src/config/plugin/skill.ts

### Extra directory for one launch

- **[verified]** `OPENCODE_CONFIG_CONTENT='{"skills":["/absolute/skills/root"]}'` on a newly launched private server adds a directory containing **many** `<name>/SKILL.md` skills. A two-skill probe listed both. This does not require editing the real global/project config or replacing `HOME`. It configures that server's location runtimes, not one native session object; it cannot reconfigure an already-running shared server merely by changing a client environment.
- **[docs]** In an embedded host, `OpenCode.create({plugins:[...]})` or `.plugin(plugin)` permits runtime `ctx.skill.transform(editor => editor.add(Skill.Info))`. `Skill.Info` supplies `id,name,path,content` and optional `description,autoinvoke`. This registers individual definitions; the transform API itself is not a directory-scanning option. Sources: https://opencode.ai/docs/build/sdk and https://opencode.ai/docs/build/plugins/server#skills
- **[verified]** `plugins/opencode/server.go` already owns a private `opencode serve --stdio --port 0` subprocess and supplies per-launch env. `serve --help` exposes no dedicated skill-directory flag. No per-session native skill-source config field was established: unknown; the tested source/catalog scope is server/location.

### Invocation through Hypercube's input path

- **[verified]** `plugins/opencode/input.go` handles `/compact` specially, then looks up other slash names in `GET /api/command` and calls `session.command` only on a match. Otherwise it calls `POST /api/session/{sessionID}/prompt` with `text,files,delivery:"steer"`. Skills in the extra directory appeared in `/api/skill` but **not** `/api/command`. Submitting `text:"/hc-probe-one args",resume:false` preserved plain text with no skill attachment. Therefore literal `/name args` does **not** deterministically invoke a registered skill through this input path; a separate same-name command may run, or the model may later choose the skill from ordinary text.
- **[verified]** The structured alternative on that same prompt endpoint is `{"text":"args","skills":[{"id":"hc-probe-two"}],"resume":false}`. Admission resolved the ID and persisted an attachment `{id,name,text}` containing the harness-rendered `<skill_content>` block, base directory and sampled file list. The input attachment also accepts optional `mention:{start,end,text}`; mention offsets are unnecessary when supplying only `id`. `resume:false` was used only to prevent the probe from starting a model loop. `text:""` with a skill attachment is accepted; omitting `text` returns 400 `Missing key ["text"]`. The admitted message's `payload.skills[].text` holds the rendered skill content.
- **[verified]** `POST /api/experimental/session/{sessionID}/skill {id,resume:false}` activates a skill without a prompt: context immediately contained `{type:"skill",skill:ID,name,text}`. This endpoint has no argument-text field. The installed `/openapi.json` describes both this operation (`experimental.session.skill`) and prompt `skills` attachments. **[docs]** The user-facing skill mention syntax is `@skill-id`, not `/skill-id`; that does not establish parsing of raw API prompt text. Sources: installed private-server OpenAPI and https://opencode.ai/docs/skills

### Context and model invocation

- **[docs]** Before model steps, the harness advertises permitted skills with descriptions and `autoinvoke` not false, including explicit extra sources. The model calls `skill {id}`; loading checks the selected agent's skill permission and returns body text without frontmatter, base directory, and up to ten sample supporting paths. `deny` hides/rejects a skill; `ask` can prompt on loading. `autoinvoke:false` hides the description but does not forbid an explicitly named ID. Source: https://opencode.ai/docs/skills
- **[verified] [docs]** Explicit prompt attachments are resolved at admission, rather than by the host expanding the body. Standalone activation stores a separate skill message. V2.2.6's history-to-model code renders both forms as user content; model-invoked skills instead arrive in tool results. Source: live probes above and https://github.com/anomalyco/opencode/blob/v2.2.6/packages/core/src/session/runner/to-llm-message.ts
- **[docs]** There is no unconditional Claude-style skill re-attachment in the reviewed V2.2.6 history-selection code. Summary compaction keeps a summary and recent serialized history: skill attachments/standalone skill messages in the kept range include their text, while ordinary tool results (including model-loaded skill output) are clipped to 1,250 characters in that recent record. Older bodies are subject to lossy summarization. Native compaction's retained user-message helper explicitly removes skill attachments; the provider checkpoint is opaque. Skill descriptions remain part of the normal instruction catalog, but that is not preservation of invoked bodies. Sources: https://opencode.ai/docs/compaction and versioned source files https://github.com/anomalyco/opencode/blob/v2.2.6/packages/core/src/session/history.ts , https://github.com/anomalyco/opencode/blob/v2.2.6/packages/core/src/session/recent-text.ts and https://github.com/anomalyco/opencode/blob/v2.2.6/packages/core/src/session/compaction.ts . No live compaction-survival test was run; model behavior afterward remains unknown.

### Listing for autocomplete

- **[verified]** **`GET /api/skill` (`skill.list`)**, not `/api/command`, returns `{location,data:[{id,name,description?,autoinvoke?,path,content}]}`. It accepts optional `location[directory]`; the private server defaults to its working directory. It returns the registered catalog, not a per-session permission-filtered list. Entries use bare IDs such as `hc-project` and `hc-probe-one`, with no slash/plugin namespace added; frontmatter display names can differ. Live probes listed native user/compatibility sources, a temporary `.opencode/skills/hc-project` skill, and both skills from the extra directory together.
- **[verified]** Listing required a running server/location runtime but **no native session or model request**. Immediately after server readiness the catalog could be empty until plugin activation; reading after activation returned the full catalog. Hypercube's existing `awaitCatalog` observes activation for other catalogs. **[docs]** Client/plugin `skill.list()` exposes the same list; `opencode api get /api/skill` can start/connect to the service, so it is not a filesystem-only offline command. Sources: installed OpenAPI, `plugins/opencode/configure.go`, https://opencode.ai/docs/api and https://opencode.ai/docs/build/plugins/server#skills
- **[verified]** The catalog follows the filesystem: a skill added to project `.opencode/skills` after a session was created appeared in `GET /api/skill` within five seconds, with no reload request.
