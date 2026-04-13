using Microsoft.Extensions.AI;

namespace Ox.Agent.Sessions;

/// <summary>
/// Abstracts session lifecycle and message persistence.
///
/// The contract is message-level: callers append, read, and replace messages
/// without knowing the underlying storage format. The default implementation
/// (<see cref="JsonlSessionStore"/>) uses append-only JSONL files with compact
/// boundary sentinels, but alternative backends (database, in-memory for tests)
/// can implement this interface without those assumptions.
///
/// <see cref="ReplaceAllAsync"/> is the compaction seam: after it completes,
/// <see cref="ReadAllAsync"/> returns exactly the provided messages. How the
/// backend achieves that is an implementation detail.
/// </summary>
public interface ISessionStore
{
    /// <summary>Creates a new session with a unique ID and backing storage.</summary>
    Session Create();

    /// <summary>Lists all persisted sessions, ordered by creation time.</summary>
    IReadOnlyList<Session> List();

    /// <summary>Retrieves a session by ID, or null if it doesn't exist.</summary>
    Session? GetById(string id);

    /// <summary>Appends a single message to the session's persistent storage.</summary>
    Task AppendAsync(Session session, ChatMessage message, CancellationToken ct = default);

    /// <summary>
    /// Reads all current messages for a session. After a <see cref="ReplaceAllAsync"/>
    /// call, this returns exactly the messages that were provided to that call
    /// (plus any subsequently appended via <see cref="AppendAsync"/>).
    /// </summary>
    Task<IReadOnlyList<ChatMessage>> ReadAllAsync(Session session, CancellationToken ct = default);

    /// <summary>
    /// Replaces the entire message history for a session. After this call completes,
    /// <see cref="ReadAllAsync"/> returns exactly <paramref name="messages"/>. This is
    /// the persistence primitive that compaction uses — the strategy decides what the
    /// new message list looks like, then hands it to the store.
    /// </summary>
    Task ReplaceAllAsync(Session session, IReadOnlyList<ChatMessage> messages, CancellationToken ct = default);

    /// <summary>
    /// Writes session metrics alongside the message data. The metrics file is a
    /// separate artifact that eval infrastructure reads without parsing messages.
    /// </summary>
    Task WriteMetricsAsync(Session session, SessionMetrics metrics, CancellationToken ct = default);
}
