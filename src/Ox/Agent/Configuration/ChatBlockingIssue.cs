namespace Ox.Agent.Configuration;

/// <summary>
/// Reasons why the chat system cannot run a turn. Used by
/// <see cref="ChatReadiness.BlockingIssues"/> to guide the user toward
/// resolving the problem.
/// </summary>
public enum ChatBlockingIssue
{
    /// <summary>
    /// The selected model's provider is not ready. This could mean a missing
    /// API key, unreachable endpoint, or unknown provider prefix. The specific
    /// issue is available from the provider's <c>GetBlockingIssue()</c> method.
    /// </summary>
    ProviderNotReady,

    /// <summary>No model selected in user or workspace settings.</summary>
    MissingModelSelection
}
