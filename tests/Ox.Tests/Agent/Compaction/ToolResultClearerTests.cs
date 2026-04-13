using Microsoft.Extensions.AI;
using Ox.Agent.Compaction;

namespace Ox.Tests.Agent.Compaction;

/// <summary>
/// Tests for <see cref="ToolResultClearer"/>. The clearer is a pure projection —
/// it must never mutate the input list and must correctly identify turn boundaries
/// to decide which tool results to clear.
/// </summary>
public sealed class ToolResultClearerTests
{
    // ─── Helper: build a simple conversation with tool calls ──────────

    /// <summary>
    /// Builds a conversation with the given number of turns. Each turn is:
    /// user message → tool result message (with FunctionResultContent) → assistant message.
    /// This mirrors the real conversation shape from AgentLoop.
    /// </summary>
    private static List<ChatMessage> BuildConversation(int turnCount)
    {
        var messages = new List<ChatMessage>();
        for (var i = 1; i <= turnCount; i++)
        {
            messages.Add(new ChatMessage(ChatRole.User, $"Turn {i} input"));
            messages.Add(new ChatMessage(ChatRole.Tool,
                [new FunctionResultContent($"call-{i}", $"Tool result for turn {i}")]));
            messages.Add(new ChatMessage(ChatRole.Assistant, $"Turn {i} response"));
        }
        return messages;
    }

    // ─── Core clearing behavior ───────────────────────────────────────

    [Fact]
    public void FiveTurns_KeepThree_ClearsFirstTwoTurns()
    {
        var messages = BuildConversation(5);

        var result = ToolResultClearer.ClearOldToolResults(messages, turnsToKeep: 3).ToList();

        Assert.Equal(messages.Count, result.Count);

        // Turns 1 and 2 should have cleared tool results.
        AssertToolResultCleared(result, turnIndex: 1);
        AssertToolResultCleared(result, turnIndex: 2);

        // Turns 3, 4, 5 should have intact tool results.
        AssertToolResultIntact(result, turnIndex: 3, "Tool result for turn 3");
        AssertToolResultIntact(result, turnIndex: 4, "Tool result for turn 4");
        AssertToolResultIntact(result, turnIndex: 5, "Tool result for turn 5");
    }

    // ─── No tool results → pass through unchanged ────────────────────

    [Fact]
    public void NoToolResults_PassThroughUnchanged()
    {
        var messages = new List<ChatMessage>
        {
            new(ChatRole.User, "hello"),
            new(ChatRole.Assistant, "hi"),
            new(ChatRole.User, "how are you"),
            new(ChatRole.Assistant, "good"),
        };

        var result = ToolResultClearer.ClearOldToolResults(messages, turnsToKeep: 3).ToList();

        Assert.Equal(messages.Count, result.Count);
        for (var i = 0; i < messages.Count; i++)
        {
            Assert.Equal(messages[i].Role, result[i].Role);
            Assert.Equal(messages[i].Text, result[i].Text);
        }
    }

    // ─── Exactly N turns → nothing cleared (boundary condition) ──────

    [Fact]
    public void ExactlyNTurns_NothingCleared()
    {
        var messages = BuildConversation(3);

        var result = ToolResultClearer.ClearOldToolResults(messages, turnsToKeep: 3).ToList();

        // All 3 turns should have intact tool results — nothing is old enough to clear.
        AssertToolResultIntact(result, turnIndex: 1, "Tool result for turn 1");
        AssertToolResultIntact(result, turnIndex: 2, "Tool result for turn 2");
        AssertToolResultIntact(result, turnIndex: 3, "Tool result for turn 3");
    }

    // ─── Fewer turns than threshold → nothing cleared ────────────────

    [Fact]
    public void FewerTurnsThanThreshold_NothingCleared()
    {
        var messages = BuildConversation(2);

        var result = ToolResultClearer.ClearOldToolResults(messages, turnsToKeep: 5).ToList();

        AssertToolResultIntact(result, turnIndex: 1, "Tool result for turn 1");
        AssertToolResultIntact(result, turnIndex: 2, "Tool result for turn 2");
    }

