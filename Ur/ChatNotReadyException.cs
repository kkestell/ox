namespace Ur;

public sealed class ChatNotReadyException : Exception
{
    public ChatReadiness Readiness { get; }

    public ChatNotReadyException(ChatReadiness readiness)
        : base("Chat is not ready to run turns.")
    {
        Readiness = readiness;
    }
}
