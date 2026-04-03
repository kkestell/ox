namespace Ur;

public sealed class ChatReadiness
{
    public bool CanRunTurns { get; }
    public IReadOnlyList<ChatBlockingIssue> BlockingIssues { get; }

    public ChatReadiness(IReadOnlyList<ChatBlockingIssue> blockingIssues)
    {
        BlockingIssues = blockingIssues;
        CanRunTurns = blockingIssues.Count == 0;
    }
}
