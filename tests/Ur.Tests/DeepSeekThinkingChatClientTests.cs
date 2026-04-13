using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;
using Ur.Providers;

// Needed for the ChunkStubClient's async iterator (EnumeratorCancellation).
#pragma warning disable CS8424 // suppress "no parameters" warning for EnumeratorCancellation in nested class

namespace Ur.Tests;

/// <summary>
/// Unit tests for <see cref="DeepSeekThinkingChatClient"/>.
///
/// The decorator's core job is to extract <c>&lt;think&gt;…&lt;/think&gt;</c> blocks
/// from streaming <see cref="TextContent"/> updates and re-emit them as
/// <see cref="TextReasoningContent"/>. The state machine must handle:
///   - tags wholly within a single chunk
///   - tags split across two or more chunks (open or close)
///   - responses that have no think tag (pass-through)
///   - non-TextContent items passing through unchanged
/// </summary>
public sealed class DeepSeekThinkingChatClientTests
{
    // ─── Happy path ───────────────────────────────────────────────────────────

    [Fact]
    public async Task SingleChunk_WithThinkBlock_ExtractsReasoningAndText()
    {
        // Single chunk containing the full <think>...</think> tag followed by the answer.
        var inner = StubClient("<think>I should reason</think>The answer is 42");
        var sut = new DeepSeekThinkingChatClient(inner);

        var items = await CollectContentsAsync(sut);

        var reasoning = Assert.Single(items.OfType<TextReasoningContent>());
        Assert.Equal("I should reason", reasoning.Text);

        var text = Assert.Single(items.OfType<TextContent>());
        Assert.Equal("The answer is 42", text.Text);
    }

    [Fact]
    public async Task NoThinkBlock_PassesThroughAsTextContent()
    {
        // Response without any <think> tag — normal text, no wrapper overhead.
        var inner = StubClient("Just a plain answer.");
        var sut = new DeepSeekThinkingChatClient(inner);

        var items = await CollectContentsAsync(sut);

        var text = Assert.Single(items.OfType<TextContent>());
        Assert.Equal("Just a plain answer.", text.Text);
        Assert.Empty(items.OfType<TextReasoningContent>());
    }

    [Fact]
    public async Task ThinkingOnly_NoRemainder_EmitsJustReasoning()
    {
        // Response that is all thinking with no text after </think>.
        var inner = StubClient("<think>Only reasoning, no response</think>");
        var sut = new DeepSeekThinkingChatClient(inner);

        var items = await CollectContentsAsync(sut);

        var reasoning = Assert.Single(items.OfType<TextReasoningContent>());
        Assert.Equal("Only reasoning, no response", reasoning.Text);
        Assert.Empty(items.OfType<TextContent>());
    }

    // ─── Streaming split scenarios ────────────────────────────────────────────

    [Fact]
    public async Task OpenTagSplitAcrossChunks_ExtractsCorrectly()
    {
        // The <think> tag is split: "<thi" in chunk 1, "nk>reason</think>answer" in chunk 2.
        var inner = StubClient("<thi", "nk>reason</think>answer");
        var sut = new DeepSeekThinkingChatClient(inner);

        var items = await CollectContentsAsync(sut);

        Assert.Equal("reason", ConcatReasoning(items));
        Assert.Equal("answer", ConcatText(items));
    }

    [Fact]
    public async Task CloseTagSplitAcrossChunks_ExtractsCorrectly()
    {
        // The </think> tag is split: chunk 1 ends with "</thi", chunk 2 starts with "nk>answer".
        var inner = StubClient("<think>reason</thi", "nk>answer");
        var sut = new DeepSeekThinkingChatClient(inner);

        var items = await CollectContentsAsync(sut);

        Assert.Equal("reason", ConcatReasoning(items));
        Assert.Equal("answer", ConcatText(items));
    }

    [Fact]
    public async Task LargeReasoningInManyChunks_AccumulatesCorrectly()
    {
        // Reasoning text arrives across multiple small chunks before the close tag.
        var inner = StubClient("<think>", "step one ", "step two ", "step three", "</think>result");
        var sut = new DeepSeekThinkingChatClient(inner);

        var items = await CollectContentsAsync(sut);

        Assert.Equal("step one step two step three", ConcatReasoning(items));
        Assert.Equal("result", ConcatText(items));
    }

    [Fact]
    public async Task OpenTagExactlyAtChunkBoundary_TransitionsCorrectly()
    {
        // Chunk 1 is exactly "<think>", chunk 2 is the reasoning and close tag.
        var inner = StubClient("<think>", "reasoning</think>answer");
        var sut = new DeepSeekThinkingChatClient(inner);

        var items = await CollectContentsAsync(sut);

        Assert.Equal("reasoning", ConcatReasoning(items));
        Assert.Equal("answer", ConcatText(items));
    }

