using System.ClientModel;
using dotenv.net;
using GeminiDotnet;
using GeminiDotnet.Extensions.AI;
using Microsoft.Extensions.AI;
using OllamaSharp;
using OpenAI;
using Xunit.Abstractions;

namespace Ur.IntegrationTests;

/// <summary>
/// Phase 1 experiments for the thinking-across-providers plan
/// (docs/agents/plans/2026-04-12-006-thinking-across-providers.md).
///
/// Each test probes a real provider to answer: does the MEAI adapter emit
/// <see cref="TextReasoningContent"/> natively for reasoning/thinking models,
/// or do we need a thin wrapper to extract and surface it?
///
/// Gated by <c>UR_RUN_THINKING_EXPERIMENTS=1</c> so they never run in CI.
/// Run them manually and record the "native TextReasoningContent ✓/✗" result
/// per provider to determine which wrappers Phase 2 needs.
///
/// Also probes interleaved narration (text before tool calls in one turn)
/// to confirm that path already works end-to-end before we change anything.
/// </summary>
public class ThinkingExperimentTests
{
    private const string RunExperimentsEnvVar = "UR_RUN_THINKING_EXPERIMENTS";

    private readonly ITestOutputHelper _output;

    public ThinkingExperimentTests(ITestOutputHelper output)
    {
        _output = output;
        DotEnv.Load(options: new DotEnvOptions(probeForEnv: true, probeLevelsToSearch: 8));
    }

    // ─── OpenAI o-series ────────────────────────────────────────────────────────

