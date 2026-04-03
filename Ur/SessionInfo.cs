namespace Ur;

public sealed class SessionInfo
{
    public string Id { get; }
    public DateTimeOffset CreatedAt { get; }

    public SessionInfo(string id, DateTimeOffset createdAt)
    {
        Id = id;
        CreatedAt = createdAt;
    }
}