    [Fact]
    public async Task CloseTagExactlyAtChunkBoundary_EmitsReasoningAndText()
    {
        // The close tag is exactly the entire second chunk.
        var inner = StubClient("<think>reasoning", "</think>", "answer");
        var sut = new DeepSeekThinkingChatClient(inner);

        var items = await CollectContentsAsync(sut);

        Assert.Equal("reasoning", ConcatReasoning(items));
        Assert.Equal("answer", ConcatText(items));
    }

    // ─── Non-text content pass-through ───────────────────────────────────────

    [Fact]
    public async Task NonTextContent_PassesThroughUnchanged()
    {
        // A usage content item mixed with a text+think response.
        var usageContent = new UsageContent(new UsageDetails { InputTokenCount = 42 });
        var inner = StubClient(
            [new TextContent("<think>r</think>done"), usageContent]);
        var sut = new DeepSeekThinkingChatClient(inner);

        var items = await CollectContentsAsync(sut);

        Assert.Single(items.OfType<TextReasoningContent>());
        Assert.Single(items.OfType<TextContent>());
        // UsageContent should pass through in the same update.
        var usage = Assert.Single(items.OfType<UsageContent>());
        Assert.Equal(42, usage.Details.InputTokenCount);
    }

    // ─── Edge cases ───────────────────────────────────────────────────────────

    [Fact]
    public async Task EmptyChunkBeforeOpenTag_DoesNotCauseSpuriousTextEntry()
    {
        // An empty chunk arriving before <think> should not be emitted as a TextContent
        // and must not cause a premature Done transition that would miss the think block.
        var inner = StubClient("", "<think>r</think>done");
        var sut = new DeepSeekThinkingChatClient(inner);

        var items = await CollectContentsAsync(sut);

        Assert.Single(items.OfType<TextReasoningContent>());
        Assert.Single(items.OfType<TextContent>());
        Assert.DoesNotContain(items.OfType<TextContent>(), tc => tc.Text?.Length == 0);
    }

    [Fact]
    public async Task TextAfterThinkBlock_DoesNotReEmitAsFurtherReasoning()
    {
        // Verify Done state is sticky — text after </think> stays as TextContent.
        var inner = StubClient("<think>r</think>answer part 1 ", "answer part 2");
        var sut = new DeepSeekThinkingChatClient(inner);

        var items = await CollectContentsAsync(sut);

        Assert.Equal("r", ConcatReasoning(items));
        // Both post-think chunks should be TextContent.
        Assert.Contains("answer part 1", ConcatText(items));
        Assert.Contains("answer part 2", ConcatText(items));
        Assert.DoesNotContain(items.OfType<TextReasoningContent>(), trc => trc.Text != "r");
    }

    // ─── Test helpers ─────────────────────────────────────────────────────────

    /// <summary>
    /// Creates a stub client that emits the given text strings as individual
    /// TextContent chunks in a single streaming response.
    /// </summary>
    private static IChatClient StubClient(params string[] chunks)
    {
        var updates = chunks
            .Select(c => new ChatResponseUpdate(ChatRole.Assistant, [new TextContent(c)]))
            .ToList();
        return new ChunkStubClient(updates);
    }

    /// <summary>
    /// Creates a stub client that emits the given <see cref="AIContent"/> items
    /// as a single streaming update.
    /// </summary>
    private static IChatClient StubClient(IList<AIContent> contents) =>
        new ChunkStubClient([new ChatResponseUpdate(ChatRole.Assistant, contents)]);

    private static async Task<List<AIContent>> CollectContentsAsync(IChatClient client)
    {
        var result = new List<AIContent>();
        await foreach (var update in client.GetStreamingResponseAsync([], options: null))
            result.AddRange(update.Contents);
        return result;
    }

    private static string ConcatReasoning(IEnumerable<AIContent> items) =>
        string.Concat(items.OfType<TextReasoningContent>().Select(trc => trc.Text ?? ""));

    private static string ConcatText(IEnumerable<AIContent> items) =>
        string.Concat(items.OfType<TextContent>().Select(tc => tc.Text ?? ""));

    // ─── Minimal stub IChatClient ─────────────────────────────────────────────

    /// <summary>
    /// A minimal IChatClient that replays a pre-built list of streaming updates.
    /// Only GetStreamingResponseAsync is implemented — it's the only path exercised
    /// by these tests and by AgentLoop in production.
    /// </summary>
    private sealed class ChunkStubClient(IReadOnlyList<ChatResponseUpdate> updates) : IChatClient
    {
        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default) =>
            throw new NotSupportedException("Only streaming path is used.");

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken cancellationToken = default)
        {
            foreach (var update in updates)
            {
                cancellationToken.ThrowIfCancellationRequested();
                yield return update;
            }
            await Task.CompletedTask;
        }

        public object? GetService(Type serviceType, object? key = null) => null;
        public void Dispose() { }
    }
}
