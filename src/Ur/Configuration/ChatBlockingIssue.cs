namespace Ur.Configuration;

/// <summary>
/// Reasons why the chat system cannot run a turn. Used by
/// <see cref="ChatReadiness.BlockingIssues"/> to guide the user toward
/// resolving the problem.
/// </summary>
public enum ChatBlockingIssue
{
    /// <summary>No OpenRouter API key stored in the OS keyring.</summary>
    MissingApiKey,
    /// <summary>No model selected in user or workspace settings.</summary>
    MissingModelSelection,
}
