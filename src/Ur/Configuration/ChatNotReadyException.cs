namespace Ur.Configuration;

/// <summary>
/// Thrown when the system lacks a required pre-condition (API key, model selection).
/// Carries the <see cref="Readiness"/> snapshot so the caller can display specific issues.
/// </summary>
public sealed class ChatNotReadyException(ChatReadiness readiness)
    : Exception("Chat is not ready to run turns.")
{
    public ChatReadiness Readiness { get; } = readiness;
}
