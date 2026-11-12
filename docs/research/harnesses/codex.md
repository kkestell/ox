# Codex CLI

> Imported Hypercube research; see [provenance and scope](../README.md).

## 1. Overview

- **[verified]** `codex --version`: `codex-cli 0.164.0`. Top-level, exec, app-server, queue and login-status help checked; no model calls or sessions started.
- **[docs]** OpenAI, Apache-2.0 open source; native Rust CLI. Install `npm install -g @openai/codex`, Homebrew cask, or platform release binary. Source: https://github.com/openai/codex
- Release cadence, current quantitative adoption: unknown. Public documentation is moving and newer than many older Codex integrations.

## 2. Interactive terminal interface

- **[help]** Default TUI has alternate-screen mode; `--no-alt-screen` runs inline preserving scrollback. `--remote ws://...|wss://...|unix://|unix://PATH` connects terminal UI to an app-server, `--no-daemon` avoids the shared background server.
- **[docs]** TUI settings include `tui.notifications`, `tui.notification_method` and status-line customization; notification method supports terminal escape/bell choices. Source: https://developers.openai.com/codex/config-reference
- Exact OSC 9/9;4/7501 emission, title format, mouse sequences, resize protocol, terminal-specific bugs in 0.164.0: unknown; no live TUI launched.

## 3. Launch and configuration

- **[help]** Arbitrary per-process `-c/--config key=value` uses dotted keys and TOML parsing (falls back to literal string); repeatable `--enable/--disable FEATURE` is `features.NAME=true|false`. `--strict-config` rejects unrecognized fields. `-p/--profile NAME` layers `$CODEX_HOME/NAME.config.toml`, not the old `[profiles.NAME]` assumption.
- **[help]** Launch inputs: initial positional prompt, repeatable images, `--model`, `--oss`, `--local-provider lmstudio|ollama`, `--cd`, `--worktree`, `--add-dir`, sandbox/approval flags, `--search`, remote/token-env selection. Exec adds `--ignore-user-config`, `--ignore-rules`, `--ephemeral`, `--thread-source`, `--skip-git-repo-check`, `--color`, `--json`, `--output-schema`, `--output-last-message`.
- **[docs]** Precedence, highest first: CLI/config overrides; trusted project `.codex/config.toml` from repo root to cwd (nearest wins); selected profile file; user `$CODEX_HOME/config.toml`; cloud-managed defaults; `/etc/codex/config.toml`; builtins. Untrusted project layers, hooks and rules are skipped, but user/system layers remain. Managed `requirements.toml` constrains allowed values rather than merely supplying defaults. Source: https://developers.openai.com/codex/config-basic
- **[docs]** `CODEX_HOME` defaults `~/.codex` and also controls auth/state; override it to isolate a worker, not just config. Useful keys: `model`, `model_provider`, `model_reasoning_effort`, `model_reasoning_summary`, `model_verbosity`, `approval_policy`, `sandbox_mode`, `shell_environment_policy`, `mcp_servers`, `features`, `agents`, `hooks`, `notify`, `tui`, telemetry. Source: https://developers.openai.com/codex/config-reference
- **[help]** `app-server --print-startup-config` validates config/prints requirements JSON without starting server; `--startup-client-info` supplies ClientInfo JSON. Do not use `--startup-sign-out` as an inspection flag (it signs out).

## 4. Models and reasoning

- **[help]** `-m/--model MODEL`, `--oss`, `--local-provider`; use `-c model_provider=...` and `-c model_reasoning_effort=...` for one process.
- **[docs]** `model/list {limit?,cursor?,includeHidden?}` returns `id`, `model`, `displayName`, `hidden`, `defaultReasoningEffort`, `supportedReasoningEfforts[{reasoningEffort,description}]`, `inputModalities`, `upgrade?/upgradeInfo?`, `isDefault`; deprecated `supportsPersonality` is always false. Missing inputModalities means text+image for backward compatibility. `modelProvider/capabilities/read` supplies capability bounds. Source: https://developers.openai.com/codex/app-server
- **[docs]** Thread/turn requests can override model/effort; resuming with a different model warns and adds a one-time model-switch instruction. Source: same app-server reference.
- Unknown model behavior without provider call: unknown; not tested.

## 5. Conversations

