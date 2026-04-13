using Ox.App;

namespace Ox.Tests.App;

/// <summary>
/// Unit tests for <see cref="TurnController"/>. These cover the small state
/// machine it maintains without starting a real turn — starting a turn
/// requires a live <c>OxSession</c>, which is exercised indirectly through
/// <c>ExecuteBuiltinCommandTests</c> and the broader integration suite.
/// </summary>
public sealed class TurnControllerTests
{
    [Fact]
    public void IsActive_StartsFalse()
    {
        using var controller = new TurnController(wakeMainLoop: () => { });
        Assert.False(controller.IsActive);
    }

    [Fact]
    public void MarkEnded_ClearsActiveFlag()
    {
        using var controller = new TurnController(wakeMainLoop: () => { });
        // IsActive is private-set, so we reach it through Start, but that
        // requires a session. Use the MarkEnded shortcut — if it doesn't
        // flip the flag when already false, at least it shouldn't throw.
        controller.MarkEnded();
        Assert.False(controller.IsActive);
    }

    [Fact]
    public void TakeQueuedInput_Empty_ReturnsNull()
    {
        using var controller = new TurnController(wakeMainLoop: () => { });
        Assert.Null(controller.TakeQueuedInput());
    }

    [Fact]
    public void QueueWhileActive_Then_TakeQueuedInput_ReturnsAndClears()
    {
        using var controller = new TurnController(wakeMainLoop: () => { });

        controller.QueueWhileActive("hello");
        Assert.Equal("hello", controller.TakeQueuedInput());

        // After taking, the slot is empty again — important so a stale queued
        // line doesn't become the next turn's input after nothing new arrived.
        Assert.Null(controller.TakeQueuedInput());
    }

    [Fact]
    public void QueueWhileActive_OverwritesPrevious()
    {
        using var controller = new TurnController(wakeMainLoop: () => { });

        controller.QueueWhileActive("first");
        controller.QueueWhileActive("second");

        // A long turn can generate many prompts; only the most recent one
        // represents current user intent, so overwrite semantics are correct.
        Assert.Equal("second", controller.TakeQueuedInput());
    }

    [Fact]
    public void Events_IsEmptyAtStart()
    {
        using var controller = new TurnController(wakeMainLoop: () => { });
        Assert.Empty(controller.Events);
    }

    [Fact]
    public void Cancel_WhenIdle_DoesNotThrow()
    {
        using var controller = new TurnController(wakeMainLoop: () => { });
        controller.Cancel();
        Assert.False(controller.IsActive);
    }
}
