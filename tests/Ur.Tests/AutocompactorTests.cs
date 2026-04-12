using Microsoft.Extensions.AI;
using Microsoft.Extensions.Logging.Abstractions;
using Ur.Compaction;

namespace Ur.Tests;

/// <summary>
/// Tests for <see cref="Autocompactor"/>. The autocompactor orchestrates the
/// summarization step — it must respect the threshold, produce a properly-tagged
/// summary message, and preserve recent messages.
/// </summary>
public sealed class AutocompactorTests
{
    private static readonly IReadOnlyList<ChatMessage> EmptyChatResponse =
        [new ChatMessage(ChatRole.Assistant, "Summary of the conversation.")];

    // ─── Threshold behavior ───────────────────────────────────────────

    [Fact]
    public async Task HighFill_CompactionOccurs()
    {
        // 85% fill > 60% threshold → compaction should fire.
        var messages = BuildLongConversation(10);
        var client = new FakeSummarizingClient("This is the summary.");
        var logger = NullLogger.Instance;

        var compacted = await Autocompactor.TryCompactAsync(
            messages, client, contextWindow: 100_000, lastInputTokens: 85_000,
            logger, CancellationToken.None);

        Assert.True(compacted);

        // The first message should be the summary, wrapped in context-summary tags.
        Assert.StartsWith(Autocompactor.SummaryOpenTag, messages[0].Text!);
        Assert.Contains("This is the summary.", messages[0].Text!);
        Assert.EndsWith(Autocompactor.SummaryCloseTag, messages[0].Text!);

        // Stored role stays User — the System projection happens in BuildLlmMessages,
        // not in the compactor. BuildLlmMessages is private; that projection is validated
        // manually via boo (see plan validation section).
        Assert.Equal(ChatRole.User, messages[0].Role);

        // Total message count should be smaller than before (summary + preserved tail).
        Assert.True(messages.Count < 30); // 10 turns = 30 messages originally
    }

    [Fact]
    public async Task LowFill_NoCompaction()
    {
        // 50% fill <= 60% threshold → no compaction.
        var messages = BuildLongConversation(10);
        var originalCount = messages.Count;
        var client = new FakeSummarizingClient("Should not be called.");
        var logger = NullLogger.Instance;

        var compacted = await Autocompactor.TryCompactAsync(
            messages, client, contextWindow: 100_000, lastInputTokens: 50_000,
            logger, CancellationToken.None);

        Assert.False(compacted);
        Assert.Equal(originalCount, messages.Count);
    }

    [Fact]
    public async Task ExactThreshold_NoCompaction()
    {
        // Exactly at 60% → should NOT compact (threshold is strictly greater-than).
        var messages = BuildLongConversation(10);
        var originalCount = messages.Count;
        var client = new FakeSummarizingClient("Should not be called.");
        var logger = NullLogger.Instance;

        var compacted = await Autocompactor.TryCompactAsync(
            messages, client, contextWindow: 100_000, lastInputTokens: 60_000,
            logger, CancellationToken.None);

        Assert.False(compacted);
        Assert.Equal(originalCount, messages.Count);
    }

    [Fact]
    public async Task TooFewMessages_NoCompaction()
    {
        // Very short conversation — not enough messages to compact meaningfully.
        var messages = new List<ChatMessage>
        {
            new(ChatRole.User, "hello"),
            new(ChatRole.Assistant, "hi")
        };
        var client = new FakeSummarizingClient("Should not be called.");
        var logger = NullLogger.Instance;

        var compacted = await Autocompactor.TryCompactAsync(
            messages, client, contextWindow: 1_000, lastInputTokens: 900,
            logger, CancellationToken.None);

        Assert.False(compacted);
        Assert.Equal(2, messages.Count);
    }

