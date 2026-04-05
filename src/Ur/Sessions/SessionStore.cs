using System.Text.Json;
using Microsoft.Extensions.AI;

namespace Ur.Sessions;

/// <summary>
/// Manages session lifecycle and persistence as JSONL files.
/// Each line is a JSON-serialized ChatMessage using M.E.AI's polymorphic serialization.
/// </summary>
internal sealed class SessionStore
{
    private readonly string _sessionsDirectory;
    private static readonly JsonSerializerOptions JsonOptions = new(AIJsonUtilities.DefaultOptions)
    {
        WriteIndented = false,
    };

    public SessionStore(string sessionsDirectory)
    {
        _sessionsDirectory = sessionsDirectory;
    }

    public Session Create()
    {
        var createdAt = DateTimeOffset.UtcNow;
        var id = createdAt.ToString("yyyyMMdd-HHmmss-fff");
        var filePath = Path.Combine(_sessionsDirectory, $"{id}.jsonl");
        return new Session(id, filePath, createdAt);
    }

    public IReadOnlyList<Session> List()
    {
        if (!Directory.Exists(_sessionsDirectory))
            return [];

        return Directory.GetFiles(_sessionsDirectory, "*.jsonl")
            .Order()
            .Select(path =>
            {
                var id = Path.GetFileNameWithoutExtension(path);
                var created = new DateTimeOffset(File.GetCreationTimeUtc(path), TimeSpan.Zero);
                return new Session(id, path, created);
            })
            .ToList();
    }

    public Session? Get(string id)
    {
        var filePath = Path.Combine(_sessionsDirectory, $"{id}.jsonl");
        if (!File.Exists(filePath))
            return null;

        var created = new DateTimeOffset(File.GetCreationTimeUtc(filePath), TimeSpan.Zero);
        return new Session(id, filePath, created);
    }

    public async Task AppendAsync(Session session, ChatMessage message, CancellationToken ct = default)
    {
        Directory.CreateDirectory(_sessionsDirectory);
        var json = JsonSerializer.Serialize(message, JsonOptions);
        await File.AppendAllTextAsync(session.FilePath, json + "\n", ct);
    }

    public async Task<IReadOnlyList<ChatMessage>> ReadAllAsync(Session session, CancellationToken ct = default)
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
                var msg = JsonSerializer.Deserialize<ChatMessage>(line, JsonOptions);
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
