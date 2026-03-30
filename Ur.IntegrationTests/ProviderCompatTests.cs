using System.ClientModel;
using System.Text.Json;
using dotenv.net;
using GenerativeAI.Microsoft;
using Microsoft.Extensions.AI;
using OpenAI;
using Xunit.Abstractions;

namespace Ur.IntegrationTests;

/// <summary>
/// Captures raw ChatMessage output from each provider (with tool calls) and tests
/// cross-provider message feeding — both as-is and with AdditionalProperties stripped.
/// </summary>
public class ProviderCompatTests
{
    private readonly ITestOutputHelper _output;

    public ProviderCompatTests(ITestOutputHelper output)
    {
        _output = output;

        DotEnv.Load(options: new DotEnvOptions(probeForEnv: true, probeLevelsToSearch: 8));
    }

    // -- Provider client creation --------------------------------------------------

    private static readonly (string Name, string EnvVar, Func<string, IChatClient> Factory)[] Providers =
    [
        ("openai", "OPENAI_API_KEY", key =>
            new OpenAI.Chat.ChatClient("gpt-5-nano", new ApiKeyCredential(key))
                .AsIChatClient()),

        ("google", "GOOGLE_API_KEY", key =>
            new GenerativeAIChatClient(key, "gemini-3-flash-preview")),

        ("openrouter", "OPENROUTER_API_KEY", key =>
            new OpenAI.Chat.ChatClient(
                "qwen/qwen3.5-flash-02-23",
                new ApiKeyCredential(key),
                new OpenAIClientOptions { Endpoint = new Uri("https://openrouter.ai/api/v1") })
                .AsIChatClient()),
    ];

    private static Dictionary<string, IChatClient> CreateClients()
    {
        var clients = new Dictionary<string, IChatClient>();
        foreach (var (name, envVar, factory) in Providers)
        {
            var key = Environment.GetEnvironmentVariable(envVar);
            if (key is not null)
                clients[name] = factory(key);
        }
        return clients;
    }

    // -- Tool definition -----------------------------------------------------------

    private static readonly AIFunction WeatherTool = AIFunctionFactory.Create(
        (string city) => $"Sunny, 22°C in {city}",
        "get_weather",
        "Get the current weather for a city");

    // -- Conversation runner -------------------------------------------------------

    /// <summary>
    /// Runs a two-turn tool-calling conversation and returns the full message list.
    /// Turn 1: user asks about weather → model calls get_weather.
    /// Turn 2: tool result sent back → model gives final answer.
    /// </summary>
    private async Task<List<ChatMessage>> RunToolCallingConversation(IChatClient client)
    {
        var messages = new List<ChatMessage>
        {
            new(ChatRole.System, "You have access to tools. You MUST call tools when asked about weather — do not answer from your own knowledge."),
            new(ChatRole.User, "What's the weather in Tokyo?"),
        };

        var options = new ChatOptions
        {
            Tools = [WeatherTool],
            ToolMode = ChatToolMode.Auto,
        };

        // Turn 1: expect tool call
        var response = await client.GetResponseAsync(messages, options);
        var assistantMsg = response.Messages[^1];
        messages.Add(assistantMsg);

        var toolCalls = assistantMsg.Contents.OfType<FunctionCallContent>().ToList();
        if (toolCalls.Count > 0)
        {
            var toolMessage = new ChatMessage(ChatRole.Tool, []);
            foreach (var call in toolCalls)
                toolMessage.Contents.Add(new FunctionResultContent(call.CallId, "Sunny, 22°C in Tokyo"));
            messages.Add(toolMessage);

            // Turn 2: final text response
            var response2 = await client.GetResponseAsync(messages, options);
            messages.Add(response2.Messages[^1]);
        }

        return messages;
    }

    // -- Tests ---------------------------------------------------------------------

