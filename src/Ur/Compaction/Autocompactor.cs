using Microsoft.Extensions.AI;
using Microsoft.Extensions.Logging;

namespace Ur.Compaction;

/// <summary>
/// Orchestrates conversation summarization when the context window fills up.
///
/// The compactor checks whether <c>lastInputTokens</c> exceeds a conservative
/// fraction of the context window (60%). If so, it asks the LLM to summarize
/// the conversation, replaces the old messages with a single summary message,
/// and preserves a tail of recent messages so the model retains immediate context.
///
/// The 60% threshold is deliberately conservative — it leaves ample headroom so
/// we never need reactive error recovery for context-too-long API errors.
///
/// Autocompactor is stateless: it receives its inputs as parameters and mutates
/// the provided message list in-place. UrSession owns the persistence step after
/// compaction returns true.
/// </summary>
internal static class Autocompactor
{
    /// <summary>
    /// The fraction of the context window that triggers compaction. When
    /// <c>lastInputTokens > contextWindow * Threshold</c>, compaction fires.
    /// </summary>
    internal const double Threshold = 0.60;

    /// <summary>
    /// Minimum number of content-bearing messages to preserve in the tail after
    /// compaction. "Content-bearing" means the message has text, tool results, or
    /// other meaningful content — empty tool scaffolding messages are skipped when
    /// counting but still preserved positionally.
    /// </summary>
    private const int MinPreservedContentMessages = 5;

    /// <summary>
    /// Tag used to wrap the summary so it is machine-recognizable for future
    /// compaction passes and session replay tools.
    /// </summary>
    internal const string SummaryOpenTag = "<context-summary>";
    internal const string SummaryCloseTag = "</context-summary>";

    /// <summary>
    /// Returns true if <paramref name="message"/> is a compaction summary produced
    /// by a previous <see cref="TryCompactAsync"/> call. Detection is based on the
    /// <see cref="SummaryOpenTag"/> prefix — the same tag the compactor wraps around
    /// its output. Used by <c>BuildLlmMessages</c> to project the summary as a
    /// System message at the LLM-call boundary.
    /// </summary>
    internal static bool IsCompactionSummary(ChatMessage message) =>
        message.Contents.OfType<TextContent>()
            .Any(tc => tc.Text.StartsWith(SummaryOpenTag, StringComparison.Ordinal));

