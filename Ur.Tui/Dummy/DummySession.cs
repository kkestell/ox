using System.Runtime.CompilerServices;

namespace Ur.Tui.Dummy;

public sealed class DummySession
{
    private static readonly string[] Responses =
    [
        "I can help you with that! Let me think about this for a moment.",
        "That's an interesting question. Here's what I know about the topic.",
        "Sure! Here's a quick example of how you could approach this problem.",
        "Great question. The key insight here is that the solution involves multiple steps.",
        "I'd recommend starting with a simple approach and iterating from there.",
    ];

    private static readonly string[] ToolNames = ["read_file", "search", "write_file", "run_command"];

    private int _responseIndex;

    public async IAsyncEnumerable<DummyAgentLoopEvent> RunTurnAsync(
        string input,
        [EnumeratorCancellation] CancellationToken ct = default)
    {
        var response = Responses[_responseIndex % Responses.Length];
        _responseIndex++;

        // Simulate tool call on every other turn
        if (_responseIndex % 2 == 0)
        {
            var toolName = ToolNames[_responseIndex / 2 % ToolNames.Length];
            var callId = Guid.NewGuid().ToString("N")[..8];

            yield return new DummyToolCallStarted { CallId = callId, ToolName = toolName };
            await Task.Delay(Random.Shared.Next(200, 500), ct);

            yield return new DummyToolCallCompleted
            {
                CallId = callId,
                ToolName = toolName,
                Result = $"Tool '{toolName}' completed successfully.",
                IsError = false,
            };
            await Task.Delay(Random.Shared.Next(50, 150), ct);
        }

        // Stream the response word by word
        var words = response.Split(' ');
        foreach (var word in words)
        {
            ct.ThrowIfCancellationRequested();
            var chunk = word + " ";
            yield return new DummyResponseChunk { Text = chunk };
            await Task.Delay(Random.Shared.Next(30, 80), ct);
        }

        // TurnCompleted is enqueued by the caller's finally block,
        // mirroring how the real integration wraps the stream.
    }
}