    [Fact]
    public async Task EmptySummary_NoCompaction()
    {
        // LLM returns empty summary → compaction should abort.
        var messages = BuildLongConversation(10);
        var originalCount = messages.Count;
        var client = new FakeSummarizingClient("   "); // whitespace only
        var logger = NullLogger.Instance;

        var compacted = await Autocompactor.TryCompactAsync(
            messages, client, contextWindow: 100_000, lastInputTokens: 85_000,
            logger, CancellationToken.None);

        Assert.False(compacted);
        Assert.Equal(originalCount, messages.Count);
    }

    [Fact]
    public async Task PreservedTail_ContainsRecentMessages()
    {
        // After compaction, the most recent messages should be preserved.
        var messages = BuildLongConversation(10);
        var lastUserMsg = messages.Last(m => m.Role == ChatRole.User);
        var lastAssistantMsg = messages.Last(m => m.Role == ChatRole.Assistant);
        var client = new FakeSummarizingClient("Conversation summary.");
        var logger = NullLogger.Instance;

        await Autocompactor.TryCompactAsync(
            messages, client, contextWindow: 100_000, lastInputTokens: 85_000,
            logger, CancellationToken.None);

        // The last user and assistant messages should still be present.
        Assert.Contains(messages, m => m.Text == lastUserMsg.Text);
        Assert.Contains(messages, m => m.Text == lastAssistantMsg.Text);
    }

    // ─── IsCompactionSummary detection ──────────────────────────────

    [Fact]
    public void IsCompactionSummary_TaggedMessage_ReturnsTrue()
    {
        var msg = new ChatMessage(ChatRole.User,
            $"{Autocompactor.SummaryOpenTag}\nSome summary.\n{Autocompactor.SummaryCloseTag}");

        Assert.True(Autocompactor.IsCompactionSummary(msg));
    }

    [Fact]
    public void IsCompactionSummary_PlainUserMessage_ReturnsFalse()
    {
        var msg = new ChatMessage(ChatRole.User, "Hello, how are you?");

        Assert.False(Autocompactor.IsCompactionSummary(msg));
    }

    [Fact]
    public void IsCompactionSummary_NoTextContent_ReturnsFalse()
    {
        // A tool result message has no TextContent at all.
        var msg = new ChatMessage(ChatRole.Tool,
            [new FunctionResultContent("call-1", "some result")]);

        Assert.False(Autocompactor.IsCompactionSummary(msg));
    }

    [Fact]
    public void IsCompactionSummary_TagInLaterTextContent_ReturnsTrue()
    {
        // If the tag appears in any TextContent item (not just the first), detect it.
        var msg = new ChatMessage(ChatRole.User,
            [new TextContent("preamble"), new TextContent($"{Autocompactor.SummaryOpenTag}\nstuff\n{Autocompactor.SummaryCloseTag}")]);

        Assert.True(Autocompactor.IsCompactionSummary(msg));
    }

    // ─── Helpers ──────────────────────────────────────────────────────

    /// <summary>
    /// Builds a conversation with the given number of turns (user + tool + assistant each).
    /// </summary>
    private static List<ChatMessage> BuildLongConversation(int turnCount)
    {
        var messages = new List<ChatMessage>();
        for (var i = 1; i <= turnCount; i++)
        {
            messages.Add(new ChatMessage(ChatRole.User, $"User message {i}"));
            messages.Add(new ChatMessage(ChatRole.Tool,
                [new FunctionResultContent($"call-{i}", $"Tool result {i}")]));
            messages.Add(new ChatMessage(ChatRole.Assistant, $"Assistant response {i}"));
        }
        return messages;
    }

    /// <summary>
    /// Minimal chat client that returns a canned summary for the compaction call.
    /// </summary>
    private sealed class FakeSummarizingClient(string summaryText) : IChatClient
    {
        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default)
        {
            var response = new ChatResponse(new ChatMessage(ChatRole.Assistant, summaryText));
            return Task.FromResult(response);
        }

        public IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default) =>
            throw new NotImplementedException("Autocompactor uses non-streaming call.");

        public object? GetService(Type serviceType, object? serviceKey = null) => null;

        public void Dispose() { }
    }
}
