namespace Ur.Configuration;

/// <summary>
/// Thrown when the system lacks a required pre-condition (API key, model selection).
/// Carries the <see cref="Readiness"/> snapshot so the caller can display specific issues.
/// </summary>
public sealed class ChatNotReadyException : Exception
{
    public ChatReadiness Readiness { get; }

    public ChatNotReadyException(ChatReadiness readiness)
        : base("Chat is not ready to run turns.")
    {
        Readiness = readiness;
    }
}
