namespace Ox.Agent.Sessions;

public sealed class SessionInfo(string id, DateTimeOffset createdAt)
{
    public string Id { get; } = id;
    public DateTimeOffset CreatedAt { get; } = createdAt;
}
