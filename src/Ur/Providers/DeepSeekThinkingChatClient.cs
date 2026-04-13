using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;

namespace Ur.Providers;

/// <summary>
/// An <see cref="IChatClient"/> decorator that extracts DeepSeek-R1-style
/// <c>&lt;think&gt;…&lt;/think&gt;</c> blocks from streaming <see cref="TextContent"/>
/// and re-emits them as <see cref="TextReasoningContent"/> items.
///
/// Why this is needed: DeepSeek-R1 (and OpenRouter's relay of it) embeds its
/// reasoning trace inline in the normal text stream — the thinking arrives as
/// <see cref="TextContent"/> with <c>&lt;think&gt;</c> tags rather than as a
/// separate <c>reasoning_content</c> field. The MEAI OpenAI adapter therefore
/// emits no <see cref="TextReasoningContent"/>, so the AgentLoop's content switch
/// would not see reasoning at all without this wrapper.
///
/// The wrapper is applied at the provider layer (inside
/// <see cref="OpenAiCompatibleProvider.CreateChatClient"/>) so that AgentLoop and
/// the rest of the stack stay unaware of this provider quirk.
///
/// Streaming design: the <c>&lt;think&gt;</c> / <c>&lt;/think&gt;</c> boundary may
/// arrive across multiple streaming chunks, so the wrapper maintains a small state
/// machine and a buffer holding at most <c>len("&lt;/think&gt;") - 1 = 7</c> chars to
/// avoid emitting text that might be the start of the close tag.
/// </summary>
internal sealed class DeepSeekThinkingChatClient : IChatClient
{
    private const string OpenTag = "<think>";
    private const string CloseTag = "</think>";

    private readonly IChatClient _inner;

    public DeepSeekThinkingChatClient(IChatClient inner) => _inner = inner;

    /// <summary>
    /// Non-streaming path delegates to the inner client unchanged. AgentLoop uses
    /// only the streaming path; this is provided for interface completeness.
    /// </summary>
    public Task<ChatResponse> GetResponseAsync(
        IEnumerable<ChatMessage> messages,
        ChatOptions? options = null,
        CancellationToken cancellationToken = default) =>
        _inner.GetResponseAsync(messages, options, cancellationToken);

    /// <summary>
    /// Streaming path: runs the inner stream through the think-tag state machine,
    /// transforming <c>&lt;think&gt;…&lt;/think&gt;</c> <see cref="TextContent"/>
    /// into <see cref="TextReasoningContent"/> items.
    /// </summary>
    public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
        IEnumerable<ChatMessage> messages,
        ChatOptions? options = null,
        [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        // State machine with a tiny lookahead buffer. Once the close tag is found
        // (or the response never started with <think>), state moves to Done and
        // subsequent updates pass through with no transformation.
        var state = ThinkParseState.AwaitingOpenTag;
        var buffer = new System.Text.StringBuilder();

        await foreach (var update in _inner.GetStreamingResponseAsync(messages, options, cancellationToken))
        {
            // Fast path: once Done (either tag processed or never present), pass
            // updates through without scanning their contents.
            if (state == ThinkParseState.Done)
            {
                yield return update;
                continue;
            }

            // Check whether any TextContent items need transformation.
            var hasText = update.Contents.Any(c => c is TextContent);
            if (!hasText)
            {
                yield return update;
                continue;
            }

            // Rebuild the contents list, transforming TextContent through the state machine.
            var newContents = new List<AIContent>(update.Contents.Count);
            foreach (var content in update.Contents)
            {
                if (content is not TextContent tc)
                {
                    newContents.Add(content);
                    continue;
                }

                buffer.Append(tc.Text ?? "");

                switch (state)
                {
                    case ThinkParseState.AwaitingOpenTag:
                        ProcessAwaitingOpenTag(buffer, newContents, ref state);
                        break;

                    case ThinkParseState.InThinking:
                        ProcessInThinking(buffer, newContents, ref state);
                        break;
                }
            }

            if (newContents.Count > 0)
            {
                // Copy all metadata from the original update so downstream consumers
                // (e.g. token usage accumulators) see the same update shape.
                yield return new ChatResponseUpdate(update.Role, newContents)
                {
                    MessageId = update.MessageId,
                    ModelId = update.ModelId,
                    FinishReason = update.FinishReason,
                    RawRepresentation = update.RawRepresentation,
                    AdditionalProperties = update.AdditionalProperties,
                    AuthorName = update.AuthorName,
                };
            }
        }
    }

