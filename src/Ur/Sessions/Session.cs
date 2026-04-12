namespace Ur.Sessions;

/// <summary>
/// A conversation within a workspace, persisted as a JSONL file.
/// </summary>
public sealed class Session(string id, string filePath, DateTimeOffset createdAt)
{
    public string Id { get; } = id;
    public string FilePath { get; } = filePath;
    public DateTimeOffset CreatedAt { get; } = createdAt;
}
