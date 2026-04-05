namespace Ur.Configuration;

/// <summary>
/// A pre-flight check result that tells the UI whether the system is ready to
/// run a chat turn. If not ready, <see cref="BlockingIssues"/> lists the
/// specific problems the user needs to fix (e.g. missing API key, no model).
///
/// This is computed on demand by <see cref="UrConfiguration.Readiness"/> so
/// that the UI always gets a fresh snapshot.
/// </summary>
public sealed class ChatReadiness(IReadOnlyList<ChatBlockingIssue> blockingIssues)
{
    public bool CanRunTurns { get; } = blockingIssues.Count == 0;
    public IReadOnlyList<ChatBlockingIssue> BlockingIssues { get; } = blockingIssues;
}
