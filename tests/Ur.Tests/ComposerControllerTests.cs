using Ox;

namespace Ur.Tests;

/// <summary>
/// Unit tests for the ComposerController queue/mode contract.
///
/// These tests verify the new event-driven submission model:
/// - typed-ahead chat input queues without a consumer waiting,
/// - permission mode never drains the chat queue,
/// - cancellation denies permission without killing the chat channel,
/// - the controller routes submissions to the right destination based on mode.
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

    // ---- Permission mode isolation ----

    [Fact]
    public async Task PermissionMode_DoesNotConsumeQueuedChatSubmissions()
    {
        var ctrl = new ComposerController();

        // The user typed ahead a chat message while the agent was running.
        ctrl.OnViewSubmit("queued chat");

        // A permission prompt arrives.
        var permTask = ctrl.EnterPermissionMode();
        Assert.Equal(ComposerMode.Permission, ctrl.Mode);

        // The user answers the permission prompt.
        ctrl.OnViewSubmit("y");
        Assert.Equal("y", await permTask);

        ctrl.ExitPermissionMode();
        Assert.Equal(ComposerMode.Chat, ctrl.Mode);

        // The queued chat message must still be in the channel, untouched.
        Assert.True(ctrl.ChatSubmissions.TryRead(out var queued));
        Assert.Equal("queued chat", queued);
    }

    [Fact]
    public async Task PermissionMode_RoutesSubmitToTcs_NotChannel()
    {
        var ctrl = new ComposerController();

        var permTask = ctrl.EnterPermissionMode();
        ctrl.OnViewSubmit("session");

        Assert.Equal("session", await permTask);

        // Nothing should have landed in the chat channel.
        Assert.False(ctrl.ChatSubmissions.TryRead(out _));
    }

    // ---- Cancellation semantics ----

    [Fact]
    public async Task PermissionCancellation_DeniesWithoutAffectingChatQueue()
    {
        var ctrl = new ComposerController();

        // Pre-queue a chat message.
        ctrl.OnViewSubmit("chat message");

        var cts = new CancellationTokenSource();
        var permTask = ctrl.EnterPermissionMode(cts.Token);

        // Cancel the permission request.
        await cts.CancelAsync();

        // Permission resolves with null (denied).
        Assert.Null(await permTask);

        ctrl.ExitPermissionMode();

        // Chat queue is intact.
        Assert.True(ctrl.ChatSubmissions.TryRead(out var chat));
        Assert.Equal("chat message", chat);
    }

    [Fact]
    public async Task PermissionEof_DeniesWithoutCompletingChatChannel()
    {
        var ctrl = new ComposerController();

        var permTask = ctrl.EnterPermissionMode();
        ctrl.OnViewEof(); // Ctrl+C / Ctrl+D during permission prompt

        // Permission denied.
        Assert.Null(await permTask);

        ctrl.ExitPermissionMode();

        // Channel is still open: a new chat submission should queue normally.
        ctrl.OnViewSubmit("still works");
        Assert.True(ctrl.ChatSubmissions.TryRead(out var result));
        Assert.Equal("still works", result);
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

    // ---- Mode guard ----

    [Fact]
    public void ExitPermissionMode_RestoresChatMode()
    {
        var ctrl = new ComposerController();
        _ = ctrl.EnterPermissionMode();
        Assert.Equal(ComposerMode.Permission, ctrl.Mode);

        ctrl.ExitPermissionMode();
        Assert.Equal(ComposerMode.Chat, ctrl.Mode);
    }
}
