using System.Text.Json;
using System.Text.Json.Serialization.Metadata;
using Microsoft.Extensions.AI;

namespace Ur.Sessions;

/// <summary>
/// Manages session lifecycle and persistence as JSONL files.
/// Each line is a JSON-serialized ChatMessage using M.E.AI's polymorphic serialization.
/// </summary>
internal sealed class SessionStore(string sessionsDirectory)
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
        var id = createdAt.ToString("yyyyMMdd-HHmmss-fff");
        var filePath = Path.Combine(sessionsDirectory, $"{id}.jsonl");
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
            System.Globalization.CultureInfo.InvariantCulture,
            System.Globalization.DateTimeStyles.AssumeUniversal,
            out var parsed)
            ? parsed
            : null;

    public Session? Get(string id)
    {
        var filePath = Path.Combine(sessionsDirectory, $"{id}.jsonl");
        if (!File.Exists(filePath))
            return null;

        var created = new DateTimeOffset(File.GetCreationTimeUtc(filePath), TimeSpan.Zero);
        return new Session(id, filePath, created);
    }

    public async Task AppendAsync(Session session, ChatMessage message, CancellationToken ct = default)
    {
        Directory.CreateDirectory(sessionsDirectory);
        var json = JsonSerializer.Serialize(message, ChatMessageTypeInfo);
        await File.AppendAllTextAsync(session.FilePath, json + "\n", ct);
    }

    public static async Task<IReadOnlyList<ChatMessage>> ReadAllAsync(Session session, CancellationToken ct = default)
    {
        if (!File.Exists(session.FilePath))
            return [];

        var lines = await File.ReadAllLinesAsync(session.FilePath, ct);
        var messages = new List<ChatMessage>(lines.Length);
        foreach (var line in lines)
        {
            if (string.IsNullOrWhiteSpace(line))
                continue;

            try
            {
                var msg = JsonSerializer.Deserialize(line, ChatMessageTypeInfo);
                if (msg is not null)
                    messages.Add(msg);
            }
            catch (JsonException)
            {
                // Malformed trailing lines (crash during write) are silently skipped.
            }
        }
        return messages;
    }
}