    /// <summary>
    /// Attempts to compact the conversation if the context window fill level
    /// exceeds the threshold. When compaction occurs, older messages in
    /// <paramref name="messages"/> are replaced with a single summary message
    /// and a recent tail is preserved.
    ///
    /// Returns true if compaction occurred (caller must persist the new state).
    /// Returns false if the threshold was not exceeded or if the conversation
    /// is too short to compact meaningfully.
    /// </summary>
    /// <param name="messages">The mutable message list. Modified in-place on compaction.</param>
    /// <param name="client">Chat client for the summarization call (same model, same provider).</param>
    /// <param name="contextWindow">The model's total context window size in tokens.</param>
    /// <param name="lastInputTokens">Input tokens reported by the most recent LLM call.</param>
    /// <param name="logger">Logger for diagnostic output.</param>
    /// <param name="ct">Cancellation token.</param>
    public static async Task<bool> TryCompactAsync(
        List<ChatMessage> messages,
        IChatClient client,
        int contextWindow,
        long lastInputTokens,
        ILogger logger,
        CancellationToken ct)
    {
        // Threshold check: only compact when context fill exceeds the conservative limit.
        if (lastInputTokens <= contextWindow * Threshold)
        {
            logger.LogDebug(
                "Compaction skipped: {InputTokens} tokens <= {Threshold:P0} of {ContextWindow}",
                lastInputTokens, Threshold, contextWindow);
            return false;
        }

        // Don't compact if the conversation is too short to produce a meaningful summary.
        if (messages.Count <= MinPreservedContentMessages + 1)
        {
            logger.LogDebug(
                "Compaction skipped: only {Count} messages, need > {Min} to compact",
                messages.Count, MinPreservedContentMessages + 1);
            return false;
        }

        logger.LogInformation(
            "Compaction triggered: {InputTokens} tokens > {Threshold:P0} of {ContextWindow} ({FillPct:P0} full)",
            lastInputTokens, Threshold, contextWindow, (double)lastInputTokens / contextWindow);

        // Build the summarization request: a system prompt defining the task,
        // plus the full conversation serialized as a single user message.
        var conversationText = SerializeConversation(messages);
        var summarizationMessages = new List<ChatMessage>
        {
            new(ChatRole.System, SummaryPrompt.Build()),
            new(ChatRole.User, conversationText),
        };

        // Non-streaming call with no tools — we just need the summary text.
        var response = await client.GetResponseAsync(
            summarizationMessages,
            new ChatOptions { Tools = [], ToolMode = ChatToolMode.None },
            ct);

        var summaryText = response.Text;
        if (string.IsNullOrWhiteSpace(summaryText))
        {
            logger.LogWarning("Compaction aborted: summarization returned empty text");
            return false;
        }

        // Find the cut point: preserve the most recent messages that contain at
        // least MinPreservedContentMessages content-bearing messages. Walk backward
        // from the end, counting messages that have substantive content.
        var cutPoint = FindCutPoint(messages);

        // Build the summary message. Stored as User role, but BuildLlmMessages
        // projects it as System at send time via IsCompactionSummary detection.
        // The persisted role is cosmetic — it doesn't reach the LLM. The
        // <context-summary> tag makes it machine-recognizable for future
        // compaction passes, session replay tools, and the role projection.
        var summaryMessage = new ChatMessage(ChatRole.User,
            $"{SummaryOpenTag}\n{summaryText}\n{SummaryCloseTag}");

        // Replace messages[0..cutPoint) with the summary message.
        var preserved = messages.GetRange(cutPoint, messages.Count - cutPoint);
        messages.Clear();
        messages.Add(summaryMessage);
        messages.AddRange(preserved);

        logger.LogInformation(
            "Compaction complete: replaced {Replaced} messages with summary + {Preserved} preserved",
            cutPoint, preserved.Count);

        return true;
    }

    /// <summary>
    /// Determines where to cut: finds the index such that messages from
    /// [cutPoint..end] contain at least <see cref="MinPreservedContentMessages"/>
    /// content-bearing messages. A message is "content-bearing" if it contains
    /// text or function result content (not just empty tool call scaffolding).
    /// </summary>
    private static int FindCutPoint(List<ChatMessage> messages)
    {
        var contentCount = 0;
        for (var i = messages.Count - 1; i >= 0; i--)
        {
            if (IsContentBearing(messages[i]))
                contentCount++;

            if (contentCount >= MinPreservedContentMessages)
                return i;
        }

        // Conversation is too short to reach the minimum — cut at the midpoint
        // as a fallback so we always summarize at least something.
        return messages.Count / 2;
    }

    /// <summary>
    /// A message is content-bearing if it has text content or function result content.
    /// Empty assistant messages with only FunctionCallContent (tool call requests)
    /// are structural scaffolding, not substantive content.
    /// </summary>
    private static bool IsContentBearing(ChatMessage msg) =>
        msg.Contents.Any(c => c is TextContent or FunctionResultContent);

    /// <summary>
    /// Serializes the conversation into a single string for the summarization prompt.
    /// Each message is prefixed with its role for clarity.
    /// </summary>
    private static string SerializeConversation(List<ChatMessage> messages)
    {
        var sb = new System.Text.StringBuilder();
        foreach (var msg in messages)
        {
            sb.Append('[').Append(msg.Role.Value).Append("] ");

            foreach (var content in msg.Contents)
            {
                switch (content)
                {
                    case TextContent tc:
                        sb.AppendLine(tc.Text);
                        break;
                    case FunctionCallContent fcc:
                        sb.Append("Tool call: ").Append(fcc.Name);
                        sb.Append('(').Append(string.Join(", ",
                            fcc.Arguments?.Select(kv => $"{kv.Key}={kv.Value}") ?? []));
                        sb.AppendLine(")");
                        break;
                    case FunctionResultContent frc:
                        sb.Append("Tool result: ").AppendLine(frc.Result?.ToString() ?? "(null)");
                        break;
                    default:
                        // Skip UsageContent and other non-displayable types.
                        break;
                }
            }
        }
        return sb.ToString();
    }
}