- **[help]** `resume` and `fork` pick sessions, `--last` selects most recent; exec has resume/fork too. `archive`, `unarchive`, `delete`, `agents` and `queue` are CLI session controls. No chosen new-session UUID flag found in top-level/exec help.
- **[docs]** Server `thread/start`, `thread/resume {threadId,excludeTurns?}`, `thread/fork {threadId,lastTurnId?,ephemeral?,excludeTurns?}`, `thread/read`, `thread/list`, `thread/loaded/list`, `thread/archive`, `thread/unarchive`, `thread/delete`, `thread/name/set`, `thread/metadata/update`, `thread/compact/start`, `thread/revert`. Fork refuses an in-progress lastTurnId; omitted boundary while mid-turn records interruption. Delete recursively removes spawned descendants. Source: https://developers.openai.com/codex/app-server
- **[docs]** Thread objects distinguish `id` and `sessionId` (live session-tree root); persistent forks retain root sessionId, ephemeral forks may have own root. Do not derive sessionId from threadId. New persistent threads default to paginated history. `historyMode:"legacy"|"paginated"` is experimental. `thread/read includeTurns:true` and full history in resume/fork are deprecated for paginated threads. Source: same app-server reference.
- **[docs]** Use `thread/turns/list {threadId,limit,cursor,sortDirection,itemsView:"notLoaded"|"summary"|"full"}` and `thread/items/list {threadId,turnId?,cursor,sortDirection}`. Page responses have `nextCursor` and `backwardsCursor`; item-anchor cursor `{type:"item",itemId}` requires turnId, excludes anchor, and invalid/out-of-scope IDs return -32602. Source: same reference.
- **[docs]** `thread/list` filters include `modelProviders`, `sourceKinds`, `archived`, `isPinned`, `cwd`, `useStateDbOnly`, `searchTerm`, experimental `parentThreadId|ancestorThreadId`; search is case-sensitive title fragment. Default sources are interactive `cli,vscode`, not every exec/app-server thread. `useStateDbOnly:true` avoids scanning/repairing logs. Source: same reference.
- Exact storage layout for current paginated history and simultaneous independent-server writer guarantees: unknown. Legacy JSONL rollouts and SQLite metadata coexist; migration commands exist **[help]** `migrate-rollouts`, `migrate-nfs-db`.

## 6. Transcript and structured output

- **[help]** `codex exec --json` writes JSONL events; `--output-schema FILE` constrains final response, `-o FILE` saves last response; `--ephemeral` disables session-file persistence. Piped stdin plus prompt is appended as a `<stdin>` block; omitted prompt or `-` reads stdin.
- **[docs]** Exec stream includes `thread.started`, `turn.started`, `turn.completed`, `turn.failed`, `item.started`, `item.updated`, `item.completed`, `error`; items include agent messages, reasoning, commands, file changes, MCP calls, web search and todo lists. Completed-turn usage includes input/cached/output token counts. Source: https://developers.openai.com/codex/non-interactive-mode
- **[docs]** App-server events are a separate slash-named protocol: `item/started`, `item/completed`, `item/agentMessage/delta`, tool/command deltas, `turn/plan/updated`, `turn/diff/updated`, etc. Do not parse exec events as app-server records. Source: https://developers.openai.com/codex/app-server
- **[docs]** Hook `transcript_path` is convenience only, not a stable schema; paginated read APIs are preferable to tailing legacy log files. Sources: https://developers.openai.com/codex/hooks and app-server reference.

## 7. Hooks and events

