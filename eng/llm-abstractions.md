# LLM abstractions across the research set

This summarizes the `## LLM abstraction and integration` sections of all fourteen reports in `research/` (pinned revisions, researched 2026-09-19). The ACP-side findings live in [acp-architecture-review.md](acp-architecture-review.md); this doc is about how each project reaches its model.

## The convergent seam

Every in-repo model path reduces to the same small surface, regardless of who owns the code around it:

```text
resolve(model, provider, credentials)
  → one streaming call(system_prompt, messages, tool_schemas)
    → ordered event stream(text deltas, thinking deltas, tool-call items, usage)
```

The whole abstraction is that one method plus neutral message and tool types. Nothing else is shared: history projection, tool execution, credentials, retries, and model catalogs all stay outside the seam, in code each project writes itself.

| Project | Seam | Provider transport | Model loop | Streaming |
| --- | --- | --- | --- | --- |
| goose | `Provider::stream` in `goose-provider-types` | Raw HTTP/SSE via `reqwest`, no SDKs | In repo | Yes |
| Docker Agent | `Provider.CreateChatCompletionStream` (Go interface) | Official SDKs (anthropic-sdk-go, openai-go, google genai, bedrock) + hand-written Docker Model Runner client | In repo | Yes |
| fast-agent | `FastAgentLLM` base class with two abstract seams | Vendor SDKs called directly (`AsyncAnthropic`, `AsyncOpenAI`, `google.genai`) | In repo | Yes |
| Gemini CLI | `ContentGenerator` interface (single vendor) | `@google/genai` SDK + hand-written Code Assist HTTP/SSE client | In repo (loop; inference remote) | Yes |
| kimi-cli | `kosong` `ChatProvider` protocol (in-repo package, also pinned on PyPI) | Official SDKs wrapped inside kosong | In repo (CLI calls `kosong.step`) | Yes |
| VTCode | `LLMProvider` trait in `vtcode-llm` | Raw HTTP/SSE via `reqwest` per adapter | In repo | Yes (skipped when tools enabled) |
| opencode | Vercel AI SDK `streamText` (default); own `@opencode-ai/llm` package (experimental native path) | AI SDK provider packages | In repo (`runLoop`) | Yes |
| octomind | `octolib` crate behind a thin in-repo adapter | Inside octolib, not visible | In repo | No — single non-streaming call |
| stakpak/agent | `stakai` crate (vendored in-workspace): `Provider` trait + registry + `Inference` client | In-repo per-provider adapters with SSE parsers | In repo | Yes |
| Ante | `ante-llm` crate (profiles, effort ladder only); transport withheld | Closed daemon | Closed | Unknown (deltas exist on the wire) |
| claude-agent-acp | None — `query()` from `@anthropic-ai/claude-agent-sdk` | CLI subprocess | Outside (Claude Code CLI) | Yes (`stream_event` messages) |
| agentclientprotocol/codex-acp | None — JSON-RPC to `codex app-server` child | Outside (Codex) | Outside | Yes (`item/*` deltas) |
| Zed codex-acp | None — two-method `CodexThreadImpl` trait (`submit`, `next_event`) over linked codex-core | Outside (codex-core) | Outside | Yes (codex `EventMsg`s) |
| OpenHands | None — LiteLLM server-side in the agent-server; ACP CLIs own their loops | Outside | Outside | Yes (event frames) |

## Who owns the abstraction

Three families, in descending order of frequency:

**1. Bespoke internal trait (seven of fourteen).** goose, Docker Agent, fast-agent, Gemini CLI, kimi-cli/kosong, VTCode, and Ante's public half all define their own interface and call SDKs or HTTP below it. The traits are strikingly uniform: a single streaming completion method, neutral message and tool types, per-provider adapters in one directory, and shared wire-format helpers (goose's `formats/openai.rs`, Docker's per-SDK adapters, fast-agent's converter classes, kosong's contrib providers).

**2. Third-party library behind a thin adapter (three).** opencode (Vercel AI SDK), octomind (octolib), and stakpak (stakai) all treat the library as a replaceable provider layer and keep an in-repo adapter module that converts to and from it. stakai is the hybrid case: a third-party-shaped crate vendored into the same workspace. opencode is the only project with two parallel abstractions — the AI SDK on the default path and its own Effect-Schema-based package behind an experimental flag — and it normalizes both onto one internal `LLMEvent` vocabulary so downstream code cannot tell them apart.

