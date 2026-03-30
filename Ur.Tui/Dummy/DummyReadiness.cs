namespace Ur.Tui.Dummy;

public enum DummyBlockingIssue
{
    MissingApiKey,
    MissingModelSelection,
}

public sealed class DummyReadiness
{
    public bool CanChat { get; }
    public IReadOnlyList<DummyBlockingIssue> BlockingIssues { get; }

    public DummyReadiness(IReadOnlyList<DummyBlockingIssue> blockingIssues)
    {
        BlockingIssues = blockingIssues;
        CanChat = blockingIssues.Count == 0;
    }
}