- **[docs]** Hooks discovered from user/project `hooks.json`, config inline `[hooks]`, plugins and managed requirements; hook trust review applies. `--dangerously-bypass-hook-trust` bypasses trust only for this invocation; `features.hooks=false` turns normal hooks off subject to policy. Sources: https://developers.openai.com/codex/hooks and local help.
- **[docs]** Events: `SessionStart`, `SessionEnd`, `SubagentStart`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `UserPromptSubmit`, `SubagentStop`, `Stop`, `Interrupt`. Do not import Claude's larger event enum into Codex. Source: https://developers.openai.com/codex/hooks
- **[docs]** Handler types `command` and `mcp_tool`; matcher groups select lifecycle/tool names. Common stdin JSON: `session_id` (parent ID for subagents), `transcript_path:string|null`, `cwd`, `hook_event_name`, `model`; turn-scoped events add `turn_id`. Most action hooks include `permission_mode:default|acceptEdits|plan|dontAsk|bypassPermissions`. Source: same hooks reference.
- **[docs]** Tool events include tool name/input and tool-use ID; PostToolUse includes JSON `tool_response`. `PreToolUse` supports `hookSpecificOutput.permissionDecision:allow|ask|deny`, reason and modified input; `PermissionRequest` supports approval decisions. Stop/SubagentStop use `decision:"block",reason` to request another turn; `stop_hook_active` identifies a previous continuation. UserPromptSubmit can block or add context. Source: same hooks reference.
- **[docs]** Shared output (only supported events): `continue`, `stopReason`, `systemMessage`, `suppressOutput` (parsed but not implemented). PreToolUse/PermissionRequest do not support continue/stopReason/suppressOutput: unsupported output fails hook and tool continues. Explicit valid denial can block, but callback errors/timeouts/malformed PreToolUse responses are **not fail-closed**. Source: same hooks reference.
- **[docs]** Default timeout 600 seconds for most hooks; Interrupt defaults one second and clamps 1–3 seconds, cannot prevent interruption. Command `async:true` runs background; SessionEnd always synchronous/advisory. Background informational output reaches next model request, not an autonomous new turn; shutdown can discard undelivered output. Source: same hooks reference.
- **[docs]** Model-visible output default ~2,500 tokens per message, oversized text spills to `<temp_dir>/hook_outputs/<session_id>/<uuid>.txt` with head/tail preview. `additionalContextLimit` overrides for context (positive integer; 0 unlimited); feedback/continuation prompts retain default. MCP hook timeout excludes elicitation wait; shorter hook/server timeout applies. Source: same hooks reference.

## 8. Plugins and extensions

- **[help]** `codex plugin` manages plugins; feature toggles/config overrides allow per-process enablement without editing files.
- **[docs]** Plugins bundle skills, apps/connectors, MCP servers and hooks; app-server plugin/marketplace APIs are explicitly under development and not for production clients. `plugin/list/read/install/uninstall`, `plugin/skill/read`, `marketplace/add/remove/upgrade`, `skills/list`, `skills/extraRoots/set`, `skills/config/write`, `experimentalFeature/enablement/set` expose these surfaces. Source: https://developers.openai.com/codex/app-server
- **[docs]** Experimental `thread/start.dynamicTools` lets host advertise tools; definitions persist and restore on resume unless replaced. Host handles `item/tool/call`; this is not an unrestricted in-process TUI plugin API. Source: same reference.
- Native arbitrary TUI widget/provider-registration plugin API: unknown. Do not confuse MCP/skills/hook bundles with Pi-style extensions.

## 9. Tools, permissions, and approvals

- **[help]** Sandbox modes `read-only|workspace-write|danger-full-access`; approval policies `on-request|never`. `--approve-for-me` routes approvals through automatic review with workspace-write. `--dangerously-bypass-approvals-and-sandbox` disables both; `--add-dir` widens writable roots. `--search` enables native live web search without per-call approval.
- **[docs]** Tools include shell/exec, patch/file edits, web search, plan updates, MCP, code-mode tools and delegation; exact tool inventory varies by model/features/provider. Rules and sandbox policy are separate from hook trust. Source: https://developers.openai.com/codex/config-reference
- **[docs]** Server requests `item/commandExecution/requestApproval`, `item/fileChange/requestApproval`, `item/permissions/requestApproval`; correlate their JSON-RPC IDs and return supported decision variants. Tool questions use experimental `item/tool/requestUserInput`; MCP uses `mcpServer/elicitation/request`. Render questions/options and reply structurally instead of typing blindly into PTY. Source: https://developers.openai.com/codex/app-server
- **[docs]** Command decisions `accept|acceptForSession|decline|cancel` or `{acceptWithExecpolicyAmendment:{execpolicy_amendment:[...]}}`; file decisions same strings. Requests include itemId/threadId/turnId, optional reason/command/cwd/commandActions/proposedExecpolicyAmendment/networkApprovalContext/availableDecisions; experimental additionalPermissions absolute paths. Network requests grouped by host+protocol+port, may unblock several commands. `serverRequest/resolved {threadId,requestId}` clears accepted or expired prompts. Source: same app-server reference.
- **[docs]** Permissions approval returns only granted subset in permissions, scope turn (default) or session; extra permissions ignored. MCP elicitation input mode form|openai/form with message/requestedSchema or url with message/url/elicitationId; reply `{action:accept,content}` or `{action:decline|cancel,content:null}`. Tool user input autoResolutionMs integer|null permits host timeout. Source: same reference.
- **[docs]** `thread/shellCommand` explicitly runs **outside sandbox with full access**, even when attached to sandboxed thread; intended only for user-initiated commands. `process/spawn` is another experimental outside-sandbox API; `command/exec` uses server sandbox. Source: same reference.

