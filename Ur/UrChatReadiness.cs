namespace Ur;

public sealed class UrChatReadiness
{
    public bool CanRunTurns { get; }
    public IReadOnlyList<UrChatBlockingIssue> BlockingIssues { get; }

    public UrChatReadiness(IReadOnlyList<UrChatBlockingIssue> blockingIssues)
    {
        BlockingIssues = blockingIssues;
        CanRunTurns = blockingIssues.Count == 0;
    }
}