    /// <summary>
    /// Delegates service lookup to the inner client after checking self.
    /// Standard decorator pattern for MEAI middleware.
    /// </summary>
    public object? GetService(Type serviceType, object? key = null) =>
        serviceType.IsInstanceOfType(this) ? this : _inner.GetService(serviceType, key);

    public void Dispose() => _inner.Dispose();

    // ─── State machine helpers ────────────────────────────────────────────────

    private enum ThinkParseState { AwaitingOpenTag, InThinking, Done }

    /// <summary>
    /// In <see cref="ThinkParseState.AwaitingOpenTag"/>: buffer holds text seen so far.
    ///
    /// Three outcomes:
    ///   1. Buffer starts with the complete <c>&lt;think&gt;</c> tag → strip it and
    ///      transition to <see cref="ThinkParseState.InThinking"/> (process remainder immediately).
    ///   2. Buffer is a strict prefix of <c>&lt;think&gt;</c> → need more input; do nothing.
    ///   3. Buffer cannot match <c>&lt;think&gt;</c> → emit as TextContent and go to Done.
    /// </summary>
    private static void ProcessAwaitingOpenTag(
        System.Text.StringBuilder buffer,
        List<AIContent> output,
        ref ThinkParseState state)
    {
        var text = buffer.ToString();

        if (text.StartsWith(OpenTag, StringComparison.Ordinal))
        {
            // Complete open tag found — strip it and process any trailing text immediately.
            buffer.Clear();
            buffer.Append(text[OpenTag.Length..]);
            state = ThinkParseState.InThinking;
            ProcessInThinking(buffer, output, ref state);
            return;
        }

        // Still a prefix of the open tag — wait for more chunks.
        if (OpenTag.StartsWith(text, StringComparison.Ordinal))
            return;

        // Not a think tag at all — emit as normal text and stop looking.
        output.Add(new TextContent(text));
        buffer.Clear();
        state = ThinkParseState.Done;
    }

    /// <summary>
    /// In <see cref="ThinkParseState.InThinking"/>: buffer holds accumulated reasoning
    /// text that has not yet been emitted.
    ///
    /// Two outcomes:
    ///   1. <c>&lt;/think&gt;</c> found → emit buffered text before the tag as
    ///      <see cref="TextReasoningContent"/>, text after as <see cref="TextContent"/>,
    ///      and transition to Done.
    ///   2. No close tag yet → emit all but the trailing
    ///      <c>len("&lt;/think&gt;") - 1 = 7</c> chars as <see cref="TextReasoningContent"/>
    ///      (the tail might be the start of the close tag). Keep the tail buffered.
    /// </summary>
    private static void ProcessInThinking(
        System.Text.StringBuilder buffer,
        List<AIContent> output,
        ref ThinkParseState state)
    {
        var text = buffer.ToString();
        var closeIdx = text.IndexOf(CloseTag, StringComparison.Ordinal);

        if (closeIdx >= 0)
        {
            // Emit reasoning text before the close tag.
            if (closeIdx > 0)
                output.Add(new TextReasoningContent(text[..closeIdx]));

            // Emit any response text that follows the close tag.
            var remainder = text[(closeIdx + CloseTag.Length)..];
            if (remainder.Length > 0)
                output.Add(new TextContent(remainder));

            buffer.Clear();
            state = ThinkParseState.Done;
            return;
        }

        // No close tag yet. Emit the safe portion — all but the last 7 chars — as
        // reasoning content. Retain the tail in the buffer in case the next chunk
        // completes the </think> tag. 7 = len("</think>") - 1.
        const int maxTagTail = 7;
        var safeLength = Math.Max(0, text.Length - maxTagTail);
        if (safeLength > 0)
        {
            output.Add(new TextReasoningContent(text[..safeLength]));
            buffer.Clear();
            buffer.Append(text[safeLength..]);
        }
    }
}
