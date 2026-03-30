namespace Ur;

public sealed class UrChatNotReadyException : Exception
{
    public UrChatReadiness Readiness { get; }

    public UrChatNotReadyException(UrChatReadiness readiness)
        : base("Chat is not ready to run turns.")
    {
        Readiness = readiness;
    }
}