## 10. Subagents and delegation

- **[docs]** `features.multi_agent=true` is stable/default in current docs; per-agent configuration under `agents`, limits such as `agents.max_threads` and depth, role definitions and tool-based spawn/message/wait/close behavior. Disable per process with `--disable multi_agent` or `-c features.multi_agent=false`. Sources: https://developers.openai.com/codex/config-basic and https://developers.openai.com/codex/multi-agent
- **[docs]** Native child threads appear as `subAgent*` source kinds, parent/ancestor filters and collaboration items/events; SubagentStart/Stop hooks observe delegation. Source: https://developers.openai.com/codex/app-server
- **[docs]** Experimental background-terminal list/terminate/clean methods operate on shell terminals, not the same thing as native subagent threads. Source: same reference.

## 11. MCP

- **[docs]** `mcp_servers.NAME` config supports stdio and Streamable HTTP with command/args/env/cwd or URL/header/bearer auth, enabled/required flags and tool filtering/timeouts. A required server failing startup fails thread/start or resume. Source: https://developers.openai.com/codex/config-reference and https://developers.openai.com/codex/app-server
- **[docs]** APIs: `mcpServerStatus/list {detail:"full"|"toolsAndAuthOnly",serverName?,threadId?,cursor?,limit?}`, `mcpServer/resource/read`, `mcpServer/tool/call`, `mcpServer/oauth/login`, `config/mcpServer/reload`; startup/OAuth status notifications. Auth flow returns URL rather than requiring model call. Source: app-server reference.
- **[docs]** Elicitation requests are host-visible, including form/URL; extended OpenAI forms require initialize capability `mcpServerOpenaiFormElicitation`. Source: same reference.
- Unsolicited MCP notification directly starting a turn and legacy SSE support: unknown.

## 12. Control from outside

- **[docs]** Preferred rich-control surface: `codex app-server`, bidirectional JSON-RPC-like protocol **without** wire `jsonrpc:"2.0"`. Request `{id,method,params}`, response `{id,result|error}`, notification `{method,params}`. Default stdio JSONL; WS one record/text frame; Unix WebSocket requires HTTP Upgrade. Sources: https://developers.openai.com/codex/app-server and local help.
- **[help]** Transports `--listen stdio://|unix://|unix://PATH|ws://IP:PORT|off`; `--stdio` alias. Auth `--ws-auth capability-token|signed-bearer-token`, `--ws-token-file`, `--ws-token-sha256`, `--ws-shared-secret-file`, issuer/audience/clock-skew flags. `--code-mode-host` controls outbound model-tool host, not listener.
- **[docs]** Handshake exactly once: `initialize {clientInfo:{name,title?,version},capabilities?}`, then `initialized`. Pre-handshake requests fail `Not initialized`, repeated initialization `Already initialized`. `experimentalApi:true` unlocks gated fields/methods; `optOutNotificationMethods` exact-name suppression (no wildcard). Source: app-server reference.
- **[docs]** Turn control: `turn/start {threadId,input:[{type:"text",text}],...}`, `turn/steer {threadId,input,expectedTurnId,...}` and `turn/interrupt`; `thread/inject_items` adds raw Responses items without a turn. Interrupt acknowledges `{}` then turn completes `status:"interrupted"`. Source: same reference.
- **[help]** `codex queue --thread <UUID|exact name> --message TEXT` sends to existing session without terminal scraping. `codex agents` browses daemon sessions; `app-server daemon`, `proxy` manage/connect shared daemon.
- **[docs]** `command/exec`, `/write`, `/resize`, `/terminate` and base64 `outputDelta`; experimental `process/spawn`, `writeStdin`, `resizePty`, `kill`, output/exited. Full fs read/write/list/copy/remove/watch APIs, remote environment inspection and review/start exist. Source: same reference.
- **[help]** `app-server generate-ts --out ...` and `generate-json-schema --out ...` generate a version-matched contract without guessing. These were not executed in this survey.

