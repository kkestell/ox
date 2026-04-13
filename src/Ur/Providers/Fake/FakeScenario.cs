using System.Text.Json;
using System.Text.Json.Serialization;

namespace Ur.Providers.Fake;

/// <summary>
/// A deterministic conversation scenario for the fake provider.
///
/// Each scenario is a sequence of turns. When the fake provider receives a
/// streaming request, it pops the next turn from the sequence and emits its
/// response chunks. If the scenario runs out of turns, the provider fails
/// fast with a clear error — this is a strict test harness, not a permissive
/// mock.
///
/// Scenarios can be embedded as built-in constants or loaded from JSON files
/// for regression reproduction.
/// </summary>
internal sealed class FakeScenario
{
    /// <summary>
    /// Human-readable name of this scenario, used in error messages and model IDs.
    /// </summary>
    public required string Name { get; init; }

    /// <summary>
    /// Ordered list of turns. Each turn describes one assistant response. The
    /// fake provider pops turns in order; it does not try to match on user input.
    /// </summary>
    public required IReadOnlyList<FakeScenarioTurn> Turns { get; init; }

    /// <summary>
    /// Simulated context window size in tokens. When set, the fake provider
    /// reports this value through <see cref="FakeProvider.GetContextWindow"/>
    /// so the compaction pipeline can test threshold behavior without a real
    /// model entry in providers.json.
    /// </summary>
    public int? ContextWindow { get; init; }
}

/// <summary>
/// One turn of a fake scenario — the assistant's response to whatever the
/// user/agent loop sends.
/// </summary>
internal sealed class FakeScenarioTurn
{
    /// <summary>
    /// Text chunks to stream as <c>ChatResponseUpdate</c> items. Each string
    /// becomes one streaming update. Use multiple chunks to exercise incremental
    /// rendering and soft-wrap behavior.
    /// </summary>
    public IReadOnlyList<string>? TextChunks { get; init; }

    /// <summary>
    /// Reasoning chunks to stream as <c>ChatResponseUpdate</c> items containing
    /// <see cref="Microsoft.Extensions.AI.TextReasoningContent"/>. Emitted before
    /// text chunks to mirror real provider behaviour (reasoning precedes response).
    /// </summary>
    public IReadOnlyList<string>? ReasoningChunks { get; init; }

    /// <summary>
    /// An optional tool call to emit. When set, the response includes a
    /// <c>FunctionCallContent</c> alongside or instead of text. The agent loop
    /// will attempt to execute this tool, and the next turn in the scenario
    /// should account for the tool result in the conversation.
    /// </summary>
    public FakeToolCall? ToolCall { get; init; }

    /// <summary>
    /// When true, the fake provider throws an exception for this turn instead
    /// of yielding any content. Used to test error handling paths.
    /// </summary>
    public bool SimulateError { get; init; }

    /// <summary>
    /// Error message to use when <see cref="SimulateError"/> is true.
    /// </summary>
    public string? ErrorMessage { get; init; }

    /// <summary>
    /// Simulated input token count reported in the usage chunk. Zero means
    /// no usage data is emitted.
    /// </summary>
    public int InputTokens { get; init; }

    /// <summary>
    /// Simulated output token count reported in the usage chunk.
    /// </summary>
    public int OutputTokens { get; init; }
}

/// <summary>
/// Describes a tool call the fake provider should emit.
/// </summary>
internal sealed class FakeToolCall
{
    /// <summary>
    /// The name of the tool to call (e.g. "read_file", "write_file").
    /// </summary>
    public required string Name { get; init; }

    /// <summary>
    /// JSON-serialized arguments for the tool call.
    /// </summary>
    public required string ArgumentsJson { get; init; }
}

/// <summary>
/// JSON file format for loading scenarios from disk. The file contains a single
/// scenario object.
/// </summary>
[JsonSerializable(typeof(FakeScenarioFile))]
[JsonSerializable(typeof(FakeScenarioTurnFile))]
[JsonSerializable(typeof(FakeToolCallFile))]
internal partial class FakeScenarioJsonContext : JsonSerializerContext;

/// <summary>
/// JSON-serializable representation of a scenario file.
/// </summary>
internal sealed class FakeScenarioFile
{
    [JsonPropertyName("name")]
    public string? Name { get; set; }

    [JsonPropertyName("turns")]
    public List<FakeScenarioTurnFile>? Turns { get; set; }

    [JsonPropertyName("context_window")]
    public int? ContextWindow { get; set; }
}

internal sealed class FakeScenarioTurnFile
{
    [JsonPropertyName("text_chunks")]
    public List<string>? TextChunks { get; set; }

    [JsonPropertyName("tool_call")]
    public FakeToolCallFile? ToolCall { get; set; }

    [JsonPropertyName("simulate_error")]
    public bool SimulateError { get; set; }

    [JsonPropertyName("error_message")]
    public string? ErrorMessage { get; set; }

    [JsonPropertyName("input_tokens")]
    public int InputTokens { get; set; }

    [JsonPropertyName("output_tokens")]
    public int OutputTokens { get; set; }
}

internal sealed class FakeToolCallFile
{
    [JsonPropertyName("name")]
    public string? Name { get; set; }

    [JsonPropertyName("arguments_json")]
    public string? ArgumentsJson { get; set; }
}

/// <summary>
/// Loads <see cref="FakeScenario"/> instances from built-in definitions or
/// JSON files on disk.
/// </summary>
internal static class FakeScenarioLoader
{
    /// <summary>
    /// Resolves a scenario by name-or-path. If the argument is a path to an
    /// existing JSON file, loads from disk. Otherwise, looks up built-in scenarios
    /// by name.
    /// </summary>
    public static FakeScenario Load(string nameOrPath)
    {
        // Try file-based scenario first.
        if (File.Exists(nameOrPath))
            return LoadFromFile(nameOrPath);

        // Look up built-in scenarios by name.
        return BuiltInScenarios.Get(nameOrPath)
            ?? throw new ArgumentException(
                $"Unknown fake scenario '{nameOrPath}'. " +
                $"Available built-ins: {string.Join(", ", BuiltInScenarios.Names)}");
    }

    private static FakeScenario LoadFromFile(string path)
    {
        var json = File.ReadAllText(path);
        var file = JsonSerializer.Deserialize(json, FakeScenarioJsonContext.Default.FakeScenarioFile)
            ?? throw new InvalidOperationException($"Failed to parse scenario file: {path}");

        return new FakeScenario
        {
            Name = file.Name ?? Path.GetFileNameWithoutExtension(path),
            ContextWindow = file.ContextWindow,
            Turns = (file.Turns ?? []).Select(t => new FakeScenarioTurn
            {
                TextChunks = t.TextChunks,
                ToolCall = t.ToolCall is { } tc ? new FakeToolCall
                {
                    Name = tc.Name ?? throw new InvalidOperationException("Tool call missing 'name'"),
                    ArgumentsJson = tc.ArgumentsJson ?? "{}"
                } : null,
                SimulateError = t.SimulateError,
                ErrorMessage = t.ErrorMessage,
                InputTokens = t.InputTokens,
                OutputTokens = t.OutputTokens,
            }).ToList()
        };
    }
}
