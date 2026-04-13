using System.Runtime.CompilerServices;
using System.Text.Json;
using Microsoft.Extensions.AI;

namespace Ur.Providers.Fake;

/// <summary>
/// A deterministic <see cref="IChatClient"/> that replays turns from a
/// <see cref="FakeScenario"/>.
///
/// Each call to <see cref="GetStreamingResponseAsync"/> pops the next turn
/// from the scenario and emits its response chunks. This is a strict test
/// harness — if the scenario runs out of turns, the client throws with a
/// clear error instead of silently returning empty results.
///
/// Thread-safety: the turn index is advanced with <see cref="Interlocked.Increment(ref int)"/>
/// so concurrent calls from the agent loop (e.g. parent + subagent) get
/// distinct turns in order.
/// </summary>
internal sealed class FakeChatClient : IChatClient
{
    private readonly FakeScenario _scenario;
    private readonly SharedTurnCounter? _sharedCounter;
    private int _turnIndex = -1;

    public FakeChatClient(FakeScenario scenario, SharedTurnCounter? sharedCounter = null)
    {
        _scenario = scenario;
        _sharedCounter = sharedCounter;
    }

    public Task<ChatResponse> GetResponseAsync(
        IEnumerable<ChatMessage> messages,
        ChatOptions? options = null,
        CancellationToken cancellationToken = default)
    {
        // The agent loop always uses streaming, but implement this for completeness.
        var turn = NextTurn();

        if (turn.SimulateError)
            throw new InvalidOperationException(
                turn.ErrorMessage ?? "Simulated fake provider error");

        var contents = new List<AIContent>();

        if (turn.TextChunks is { Count: > 0 })
            contents.Add(new TextContent(string.Concat(turn.TextChunks)));

        if (turn.ToolCall is { } tc)
            contents.Add(BuildFunctionCall(tc));

        var message = new ChatMessage(ChatRole.Assistant, contents);
        return Task.FromResult(new ChatResponse(message));
    }

    public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
        IEnumerable<ChatMessage> messages,
        ChatOptions? options = null,
        [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        var turn = NextTurn();

        if (turn.SimulateError)
            throw new InvalidOperationException(
                turn.ErrorMessage ?? "Simulated fake provider error");

        // Emit reasoning chunks before text to mirror real provider ordering
        // (reasoning traces precede the response in models that support thinking).
        if (turn.ReasoningChunks is { Count: > 0 })
        {
            foreach (var chunk in turn.ReasoningChunks)
            {
                cancellationToken.ThrowIfCancellationRequested();
                yield return new ChatResponseUpdate
                {
                    Role = ChatRole.Assistant,
                    Contents = [new TextReasoningContent(chunk)]
                };
            }
        }

        // Emit text chunks as individual streaming updates, simulating
        // token-by-token streaming from a real provider.
        if (turn.TextChunks is { Count: > 0 })
        {
            foreach (var chunk in turn.TextChunks)
            {
                cancellationToken.ThrowIfCancellationRequested();
                yield return new ChatResponseUpdate
                {
                    Role = ChatRole.Assistant,
                    Contents = [new TextContent(chunk)]
                };
            }
        }

        // Emit tool call as a single update with a FunctionCallContent.
        if (turn.ToolCall is { } tc)
        {
            cancellationToken.ThrowIfCancellationRequested();
            yield return new ChatResponseUpdate
            {
                Role = ChatRole.Assistant,
                Contents = [BuildFunctionCall(tc)]
            };
        }

        // Emit usage data if the scenario specifies token counts.
        if (turn.InputTokens > 0 || turn.OutputTokens > 0)
        {
            yield return new ChatResponseUpdate
            {
                Contents =
                [
                    new UsageContent(new UsageDetails
                    {
                        InputTokenCount = turn.InputTokens,
                        OutputTokenCount = turn.OutputTokens,
                    })
                ]
            };
        }

        await Task.CompletedTask;
    }

    public object? GetService(Type serviceType, object? serviceKey = null) => null;

    public void Dispose() { }

    /// <summary>
    /// Advances to the next turn in the scenario. Fails fast if no more turns
    /// are available — the scenario is strict, not permissive.
    /// </summary>
    private FakeScenarioTurn NextTurn()
    {
        // Use the shared counter when available (scenarios that need turn
        // coordination across client instances, e.g. compaction). Otherwise
        // fall back to the per-client index.
        var index = _sharedCounter is not null
            ? _sharedCounter.Next()
            : Interlocked.Increment(ref _turnIndex);

        if (index >= _scenario.Turns.Count)
            throw new InvalidOperationException(
                $"Fake scenario '{_scenario.Name}' has {_scenario.Turns.Count} turn(s), " +
                $"but turn {index + 1} was requested. " +
                "Add more turns to the scenario or check that the test is not making unexpected calls.");

        return _scenario.Turns[index];
    }

    /// <summary>
    /// Builds a <see cref="FunctionCallContent"/> from the scenario's tool call spec.
    /// Uses a stable call ID so tests can assert on it.
    /// </summary>
    private static FunctionCallContent BuildFunctionCall(FakeToolCall tc)
    {
        // Parse the JSON arguments into a dictionary for FunctionCallContent.
        var args = string.IsNullOrWhiteSpace(tc.ArgumentsJson)
            ? new Dictionary<string, object?>()
            : JsonSerializer.Deserialize<Dictionary<string, object?>>(tc.ArgumentsJson)
              ?? new Dictionary<string, object?>();

        return new FunctionCallContent($"fake-call-{tc.Name}", tc.Name, args);
    }
}
