namespace Ur.Sessions;

/// <summary>
/// A conversation within a workspace, persisted as a JSONL file.
/// </summary>
internal sealed class Session
{
    public string Id { get; }
    public string FilePath { get; }
    public DateTimeOffset CreatedAt { get; }

    public Session(string id, string filePath, DateTimeOffset createdAt)
    {
        Id = id;
        FilePath = filePath;
        CreatedAt = createdAt;
    }
}