**3. Wrapped runtime, no own abstraction (four, plus sub-cases).** claude-agent-acp, both codex-acp adapters, and OpenHands build no provider requests at all; their model-facing surface is a subprocess protocol or a two-method event trait. The same pattern recurs *inside* bespoke systems: goose ships `ClaudeCodeProvider` and an `AcpProvider` that wrap external agents as providers, VTCode's Copilot provider spawns the `copilot` CLI, and Ante splits a published profile crate from a closed transport. Wrapping is a provider option, not just a topology.

## Recurring choices

- **Provider-specific code is quarantined.** In every in-repo design there is exactly one directory of per-provider adapters, and the neutral middle types (`chat.Message`, `PromptMessageExtended`, `LLMRequest`, `stakai::Message`, `goose::Message`) never leak provider quirks. Provider-specific behavior concentrates in three places: message conversion, request options (thinking/effort/reasoning params, cache headers), and schema serialization.
- **Thinking/effort normalization is a first-class concern.** Ante's `ante-llm` builds an entire published crate around per-family thinking dialects and effort ladders; codex exposes `reasoning_effort` as a session config option; opencode passes reasoning parameters through AI SDK middleware; kimi wraps providers with generation overrides. Projects that multi-provider support seriously all hit this.
- **Tool schemas flow MCP → neutral type → provider wire; execution never crosses the seam.** stakai, kosong, goose, and Docker all carry schemas in and tool-call deltas out only. The tool loop belongs to the agent runtime, which is what makes the abstraction swappable.
- **Credentials are a separate layer.** Resolution chains (fast-agent's `ProviderKeyManager`, VTCode's source-precedence resolver, Docker's `environment.Provider` chain, octomind's env-only rule) never touch the provider trait; auth is applied at request construction.
- **Model metadata comes from catalogs, not code.** models.dev feeds opencode, stakpak, and Docker's generated snapshot; Ante uses a user-editable catalog file; fast-agent has its own `ModelDatabase`. Selection is a `provider:model` string parsed at the edge everywhere it exists.
- **Streaming is the default; the exception is visible.** Octomind awaits the whole response and forwards complete messages, and its own report notes ACP clients get bursty output with no intra-response progress. Every project that streams per token gets the better ACP experience for free.
- **One product can mix families.** goose is bespoke with wrapped-CLI providers; opencode is third-party with a bespoke second path; VTCode is bespoke with one wrapped provider. The families describe seams, not whole architectures.

## Where the evidence runs out

- Wrapped runtimes hide the model path entirely: adapters see only events, so commit timing, retries, and context construction are unverifiable (both codex adapters, claude-agent-acp, OpenHands, Ante's daemon, VTCode's Copilot).
- Registry dependencies (octolib) and non-vendored SDKs (`@google/genai`, AI SDK packages) leave transport internals unobservable; the reports trace only up to the call site.
- Peripheral dependencies can masquerade as the abstraction: VTCode depends on `rig-core` for wire types and stream deserialization, but every `generate`/`stream` body issues its own `reqwest` call — rig never drives a completion.
- Gemini CLI is single-vendor by design; its "providers" are auth backends, and adding a vendor means a new `ContentGenerator` implementation.

## Implications for Ox

Ox uses Rig for its OpenRouter-backed agent (`src/agent.rs`). The research reads as follows on that choice:

1. **Rig is a legitimate but minority form** — a third-party abstraction behind an in-repo adapter. stakai, octolib, and the Vercel AI SDK are the observed peers; no project in the set uses Rig as its main loop (VTCode's rig use is peripheral types only). The choice itself is not the risk; the risk is letting the library's types spread past the adapter boundary.
2. **The seam to protect is small.** One streaming call, neutral message and tool types, events out. Every project that stayed there could swap providers; every project that leaked provider quirks into the runtime (per-family thinking branches, wire-format rewrites in the ACP layer — stakpak's XML-tag filtering is the cautionary case) paid for it repeatedly.
3. **Ox owns the parts no library supplies:** history → provider message conversion, tool schema serialization, usage normalization, effort/thinking parameters, cancellation wired into the stream, and retry policy. These are exactly the seams all fourteen implement themselves, and they are where provider-specific code should stay quarantined.
4. **Keep the loop in-process and visible.** The wrapped-runtime projects cannot answer when their model context commits or how retries behave; Ox's direct Rig calls keep the commit point observable, which the ACP review identifies as the property the strongest designs share.
5. **Keep token streaming.** Octomind's non-streaming shortcut is the one observed UX regression traced to the LLM layer rather than the session layer.
6. **Effort/thinking normalization is where multi-provider growth will land.** Ante's profile table (per-family dialect, system-role policy, search policy) is the most developed answer in the set if Ox ever needs a second wire style beyond OpenRouter.
