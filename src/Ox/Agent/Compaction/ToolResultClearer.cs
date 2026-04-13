using Microsoft.Extensions.AI;

namespace Ox.Agent.Compaction;

/// <summary>
/// Pure projection function: replaces tool results in old turns with a placeholder
/// to reclaim context window space without mutating the persisted message history.
///
/// "Old" is defined by counting assistant messages backwards from the end of the
/// conversation — each assistant message marks one turn boundary. Tool results in
/// messages older than <c>turnsToKeep</c> turns are replaced with
/// "[Tool result cleared]" so the LLM no longer pays the token cost for stale
/// tool output while retaining the structural signal that a tool was called.
/// </summary>
internal static class ToolResultClearer
{
    internal const string ClearedPlaceholder = "[Tool result cleared]";

    /// <summary>
    /// Returns a new message sequence with old tool results replaced. The input
    /// sequence is never mutated — callers get a fresh projection safe for LLM
    /// submission while the persisted message list stays intact.
    /// </summary>
    /// <param name="messages">The full conversation (user + assistant + tool messages).</param>
    /// <param name="turnsToKeep">
    /// How many recent assistant turns' tool results to preserve verbatim.
    /// A turn boundary is each <see cref="ChatRole.Assistant"/> message.
    /// </param>
    public static IEnumerable<ChatMessage> ClearOldToolResults(
        IEnumerable<ChatMessage> messages, int turnsToKeep)
    {
        // Materialize once so we can do a backwards scan for turn boundaries
        // without multiple enumeration. The caller's list is typically already
        // in memory (it's the shared _messages list).
        var list = messages as IList<ChatMessage> ?? messages.ToList();

        // Walk backwards to find the index that separates "recent" from "old".
        // Every assistant message counts as one turn boundary. Once we've counted
        // turnsToKeep boundaries, everything before that point is "old".
        var cutIndex = FindCutIndex(list, turnsToKeep);

        for (var i = 0; i < list.Count; i++)
        {
            var msg = list[i];

            // Messages in the recent tail pass through unchanged.
            if (i >= cutIndex)
            {
                yield return msg;
                continue;
            }

            // Only tool-result messages need clearing. Other roles (user,
            // assistant, system) pass through even in the old region.
            if (!HasFunctionResults(msg))
            {
                yield return msg;
                continue;
            }

            // Build a replacement message with each FunctionResultContent's
            // result swapped for the placeholder. Non-FunctionResultContent
            // items (unlikely in a tool message, but defensive) are preserved.
            var cleared = new ChatMessage(msg.Role,
                msg.Contents.Select(c => c is FunctionResultContent frc
                    ? new FunctionResultContent(frc.CallId, ClearedPlaceholder)
                    : c).ToList());
            yield return cleared;
        }
    }

    /// <summary>
    /// Finds the message index at or above which messages are considered "recent"
    /// (their tool results should be preserved). Walks backwards counting assistant
    /// messages as turn boundaries, then continues backward to include the full
    /// turn (user + tool messages that precede the boundary assistant message).
    /// </summary>
    private static int FindCutIndex(IList<ChatMessage> messages, int turnsToKeep)
    {
        var turnsSeen = 0;
        for (var i = messages.Count - 1; i >= 0; i--)
        {
            if (messages[i].Role == ChatRole.Assistant)
            {
                turnsSeen++;
                if (turnsSeen >= turnsToKeep)
                {
                    // Found the Nth assistant message. Walk backward to include
                    // the full turn — user and tool messages that precede this
                    // assistant message belong to the same turn.
                    while (i > 0 && messages[i - 1].Role != ChatRole.Assistant)
                        i--;
                    return i;
                }
            }
        }

        // Fewer turns than the threshold — nothing to clear.
        return 0;
    }

    private static bool HasFunctionResults(ChatMessage msg) =>
        msg.Contents.Any(c => c is FunctionResultContent);
}
