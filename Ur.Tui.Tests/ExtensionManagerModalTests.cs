using Ur.Extensions;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Ur.Tui.Components;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Tests;

public class ExtensionManagerModalTests
{
    private readonly ExtensionManagerModal _modal = new(
    [
        CreateExtension("system:sample.system", ExtensionTier.System, desiredEnabled: true, isActive: true),
        CreateExtension("user:sample.user", ExtensionTier.User, desiredEnabled: false, isActive: false, hasOverride: true),
        CreateExtension("workspace:sample.workspace", ExtensionTier.Workspace, desiredEnabled: true, isActive: false, loadError: "boom"),
    ]);
    private readonly Buffer _buffer = new(80, 24);
    private readonly Rect _area = new(0, 0, 80, 24);

    private static KeyEvent Char(char c) => new(Key.Unknown, Modifiers.None, c);
    private static KeyEvent Named(Key key) => new(key, Modifiers.None, null);

    [Fact]
    public void Filter_NarrowsListByName()
    {
        _modal.HandleKey(Char('w'));
        _modal.HandleKey(Char('o'));
        _modal.HandleKey(Char('r'));
        _modal.HandleKey(Char('k'));

        var filtered = Assert.Single(_modal.FilteredExtensions);
        Assert.Equal("workspace:sample.workspace", filtered.Id);
    }

    [Fact]
    public void Render_ShowsTierAndStatusMetadata()
    {
        _modal.Render(_buffer, _area);

        Assert.Contains(ReadAllRows(), row => row.Contains("SYS  enabled"));
        Assert.Contains(ReadAllRows(), row => row.Contains("USR  disabled"));
        Assert.Contains(ReadAllRows(), row => row.Contains("WRK  error"));
    }

    [Fact]
    public void ArrowKeys_MoveSelection()
    {
        _modal.HandleKey(Named(Key.Down));

        Assert.Equal("user:sample.user", _modal.SelectedExtension!.Id);
    }

    [Fact]
    public void Delete_WhenSelectedExtensionHasOverride_RequestsReset()
    {
        _modal.HandleKey(Named(Key.Down));

        var consumed = _modal.HandleKey(Named(Key.Delete));

        Assert.False(consumed);
        Assert.Equal(ExtensionManagerActionKind.Reset, _modal.RequestedAction!.Value.Kind);
        Assert.Equal("user:sample.user", _modal.RequestedAction!.Value.ExtensionId);
    }

    [Fact]
    public void Enter_WhenWorkspaceEnableNeedsConfirmation_WaitsForSecondConfirm()
    {
        var modal = new ExtensionManagerModal(
        [
            CreateExtension("workspace:sample.workspace", ExtensionTier.Workspace, desiredEnabled: false, isActive: false),
        ]);

        var firstConsumed = modal.HandleKey(Named(Key.Enter));

        Assert.True(firstConsumed);
        Assert.True(modal.IsAwaitingWorkspaceEnableConfirmation);
        Assert.Null(modal.RequestedAction);

        var secondConsumed = modal.HandleKey(Named(Key.Enter));

        Assert.False(secondConsumed);
        Assert.Equal(ExtensionManagerActionKind.SetEnabled, modal.RequestedAction!.Value.Kind);
        Assert.True(modal.RequestedAction!.Value.Enabled);
    }

    [Fact]
    public void MutationCommands_AreBlockedDuringActiveTurns()
    {
        _modal.IsMutationBlocked = true;

        var consumed = _modal.HandleKey(Named(Key.Enter));

        Assert.True(consumed);
        Assert.Null(_modal.RequestedAction);
        Assert.Equal("Read-only while a turn is running.", _modal.FeedbackMessage);
    }

    private IEnumerable<string> ReadAllRows()
    {
        for (var y = 0; y < _buffer.Height; y++)
            yield return ReadRow(y);
    }

    private string ReadRow(int y)
    {
        var chars = new char[_buffer.Width];
        for (var i = 0; i < _buffer.Width; i++)
            chars[i] = _buffer.Get(i, y).Char;

        return new string(chars).TrimEnd();
    }

    private static UrExtensionInfo CreateExtension(
        string id,
        ExtensionTier tier,
        bool desiredEnabled,
        bool isActive,
        bool hasOverride = false,
        string? loadError = null)
    {
        var name = id[(id.IndexOf(':') + 1)..];
        var defaultEnabled = tier is not ExtensionTier.Workspace;
        return new UrExtensionInfo(
            id,
            name,
            tier,
            $"{name} description",
            "1.0.0",
            defaultEnabled,
            desiredEnabled,
            isActive,
            hasOverride,
            loadError);
    }
}