    [Fact]
    public async Task CaptureAllProviders()
    {
        var clients = CreateClients();
        Assert.NotEmpty(clients);

        var captured = 0;
        foreach (var (name, client) in clients)
        {
            _output.WriteLine($"\n=== {name} ===");

            try
            {
                var messages = await RunToolCallingConversation(client);

                LogMessages(messages);
                _output.WriteLine($"  → captured {messages.Count} messages");
                captured++;
            }
            catch (Exception ex)
            {
                _output.WriteLine($"  ERROR: {ex.Message}");
            }
        }

        Assert.True(captured >= 1, "At least one provider conversation captured");
    }

    [Fact]
    public async Task CrossProviderFeeding()
    {
        var clients = CreateClients();
        if (clients.Count < 2)
        {
            _output.WriteLine("Need ≥2 providers configured. Skipping.");
            return;
        }

        // Capture from each provider first
        var conversations = new Dictionary<string, List<ChatMessage>>();
        foreach (var (name, client) in clients)
        {
            _output.WriteLine($"Capturing {name}...");
            conversations[name] = await RunToolCallingConversation(client);
        }

        foreach (var (sourceName, sourceMessages) in conversations)
        {
            foreach (var (targetName, targetClient) in clients)
            {
                if (sourceName == targetName) continue;

                _output.WriteLine($"\n--- {sourceName} → {targetName} ---");

                // As-is
                try
                {
                    var resp = await targetClient.GetResponseAsync(sourceMessages);
                    var preview = resp.Text is { Length: > 100 } ? resp.Text[..100] + "..." : resp.Text;
                    _output.WriteLine($"  as-is:     OK — {preview}");
                }
                catch (Exception ex)
                {
                    _output.WriteLine($"  as-is:     ERROR — {ex.Message}");
                }

                // Stripped
                try
                {
                    var stripped = StripAdditionalProperties(sourceMessages);
                    var resp = await targetClient.GetResponseAsync(stripped);
                    var preview = resp.Text is { Length: > 100 } ? resp.Text[..100] + "..." : resp.Text;
                    _output.WriteLine($"  stripped:  OK — {preview}");
                }
                catch (Exception ex)
                {
                    _output.WriteLine($"  stripped:  ERROR — {ex.Message}");
                }

            }
        }
    }

    // -- Helpers -------------------------------------------------------------------

    private void LogMessages(List<ChatMessage> messages)
    {
        foreach (var msg in messages)
        {
            _output.WriteLine($"  [{msg.Role}]");
            foreach (var c in msg.Contents)
            {
                switch (c)
                {
                    case TextContent tc:
                        var preview = tc.Text.Length > 80 ? tc.Text[..80] + "..." : tc.Text;
                        _output.WriteLine($"    text: {preview}");
                        break;
                    case FunctionCallContent fcc:
                        _output.WriteLine($"    tool_call: {fcc.Name} callId={fcc.CallId}");
                        break;
                    case FunctionResultContent frc:
                        _output.WriteLine($"    tool_result: callId={frc.CallId}");
                        break;
                    default:
                        _output.WriteLine($"    {c.GetType().Name}");
                        break;
                }

                if (c.AdditionalProperties is { Count: > 0 } cp)
                    _output.WriteLine($"      content_props: {string.Join(", ", cp.Keys)}");
            }

            if (msg.AdditionalProperties is { Count: > 0 } mp)
                _output.WriteLine($"    msg_props: {string.Join(", ", mp.Keys)}");
        }
    }

    private static List<ChatMessage> StripAdditionalProperties(List<ChatMessage> messages)
    {
        var json = JsonSerializer.Serialize(messages, AIJsonUtilities.DefaultOptions);
        var cloned = JsonSerializer.Deserialize<List<ChatMessage>>(json, AIJsonUtilities.DefaultOptions)!;

        foreach (var msg in cloned)
        {
            msg.AdditionalProperties = null;
            foreach (var content in msg.Contents)
                content.AdditionalProperties = null;
        }

        return cloned;
    }

}