## 13. Status from outside

- **[docs]** `thread/status/changed {threadId,status}` and thread/read/list return `notLoaded|idle|systemError|active`; activeFlags include `waitingOnApproval`. `turn/started/completed`, errors, approval/input requests, tool deltas add detail. Source: https://developers.openai.com/codex/app-server
- **[docs]** Last-unsubscribe unload is delayed until 30 minutes with no subscribers/activity; then status notLoaded and `thread/closed`. Running work and idle-but-loaded are different. Source: same reference.
- **[docs]** WS listener exposes GET `/readyz` once accepting, `/healthz` without Origin; Origin requests get 403. Queue overload JSON-RPC -32001 `Server overloaded; retry later.` calls for exponential-backoff+jitter. Source: same reference.

## 14. Usage, cost, and limits

- **[docs]** `thread/tokenUsage/updated`, `account/rateLimits/updated`, `account/rateLimits/read`, exec turn.completed usage expose tokens and account limits. `thread/goal/set/get/clear` tracks objective (1–4,000 characters), status, optional token budget and used tokens/time; new objective resets goal counters. Source: https://developers.openai.com/codex/app-server
- **[docs]** Config has context/auto-compaction and model-output settings; token counts are not guaranteed USD bill. Source: https://developers.openai.com/codex/config-reference
- Hard per-process dollar budget and aggregate cross-process limiter: unknown.

## 15. Authentication

- **[help]** `codex login status` is non-model login inspection; login/logout commands exist. Do not log out as a status check.
- **[docs]** ChatGPT OAuth and API-key login; stored credentials in `$CODEX_HOME/auth.json` or OS keyring depending `cli_auth_credentials_store`; environment/cloud auth paths also exist. App-server `account/read`, login/start/cancel, logout and auth notifications expose account state. Source: https://developers.openai.com/codex/auth and https://developers.openai.com/codex/app-server
- Local logged-in state cannot prove a remotely revoked credential remains valid without contacting service; exact no-network expiry validation: unknown.

## 16. Running many instances

- **[help]** Current CLI uses shared daemon unless `--no-daemon`; remote clients may share a single server. `CODEX_HOME`, separate IDs/cwd/worktrees, listener ports/socket paths are isolation controls.
- **[docs]** All threads in an app-server share chosen code-mode-host connection. Multiple clients can subscribe; bound queues require consuming events continuously. WS transport is experimental/unsupported for production; non-loopback listeners can be unauthenticated unless explicit WS auth configured. Source: https://developers.openai.com/codex/app-server
- RSS per worker, SQLite/NFS contention thresholds, same-thread multiple-server locking: unknown; no runtime benchmark.

## 17. Anything else a program driving this harness should know

- **[docs]** Public app-server API includes additional groups: config effective-read/write/batchWrite/requirements; external-agent migration detect/import/progress; skills and plugins; Windows sandbox setup; feedback upload; filesystem; auth; model/provider capability catalogs. A config read may resolve layers; a config write intentionally edits user files. Source: https://developers.openai.com/codex/app-server
- **[docs]** Persistent paginated thread history makes old tooling that only scans `sessions/**/*.jsonl` incomplete. Thread/list defaults hide exec/app-server sources; request sourceKinds deliberately. Source: same reference.
- **[verified]** Research saved public source snapshots under `sources/codex-*.txt` and URL mapping `sources/urls.json`; no credentials or user config were read.
- **[docs]** Additional streamed deltas: item/plan/delta (final plan may differ from concatenation), item/reasoning/summaryTextDelta (summaryIndex), summaryPartAdded, textDelta; commandExecution/outputDelta. item/fileChange/outputDelta deprecated/not emitted; use fileChange and turn/diff/updated. thread/compacted deprecated; use contextCompaction item. Errors include ContextWindowExceeded, UsageLimitExceeded, HttpConnectionFailed, ResponseStreamConnectionFailed, ResponseStreamDisconnected, ResponseTooManyFailedAttempts, BadRequest, Unauthorized, SandboxError, InternalServerError, Other; relevant variants include upstream httpStatusCode. Source: https://developers.openai.com/codex/app-server