    // ─── Projection safety: original list is not mutated ─────────────

    [Fact]
    public void OriginalListNotMutated()
    {
        var messages = BuildConversation(5);

        // Capture original tool result content before projection.
        var originalResults = messages
            .SelectMany(m => m.Contents.OfType<FunctionResultContent>())
            .Select(frc => frc.Result?.ToString())
            .ToList();

        _ = ToolResultClearer.ClearOldToolResults(messages, turnsToKeep: 2).ToList();

        // Verify the original messages still have their tool results intact.
        var afterResults = messages
            .SelectMany(m => m.Contents.OfType<FunctionResultContent>())
            .Select(frc => frc.Result?.ToString())
            .ToList();

        Assert.Equal(originalResults, afterResults);
    }

    // ─── Mixed messages: turns with and without tool calls ───────────

    [Fact]
    public void MixedTurns_OnlyToolResultsInOldTurnsCleared()
    {
        // Conversation: turn 1 has tool result, turn 2 is plain text, turn 3 has tool result.
        var messages = new List<ChatMessage>
        {
            new(ChatRole.User, "Turn 1"),
            new(ChatRole.Tool, [new FunctionResultContent("call-1", "old tool output")]),
            new(ChatRole.Assistant, "Turn 1 response"),
            new(ChatRole.User, "Turn 2"),
            new(ChatRole.Assistant, "Turn 2 response (no tools)"),
            new(ChatRole.User, "Turn 3"),
            new(ChatRole.Tool, [new FunctionResultContent("call-3", "recent tool output")]),
            new(ChatRole.Assistant, "Turn 3 response"),
        };

        // Keep 1 turn → only the most recent assistant turn's tool results are preserved.
        var result = ToolResultClearer.ClearOldToolResults(messages, turnsToKeep: 1).ToList();

        // Turn 1's tool result should be cleared (it's before the last 1 assistant turn).
        var turn1Tool = result[1].Contents.OfType<FunctionResultContent>().Single();
        Assert.Equal(ToolResultClearer.ClearedPlaceholder, turn1Tool.Result?.ToString());

        // Turn 3's tool result should be intact (it's within the last 1 assistant turn).
        var turn3Tool = result[6].Contents.OfType<FunctionResultContent>().Single();
        Assert.Equal("recent tool output", turn3Tool.Result?.ToString());
    }

    // ─── Empty messages list → empty result ──────────────────────────

    [Fact]
    public void EmptyMessages_ReturnsEmpty()
    {
        var result = ToolResultClearer.ClearOldToolResults([], turnsToKeep: 3).ToList();
        Assert.Empty(result);
    }

    // ─── Helpers ──────────────────────────────────────────────────────

    /// <summary>
    /// Asserts the tool result message for a given turn (1-based) has been cleared.
    /// In BuildConversation, each turn is 3 messages: user(0)+tool(1)+assistant(2).
    /// </summary>
    private static void AssertToolResultCleared(List<ChatMessage> result, int turnIndex)
    {
        var toolMsgIndex = (turnIndex - 1) * 3 + 1;
        var frc = result[toolMsgIndex].Contents.OfType<FunctionResultContent>().Single();
        Assert.Equal(ToolResultClearer.ClearedPlaceholder, frc.Result?.ToString());
    }

    /// <summary>
    /// Asserts the tool result message for a given turn is intact with the expected content.
    /// </summary>
    private static void AssertToolResultIntact(List<ChatMessage> result, int turnIndex, string expectedContent)
    {
        var toolMsgIndex = (turnIndex - 1) * 3 + 1;
        var frc = result[toolMsgIndex].Contents.OfType<FunctionResultContent>().Single();
        Assert.Equal(expectedContent, frc.Result?.ToString());
    }
}
