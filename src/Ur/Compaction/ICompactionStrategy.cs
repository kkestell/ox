using Microsoft.Extensions.AI;

namespace Ur.Compaction;

/// <summary>
/// Decides whether to compact a conversation and performs the compaction.
///
/// The strategy owns the full decision: threshold check ("should I compact?")
/// and execution ("summarize and rebuild the message list"). The caller just
/// invokes it each turn and acts on the boolean result. When true, the caller
/// persists the modified message list via <see cref="Sessions.ISessionStore.ReplaceAllAsync"/>.
///
/// Registered as a singleton in DI — <see cref="IChatClient"/> is a parameter
/// (varies per session) rather than a constructor dependency.
/// </summary>
public interface ICompactionStrategy
{
    /// <summary>
    /// Attempts to compact the conversation. Modifies <paramref name="messages"/>
    /// in-place when compaction occurs.
    ///
    /// Returns true if compaction happened (caller must persist). Returns false
    /// if the threshold was not exceeded or the conversation is too short.
    /// </summary>
    /// <param name="messages">The mutable message list. Modified in-place on compaction.</param>
    /// <param name="chatClient">Chat client for the summarization call.</param>
    /// <param name="contextWindow">The model's total context window size in tokens.</param>
    /// <param name="lastInputTokens">Input tokens reported by the most recent LLM call.</param>
    /// <param name="ct">Cancellation token.</param>
    Task<bool> TryCompactAsync(
        List<ChatMessage> messages,
        IChatClient chatClient,
        int contextWindow,
        long lastInputTokens,
        CancellationToken ct);
}
