namespace Ur;

public sealed class UrSessionInfo
{
    public string Id { get; }
    public DateTimeOffset CreatedAt { get; }

    public UrSessionInfo(string id, DateTimeOffset createdAt)
    {
        Id = id;
        CreatedAt = createdAt;
    }
}
