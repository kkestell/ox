using System.Globalization;
using System.Text.Json;
using System.Text.Json.Serialization.Metadata;
using Microsoft.Extensions.AI;
using Microsoft.Extensions.Logging;

namespace Ur.Sessions;

/// <summary>
/// Manages session lifecycle and persistence as JSONL files.
/// Each line is a JSON-serialized ChatMessage using M.E.AI's polymorphic serialization.
/// </summary>
internal sealed class SessionStore(string sessionsDirectory, ILogger<SessionStore>? logger = null)
{
    private static readonly JsonSerializerOptions JsonOptions = new(AIJsonUtilities.DefaultOptions)
    {
        WriteIndented = false
    };

    // Resolved once from the M.E.AI source-gen resolver chain for AoT-safe
    // serialize/deserialize calls.
    private static readonly JsonTypeInfo<ChatMessage> ChatMessageTypeInfo =
        (JsonTypeInfo<ChatMessage>)JsonOptions.GetTypeInfo(typeof(ChatMessage));

    public Session Create()
    {
        var createdAt = DateTimeOffset.UtcNow;
        var id = createdAt.ToString("yyyyMMdd-HHmmss-fff", CultureInfo.InvariantCulture);
        var filePath = Path.Combine(sessionsDirectory, $"{id}.jsonl");
        logger?.LogDebug("Session created: {SessionId}", id);
        return new Session(id, filePath, createdAt);
    }

    public IReadOnlyList<Session> List()
    {
        if (!Directory.Exists(sessionsDirectory))
            return [];

        return Directory.GetFiles(sessionsDirectory, "*.jsonl")
            .Order()
            .Select(path =>
            {
                var id = Path.GetFileNameWithoutExtension(path);
                // Parse the creation timestamp from the session ID (format: yyyyMMdd-HHmmss-fff)
                // instead of hitting the filesystem for each file's metadata.
                var created = ParseSessionTimestamp(id)
                    ?? new DateTimeOffset(File.GetCreationTimeUtc(path), TimeSpan.Zero);
                return new Session(id, path, created);
            })
            .ToList();
    }

    private static DateTimeOffset? ParseSessionTimestamp(string id) =>
        DateTimeOffset.TryParseExact(
            id,
            "yyyyMMdd-HHmmss-fff",
            CultureInfo.InvariantCulture,
            DateTimeStyles.AssumeUniversal,
            out var parsed)
            ? parsed
            : null;

    public Session? Get(string id)
    {
        var filePath = Path.Combine(sessionsDirectory, $"{id}.jsonl");
        if (!File.Exists(filePath))
            return null;

        var created = ParseSessionTimestamp(id)
            ?? new DateTimeOffset(File.GetCreationTimeUtc(filePath), TimeSpan.Zero);
        return new Session(id, filePath, created);
    }

    public async Task AppendAsync(Session session, ChatMessage message, CancellationToken ct = default)
    {
        Directory.CreateDirectory(sessionsDirectory);
        var json = JsonSerializer.Serialize(message, ChatMessageTypeInfo);
        await File.AppendAllTextAsync(session.FilePath, json + "\n", ct);
    }

    /// <summary>
    /// Sentinel JSON line format for compact boundaries. This object won't
    /// deserialize as a valid <see cref="ChatMessage"/> — it's a structural
    /// delimiter that tells <see cref="ReadAllAsync"/> to discard everything
    /// before the last boundary. Old (pre-boundary) messages remain on disk
    /// but are ignored on reload.
    /// </summary>
    private const string CompactBoundarySentinel = """{"__compact_boundary":true}""";

    /// <summary>
    /// Writes a compact-boundary sentinel line to the session file. After this
    /// line, the caller appends the compacted messages (summary + preserved tail).
    /// On next <see cref="ReadAllAsync"/>, only post-boundary messages are loaded.
    /// </summary>
    public async Task AppendCompactBoundaryAsync(Session session, CancellationToken ct = default)
    {
        Directory.CreateDirectory(sessionsDirectory);
        await File.AppendAllTextAsync(session.FilePath, CompactBoundarySentinel + "\n", ct);
        logger?.LogDebug("Wrote compact boundary to session '{SessionId}'", session.Id);
    }

    public async Task<IReadOnlyList<ChatMessage>> ReadAllAsync(Session session, CancellationToken ct = default)
    {
        if (!File.Exists(session.FilePath))
            return [];

        var lines = await File.ReadAllLinesAsync(session.FilePath, ct);

        // Find the last compact-boundary sentinel. Everything before it (inclusive)
        // is pre-compaction history that we skip. This must happen before the
        // deserialization loop — boundary markers are intentional delimiters, not
        // crash-corrupted lines that the error-recovery path should silently skip.
        var startIndex = 0;
        for (var i = lines.Length - 1; i >= 0; i--)
        {
            if (lines[i].Contains("__compact_boundary", StringComparison.Ordinal))
            {
                startIndex = i + 1;
                logger?.LogDebug(
                    "Compact boundary found at line {Line}, skipping {Skipped} pre-boundary messages",
                    i, i + 1);
                break;
            }
        }

        var messages = new List<ChatMessage>(lines.Length - startIndex);
        for (var i = startIndex; i < lines.Length; i++)
        {
            var line = lines[i];
            if (string.IsNullOrWhiteSpace(line))
                continue;

            try
            {
                var msg = JsonSerializer.Deserialize(line, ChatMessageTypeInfo);
                if (msg is not null)
                    messages.Add(msg);
            }
            catch (JsonException ex)
            {
                // Malformed trailing lines (crash during write) — log and skip rather than
                // losing the entire session.
                logger?.LogWarning("Skipping malformed message in session '{Path}': {Error}",
                    session.FilePath, ex.Message);
            }
        }

        logger?.LogDebug("Loaded session '{SessionId}': {MessageCount} messages", session.Id, messages.Count);
        return messages;
    }
}
