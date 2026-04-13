using Ox;

namespace Ox.Tests;

/// <summary>
/// Unit tests for the ComposerController chat channel contract.
///
/// These tests verify the event-driven submission model:
/// - typed-ahead chat input queues without a consumer waiting,
/// - EOF completes the channel cleanly.
///
/// Permission prompts are now handled by PermissionPromptView (a separate widget)
/// and no longer flow through the ComposerController at all.
///
/// None of these tests touch Terminal.Gui, because the controller is a pure
/// coordination type. That separation is intentional: the controller should
/// remain testable independent of the UI framework.
/// </summary>
public sealed class ComposerControllerTests
{
    // ---- Chat submission queueing ----

    [Fact]
    public async Task ChatSubmission_QueuesWhileNoConsumerWaiting()
    {
        var ctrl = new ComposerController();

        // Simulate the user pressing Enter while the REPL loop is still
        // processing a previous turn — no one is awaiting the channel yet.
        ctrl.OnViewSubmit("hello");

        // When the loop finally reads, the message must be there.
        var result = await ctrl.ChatSubmissions.ReadAsync();
        Assert.Equal("hello", result);
    }

    [Fact]
    public async Task ChatSubmissions_QueueInFifoOrder()
    {
        var ctrl = new ComposerController();

        ctrl.OnViewSubmit("first");
        ctrl.OnViewSubmit("second");
        ctrl.OnViewSubmit("third");

        Assert.Equal("first",  await ctrl.ChatSubmissions.ReadAsync());
        Assert.Equal("second", await ctrl.ChatSubmissions.ReadAsync());
        Assert.Equal("third",  await ctrl.ChatSubmissions.ReadAsync());
    }

    // ---- EOF in chat mode ----

    [Fact]
    public void ChatEof_CompletesChannel()
    {
        var ctrl = new ComposerController();
        ctrl.OnViewEof();

        // Channel writer is completed; ReadAsync would return immediately with
        // ChannelClosedException. TryRead returns false since there is no item.
        Assert.True(ctrl.ChatSubmissions.Completion.IsCompleted);
    }
}