    /// <summary>
    /// Probes whether GPT-5.4 mini surfaces its reasoning as
    /// <see cref="TextReasoningContent"/> through the MEAI adapter.
    ///
    /// GPT-5.4 and its variants support a reasoning slider (none→xhigh) via
    /// <see cref="ChatOptions.Reasoning"/>. The <c>reasoning_effort</c> field
    /// controls how many internal reasoning tokens the model uses, but OpenAI
    /// does not return the reasoning trace to the caller — it stays server-side.
    ///
    /// We therefore expect NO <see cref="TextReasoningContent"/> in the stream.
    /// This test confirms that expectation and captures the baseline content-type
    /// profile for the MEAI OpenAI adapter with reasoning effort set.
    /// </summary>
    [Fact]
    public async Task OpenAI_GPT54Mini_ReasoningEffort_StreamingContentTypes()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunExperimentsEnvVar}=1 to run."); return; }

        var apiKey = Environment.GetEnvironmentVariable("OPENAI_API_KEY")!;

        // gpt-5.4-mini with low reasoning effort — fast, cheap, still exercises
        // the ReasoningOptions → reasoningEffort mapping in the MEAI adapter.
        var client = new OpenAI.Chat.ChatClient("gpt-5.4-mini", new ApiKeyCredential(apiKey))
            .AsIChatClient();

        var options = new ChatOptions
        {
            Reasoning = new ReasoningOptions { Effort = ReasoningEffort.Low }
        };

        _output.WriteLine("=== OpenAI gpt-5.4-mini (reasoning effort: low) ===");

        await ProbeThinkingAsync(client, options,
            "What is the 17th prime number? Reason through it carefully.");
    }

    // ─── DeepSeek-R1 via OpenRouter ─────────────────────────────────────────────

    /// <summary>
    /// Probes whether DeepSeek-R1 (via OpenRouter's OpenAI-compatible endpoint)
    /// surfaces its reasoning chain as <see cref="TextReasoningContent"/>.
    ///
    /// DeepSeek-R1 always returns a <c>reasoning_content</c> field in the streaming
    /// delta alongside the normal <c>content</c> field. The MEAI OpenAI adapter
    /// (v10.4.1) already reads this field and emits <see cref="TextReasoningContent"/>
    /// items. We expect to see them here with no extra wrapper code.
    ///
    /// No special <see cref="ChatOptions.Reasoning"/> configuration is needed —
    /// DeepSeek-R1 thinks by default.
    /// </summary>
    [Fact]
    public async Task DeepSeekR1_ViaOpenRouter_StreamingContentTypes()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunExperimentsEnvVar}=1 to run."); return; }

        var apiKey = Environment.GetEnvironmentVariable("OPENROUTER_API_KEY")!;

        var client = new OpenAI.Chat.ChatClient(
            "deepseek/deepseek-r1",
            new ApiKeyCredential(apiKey),
            new OpenAIClientOptions { Endpoint = new Uri("https://openrouter.ai/api/v1") })
            .AsIChatClient();

        // No reasoning options needed — DeepSeek-R1 always thinks.
        _output.WriteLine("=== DeepSeek-R1 via OpenRouter (no explicit reasoning options) ===");

        await ProbeThinkingAsync(client, options: null,
            "What is the 17th prime number? Reason through it carefully.");
    }

    // ─── Gemini with thinking mode ───────────────────────────────────────────────

    /// <summary>
    /// Probes whether Gemini 3 Flash (via GeminiDotnet) surfaces its thinking trace
    /// as <see cref="TextReasoningContent"/> when thinking is enabled.
    ///
    /// GeminiDotnet 0.23.0 maps Gemini "thought" parts (<c>part.Thought == true</c>)
    /// to <see cref="TextReasoningContent"/> in <c>GeminiToMEAIMapper</c>. Thinking
    /// is enabled by setting <see cref="ChatOptions.Reasoning"/> — the mapper
    /// translates this to a <c>ThinkingConfiguration</c> in the Gemini API request.
    ///
    /// We expect <see cref="TextReasoningContent"/> to appear before
    /// <see cref="TextContent"/> in the streaming response.
    /// </summary>
    [Fact]
    public async Task Gemini_ThinkingMode_StreamingContentTypes()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunExperimentsEnvVar}=1 to run."); return; }

        var apiKey = Environment.GetEnvironmentVariable("GOOGLE_API_KEY")!;

        var client = new GeminiChatClient(new GeminiClientOptions
        {
            ApiKey = apiKey,
            // gemini-3-flash-preview supports thinking mode at all effort levels.
            ModelId = "gemini-3-flash-preview"
        });

        // ReasoningEffort.Low maps to ThinkingConfigThinkingLevel.Low in GeminiDotnet,
        // which includes thoughts in the response (IncludeThoughts = true).
        var options = new ChatOptions
        {
            Reasoning = new ReasoningOptions
            {
                Effort = ReasoningEffort.Low,
                Output = ReasoningOutput.Full
            }
        };

        _output.WriteLine("=== Gemini 3 Flash (thinking mode: low effort, full output) ===");

        await ProbeThinkingAsync(client, options,
            "What is the 17th prime number? Reason through it carefully.");
    }

    // ─── Ollama with Qwen3 thinking mode ────────────────────────────────────────

    /// <summary>
    /// Probes whether Qwen3 running on Ollama surfaces its thinking trace as
    /// <see cref="TextReasoningContent"/> via OllamaSharp.
    ///
    /// Qwen3 supports a /think mode that emits thinking wrapped in &lt;think&gt;
    /// tags. Ollama v0.7+ exposes a <c>think</c> request option for models that
    /// support it. Whether OllamaSharp maps <see cref="ChatOptions.Reasoning"/>
    /// to this option, or whether the thinking arrives as raw &lt;think&gt; text
    /// in <see cref="TextContent"/> rather than as <see cref="TextReasoningContent"/>,
    /// is what this experiment determines.
    ///
    /// The test passes the <c>think=true</c> hint via
    /// <see cref="ChatOptions.AdditionalProperties"/> as a fallback if the
    /// <see cref="ChatOptions.Reasoning"/> path doesn't reach Ollama.
    /// </summary>
    [Fact]
    public async Task Ollama_Qwen3_ThinkingMode_StreamingContentTypes()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunExperimentsEnvVar}=1 to run."); return; }

        // The Ollama endpoint comes from the same providers.json that production uses.
        var ollamaEndpoint = new Uri("http://kyles-mac-mini.local:11434");
        var client = new OllamaApiClient(ollamaEndpoint, "qwen3:8b");

        // Try to enable thinking via the standard ReasoningOptions first.
        // Also pass "think=true" in AdditionalProperties as the Ollama-native hint
        // in case OllamaSharp doesn't map ReasoningOptions to the think option.
        var options = new ChatOptions
        {
            Reasoning = new ReasoningOptions { Effort = ReasoningEffort.Low },
            AdditionalProperties = new AdditionalPropertiesDictionary { ["think"] = true }
        };

        _output.WriteLine("=== Ollama qwen3:8b (thinking mode via ReasoningOptions + AdditionalProperties) ===");

        await ProbeThinkingAsync(client, options,
            "What is the 17th prime number? Reason through it carefully.");
    }

    // ─── OpenRouter: variety of reasoning-capable models ─────────────────────────
    //
    // We probe each model with two things:
    //   1. ChatOptions.Reasoning set, so the SDK sends reasoning_effort on the wire.
    //   2. A raw non-streaming curl-equivalent request so we can see the actual
    //      JSON field names OpenRouter uses in its response (it uses "reasoning",
    //      not "reasoning_content" — but the MEAI adapter only looks for the latter).

    /// <summary>
    /// Probes MiniMax M2.7 via OpenRouter.
    /// </summary>
    [Fact]
    public async Task OpenRouter_MiniMaxM27_StreamingContentTypes()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunExperimentsEnvVar}=1 to run."); return; }

        var client = BuildOpenRouterClient("minimax/minimax-m2.7");
        _output.WriteLine("=== OpenRouter: minimax/minimax-m2.7 ===");
        await ProbeThinkingAsync(client, ReasoningOptions(),
            "What is the 17th prime number? Reason through it carefully.");
        await ProbeRawAsync("minimax/minimax-m2.7");
    }

    /// <summary>
    /// Probes ByteDance Seed 2.0 Lite via OpenRouter.
    /// </summary>
    [Fact]
    public async Task OpenRouter_Seed20Lite_StreamingContentTypes()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunExperimentsEnvVar}=1 to run."); return; }

        var client = BuildOpenRouterClient("bytedance-seed/seed-2.0-lite");
        _output.WriteLine("=== OpenRouter: bytedance-seed/seed-2.0-lite ===");
        await ProbeThinkingAsync(client, ReasoningOptions(),
            "What is the 17th prime number? Reason through it carefully.");
        await ProbeRawAsync("bytedance-seed/seed-2.0-lite");
    }

    /// <summary>
    /// Probes ByteDance Seed 1.6 via OpenRouter.
    /// </summary>
    [Fact]
    public async Task OpenRouter_Seed16_StreamingContentTypes()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunExperimentsEnvVar}=1 to run."); return; }

        var client = BuildOpenRouterClient("bytedance-seed/seed-1.6");
        _output.WriteLine("=== OpenRouter: bytedance-seed/seed-1.6 ===");
        await ProbeThinkingAsync(client, ReasoningOptions(),
            "What is the 17th prime number? Reason through it carefully.");
        await ProbeRawAsync("bytedance-seed/seed-1.6");
    }

    /// <summary>
    /// Probes OpenAI GPT-5.1 Codex Mini via OpenRouter.
    /// </summary>
    [Fact]
    public async Task OpenRouter_GPT51CodexMini_StreamingContentTypes()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunExperimentsEnvVar}=1 to run."); return; }

        var client = BuildOpenRouterClient("openai/gpt-5.1-codex-mini");
        _output.WriteLine("=== OpenRouter: openai/gpt-5.1-codex-mini ===");
        await ProbeThinkingAsync(client, ReasoningOptions(),
            "What is the 17th prime number? Reason through it carefully.");
        await ProbeRawAsync("openai/gpt-5.1-codex-mini");
    }

    /// <summary>
    /// Probes OpenAI GPT-5 Mini via OpenRouter.
    /// </summary>
    [Fact]
    public async Task OpenRouter_GPT5Mini_StreamingContentTypes()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunExperimentsEnvVar}=1 to run."); return; }

        var client = BuildOpenRouterClient("openai/gpt-5-mini");
        _output.WriteLine("=== OpenRouter: openai/gpt-5-mini ===");
        await ProbeThinkingAsync(client, ReasoningOptions(),
            "What is the 17th prime number? Reason through it carefully.");
        await ProbeRawAsync("openai/gpt-5-mini");
    }

    /// <summary>
    /// Probes Qwen 3.5 122B-A10B via OpenRouter.
    /// </summary>
    [Fact]
    public async Task OpenRouter_Qwen35_122B_StreamingContentTypes()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunExperimentsEnvVar}=1 to run."); return; }

        var client = BuildOpenRouterClient("qwen/qwen3.5-122b-a10b");
        _output.WriteLine("=== OpenRouter: qwen/qwen3.5-122b-a10b ===");
        await ProbeThinkingAsync(client, ReasoningOptions(),
            "What is the 17th prime number? Reason through it carefully.");
        await ProbeRawAsync("qwen/qwen3.5-122b-a10b");
    }

    /// <summary>
    /// Probes Qwen 3.6 Plus via OpenRouter.
    /// </summary>
    [Fact]
    public async Task OpenRouter_Qwen36Plus_StreamingContentTypes()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunExperimentsEnvVar}=1 to run."); return; }

        var client = BuildOpenRouterClient("qwen/qwen3.6-plus");
        _output.WriteLine("=== OpenRouter: qwen/qwen3.6-plus ===");
        await ProbeThinkingAsync(client, ReasoningOptions(),
            "What is the 17th prime number? Reason through it carefully.");
        await ProbeRawAsync("qwen/qwen3.6-plus");
    }

    /// <summary>
    /// Probes DeepSeek V3.2 Speciale via OpenRouter.
    /// </summary>
    [Fact]
    public async Task OpenRouter_DeepSeekV32Speciale_StreamingContentTypes()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunExperimentsEnvVar}=1 to run."); return; }

        var client = BuildOpenRouterClient("deepseek/deepseek-v3.2-speciale");
        _output.WriteLine("=== OpenRouter: deepseek/deepseek-v3.2-speciale ===");
        await ProbeThinkingAsync(client, ReasoningOptions(),
            "What is the 17th prime number? Reason through it carefully.");
        await ProbeRawAsync("deepseek/deepseek-v3.2-speciale");
    }

    // ─── Interleaved narration probe ─────────────────────────────────────────────

    /// <summary>
    /// Probes interleaved narration: text the model emits between its decision to
    /// call a tool and the actual function call in a single response turn.
    ///
    /// In AgentLoop, <see cref="AgentLoop.ResponseChunk"/> events accumulate
    /// assistant text and <see cref="AgentLoop.ToolCallStarted"/> events mark tool
    /// invocations. The question is whether providers actually stream narration
    /// text before the function call in a single streamed response, or whether all
    /// text and all function calls arrive in separate chunks.
    ///
    /// This test streams raw <see cref="ChatResponseUpdate"/> items and inspects
    /// the ordering of <see cref="TextContent"/> vs <see cref="FunctionCallContent"/>
    /// across the stream — not in the final aggregated message.
    ///
    /// Uses Google Gemini because it reliably narrates before tool calls.
    /// </summary>
    [Fact]
    public async Task Gemini_NarratesBeforeToolCall_InterleavedInStream()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunExperimentsEnvVar}=1 to run."); return; }

        var apiKey = Environment.GetEnvironmentVariable("GOOGLE_API_KEY")!;

        var client = new GeminiChatClient(new GeminiClientOptions
        {
            ApiKey = apiKey,
            ModelId = "gemini-3-flash-preview"
        });

        var weatherTool = AIFunctionFactory.Create(
            (string city) => $"Sunny, 22°C in {city}",
            "get_weather",
            "Get the current weather for a city");

        var messages = new List<ChatMessage>
        {
            new(ChatRole.User, "Before you call any tool, briefly say what you're about to do. Then check the weather in Tokyo.")
        };

        var options = new ChatOptions
        {
            Tools = [weatherTool],
            ToolMode = ChatToolMode.Auto
        };

        _output.WriteLine("=== Gemini 3 Flash — narration before tool call ===");
        _output.WriteLine("Streaming raw update sequence:");

        // Track ordering across all streaming updates to check if text arrives
        // before function calls in the same response turn.
        var sawText = false;
        var sawFunctionCall = false;
        var textBeforeCall = false;

        await foreach (var update in client.GetStreamingResponseAsync(messages, options))
        {
            foreach (var content in update.Contents)
            {
                var label = content.GetType().Name;

                if (content is TextContent tc && !string.IsNullOrWhiteSpace(tc.Text))
                {
                    var preview = tc.Text.Length > 60 ? tc.Text[..60] + "..." : tc.Text;
                    _output.WriteLine($"  [update] TextContent: \"{preview}\"");
                    sawText = true;
                }
                else if (content is FunctionCallContent fcc)
                {
                    _output.WriteLine($"  [update] FunctionCallContent: {fcc.Name}()");
                    sawFunctionCall = true;
                    // If we saw text earlier in this same streaming session and now
                    // see a function call, text arrived interleaved before the call.
                    if (sawText)
                        textBeforeCall = true;
                }
                else if (content is not UsageContent)
                {
                    _output.WriteLine($"  [update] {label}");
                }
            }
        }

        _output.WriteLine($"\nSaw text: {sawText}");
        _output.WriteLine($"Saw function call: {sawFunctionCall}");
        _output.WriteLine($"Text arrived before function call: {textBeforeCall}");

        // We expect Gemini to both narrate AND call the tool.
        Assert.True(sawFunctionCall, "Expected a tool call in this response");
        // The narration-before-tool-call result is informational — log it but
        // don't assert it, because the model may choose not to narrate.
        if (!textBeforeCall)
            _output.WriteLine("NOTE: no narration before tool call — model went straight to the call.");
    }

    // ─── Core probe helper ────────────────────────────────────────────────────────

    /// <summary>
    /// Streams a thinking-capable prompt through <paramref name="client"/> and logs
    /// every content type in the streaming response, accumulating token counts and
    /// tallying <see cref="TextReasoningContent"/> occurrences.
    ///
    /// The logged output is the experiment result: "native TextReasoningContent ✓"
    /// means Phase 2 needs no wrapper for this provider; "✗" means it does.
    /// </summary>
    private async Task ProbeThinkingAsync(
        IChatClient client,
        ChatOptions? options,
        string prompt)
    {
        var messages = new List<ChatMessage>
        {
            new(ChatRole.User, prompt)
        };

        // Tally each content type as updates arrive.
        var counts = new Dictionary<string, int>(StringComparer.Ordinal);
        var reasoningChars = 0;
        var textChars = 0;

        await foreach (var update in client.GetStreamingResponseAsync(messages, options))
        {
            foreach (var content in update.Contents)
            {
                var typeName = content.GetType().Name;
                counts[typeName] = counts.GetValueOrDefault(typeName) + 1;

                switch (content)
                {
                    case TextReasoningContent trc:
                        reasoningChars += trc.Text?.Length ?? 0;
                        break;

                    case TextContent tc:
                        textChars += tc.Text?.Length ?? 0;
                        break;
                }
            }
        }

        // Log the content-type summary — this is the experiment result.
        _output.WriteLine("Content types seen in streaming response:");
        foreach (var (type, count) in counts.OrderByDescending(kv => kv.Value))
            _output.WriteLine($"  {type}: {count} update(s)");

        var hasNativeReasoning = counts.ContainsKey(nameof(TextReasoningContent));
        _output.WriteLine(hasNativeReasoning
            ? $"  → native TextReasoningContent ✓  ({reasoningChars:N0} reasoning chars)"
            : $"  → native TextReasoningContent ✗  (no reasoning content in stream)");
        _output.WriteLine($"  → TextContent chars: {textChars:N0}");

        // At minimum we need some text or reasoning output — if we got neither,
        // the API call likely failed silently or the model returned nothing useful.
        Assert.True(textChars > 0 || reasoningChars > 0,
            "Expected at least some text or reasoning content in the response.");
    }

    // ─── Helpers ─────────────────────────────────────────────────────────────────

    private static IChatClient BuildOpenRouterClient(string model)
    {
        var apiKey = Environment.GetEnvironmentVariable("OPENROUTER_API_KEY")!;
        return new OpenAI.Chat.ChatClient(
            model,
            new ApiKeyCredential(apiKey),
            new OpenAIClientOptions { Endpoint = new Uri("https://openrouter.ai/api/v1") })
            .AsIChatClient();
    }

    // Standard reasoning options used for all OpenRouter probes.
    private static ChatOptions ReasoningOptions() => new()
    {
        Reasoning = new ReasoningOptions { Effort = ReasoningEffort.Low }
    };

    /// <summary>
    /// Makes a raw non-streaming HTTP request to OpenRouter with reasoning_effort
    /// and logs the exact reasoning-related fields present in the response JSON.
    ///
    /// This bypasses MEAI entirely so we can see what OpenRouter actually returns
    /// (e.g. "reasoning" vs "reasoning_content") independent of what the adapter
    /// knows how to parse.
    /// </summary>
    private async Task ProbeRawAsync(string model)
    {
        var apiKey = Environment.GetEnvironmentVariable("OPENROUTER_API_KEY")!;
        using var http = new System.Net.Http.HttpClient { Timeout = TimeSpan.FromSeconds(60) };
        http.DefaultRequestHeaders.Authorization =
            new System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", apiKey);

        var body = System.Text.Json.JsonSerializer.Serialize(new
        {
            model,
            stream = false,
            reasoning_effort = "low",
            messages = new[] { new { role = "user", content = "17th prime?" } }
        });

        var resp = await http.PostAsync(
            "https://openrouter.ai/api/v1/chat/completions",
            new System.Net.Http.StringContent(body, System.Text.Encoding.UTF8, "application/json"));

        var json = await resp.Content.ReadAsStringAsync();
        using var doc = System.Text.Json.JsonDocument.Parse(json);

        // Report every reasoning-related key present anywhere in the message object.
        var message = doc.RootElement
            .GetProperty("choices")[0]
            .GetProperty("message");

        _output.WriteLine($"  [raw] message fields: {string.Join(", ", message.EnumerateObject().Select(p => p.Name))}");

        foreach (var prop in message.EnumerateObject())
        {
            if (prop.Name.Contains("reason", StringComparison.OrdinalIgnoreCase))
            {
                var preview = prop.Value.ToString();
                if (preview.Length > 120) preview = preview[..120] + "...";
                _output.WriteLine($"  [raw] {prop.Name}: {preview}");
            }
        }
    }

    private static bool ShouldRun() =>
        string.Equals(
            Environment.GetEnvironmentVariable(RunExperimentsEnvVar),
            "1",
            StringComparison.Ordinal);
}
