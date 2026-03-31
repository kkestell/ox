using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Ur.Terminal.Rendering;
using Ur.Tui;
using Ur.Tui.Components;
using Ur.Tui.State;

namespace Ur.Tui.Tests;

public class ChatAppTests
{
    private readonly TestChatBackend _backend = new();
    private readonly Compositor _compositor;
    private readonly Layer _baseLayer;
    private readonly Layer _overlayLayer;
    private readonly ChatApp _app;

    public ChatAppTests()
    {
        _compositor = new Compositor(80, 24);
        _baseLayer = new Layer(0, 0, 80, 24);
        _overlayLayer = new Layer(0, 0, 80, 24);
        _compositor.AddLayer(_baseLayer);
        _compositor.AddLayer(_overlayLayer);
        _app = new ChatApp(_backend, _compositor, _baseLayer, _overlayLayer);
    }

    private static KeyEvent Char(char c) => new(Key.Unknown, Modifiers.None, c);
    private static KeyEvent Named(Key key) => new(key, Modifiers.None, null);
    private static KeyEvent CtrlC() => new(Key.C, Modifiers.Ctrl, null);
    private static KeyEvent Parsed(params byte[] input)
    {
        var result = KeyParser.Parse(input, out var consumed);

        Assert.NotNull(result);
        Assert.Equal(input.Length, consumed);

        return result.Value;
    }

    private async Task<bool> Frame(params KeyEvent[] keys)
    {
        return await _app.ProcessFrame(keys);
    }

    /// <summary>
    /// Walk through the first-run flow: submit API key, select first model.
    /// After this, the app is in chat mode with no active modal.
    /// </summary>
    private async Task SetupChatReady()
    {
        await Frame(); // Constructor already set ApiKeyModal
        // Submit API key
        await Frame(Char('k'), Named(Key.Enter));
        // ModelPickerModal — select first model
        await Frame(Named(Key.Enter));
    }

    [Fact]
    public async Task FirstRun_ShowsApiKeyModal()
    {
        await Frame();
        Assert.IsType<ApiKeyModal>(_app.State.ActiveModal);
    }

    [Fact]
    public async Task AfterApiKey_ShowsModelPicker()
    {
        await Frame();
        await Frame(Char('k'), Char('e'), Char('y'), Named(Key.Enter));

        Assert.IsType<ModelPickerModal>(_app.State.ActiveModal);
    }

    [Fact]
    public async Task AfterModelSelect_EntersChat()
    {
        await SetupChatReady();

        Assert.Null(_app.State.ActiveModal);
        Assert.Contains(_app.State.Messages,
            m => m.Role == MessageRole.System && m.Content.ToString().Contains("Ready"));
    }

    [Fact]
    public async Task SlashQuit_ExitsFalse()
    {
        await SetupChatReady();

        var result = await Frame(Char('/'), Char('q'), Char('u'), Char('i'), Char('t'), Named(Key.Enter));

        Assert.False(result);
    }

    [Fact]
    public async Task SlashModel_OpensModelPicker()
    {
        await SetupChatReady();

        await Frame(Char('/'), Char('m'), Char('o'), Char('d'), Char('e'), Char('l'), Named(Key.Enter));

        Assert.IsType<ModelPickerModal>(_app.State.ActiveModal);
    }

    [Fact]
    public async Task SlashExtensions_OpensExtensionManager()
    {
        await SetupChatReady();

        await Frame(Char('/'), Char('e'), Char('x'), Char('t'), Char('e'), Char('n'), Char('s'), Char('i'), Char('o'), Char('n'), Char('s'), Named(Key.Enter));

        Assert.IsType<ExtensionManagerModal>(_app.State.ActiveModal);
    }

    [Fact]
    public async Task SubmitMessage_AddsToChatState()
    {
        await SetupChatReady();

        await Frame(Char('h'), Char('e'), Char('l'), Char('l'), Char('o'), Named(Key.Enter));

        Assert.Contains(_app.State.Messages,
            m => m.Role == MessageRole.User && m.Content.ToString() == "hello");
        Assert.Contains(_app.State.Messages,
            m => m.Role == MessageRole.Assistant && m.IsStreaming);
    }

    [Fact]
    public async Task CtrlC_AtIdle_ExitsFalse()
    {
        await SetupChatReady();

        var result = await Frame(CtrlC());

        Assert.False(result);
    }

    [Fact]
    public async Task CtrlC_DuringTurn_CancelsTurn()
    {
        await SetupChatReady();

        // Submit a message to start a turn
        await Frame(Char('h'), Char('i'), Named(Key.Enter));
        Assert.True(_app.State.IsTurnRunning);

        // Ctrl+C should cancel the turn, not exit
        var result = await Frame(CtrlC());
        Assert.True(result);

        // Wait for cancellation to propagate, then drain repeatedly
        for (var i = 0; i < 10 && _app.State.IsTurnRunning; i++)
        {
            Thread.Sleep(50);
            await Frame();
        }

        Assert.False(_app.State.IsTurnRunning);
    }

    [Fact]
    public async Task EscDuringFirstRun_ApiKeyModal_Exits()
    {
        await Frame(); // ApiKeyModal shown

        var result = await Frame(Named(Key.Escape));

        Assert.False(result);
    }

    [Fact]
    public async Task EscDuringChat_ModelPicker_Dismisses()
    {
        await SetupChatReady();

        // Open model picker via /model
        await Frame(Char('/'), Char('m'), Char('o'), Char('d'), Char('e'), Char('l'), Named(Key.Enter));
        Assert.IsType<ModelPickerModal>(_app.State.ActiveModal);

        // Esc should dismiss, not exit
        var result = await Frame(Named(Key.Escape));
        Assert.True(result);
        Assert.Null(_app.State.ActiveModal);
    }

    [Fact]
    public async Task EscDuringChat_ExtensionManager_Dismisses()
    {
        await SetupChatReady();
        await Frame(Char('/'), Char('e'), Char('x'), Char('t'), Char('e'), Char('n'), Char('s'), Char('i'), Char('o'), Char('n'), Char('s'), Named(Key.Enter));

        var result = await Frame(Named(Key.Escape));

        Assert.True(result);
        Assert.Null(_app.State.ActiveModal);
    }

    [Fact]
    public async Task KittyDownArrow_MovesModelSelectionEndToEnd()
    {
        await Frame();
        await Frame(Char('k'), Named(Key.Enter));

        var result = await Frame(Parsed(0x1B, 0x5B, 0x31, 0x3B, 0x31, 0x42), Named(Key.Enter));

        Assert.True(result);
        Assert.Null(_app.State.ActiveModal);
        Assert.Equal("anthropic/claude-opus-4-6", _backend.SelectedModelId);
    }

    [Fact]
    public async Task KittyReleaseEvent_IsIgnoredByChatApp()
    {
        await Frame();
        await Frame(Char('k'), Named(Key.Enter));

        var result = await Frame(Parsed(0x1B, 0x5B, 0x31, 0x3B, 0x31, 0x3A, 0x33, 0x42), Named(Key.Enter));

        Assert.True(result);
        Assert.Null(_app.State.ActiveModal);
        Assert.Equal("anthropic/claude-sonnet-4-6", _backend.SelectedModelId);
    }

    [Fact]
    public async Task UnknownSlashCommand_ShowsErrorMessage()
    {
        await SetupChatReady();

        await Frame(Char('/'), Char('f'), Char('o'), Char('o'), Named(Key.Enter));

        Assert.Contains(_app.State.Messages,
            m => m.Role == MessageRole.System && m.Content.ToString().Contains("Unknown command"));
    }

    [Fact]
    public async Task ExtensionsModal_ToggleUserOrSystemExtension_UpdatesVisibleState()
    {
        await SetupChatReady();
        await Frame(Char('/'), Char('e'), Char('x'), Char('t'), Char('e'), Char('n'), Char('s'), Char('i'), Char('o'), Char('n'), Char('s'), Named(Key.Enter));

        var modal = Assert.IsType<ExtensionManagerModal>(_app.State.ActiveModal);
        Assert.Equal("system:sample.system", modal.SelectedExtension!.Id);

        await Frame(Named(Key.Enter));

        modal = Assert.IsType<ExtensionManagerModal>(_app.State.ActiveModal);
        Assert.Equal(1, _backend.SetExtensionEnabledCallCount);
        Assert.False(modal.SelectedExtension!.DesiredEnabled);
        Assert.Contains(_app.State.Messages, message => message.Content.ToString().Contains("Disabled sample.system."));
    }

    [Fact]
    public async Task ExtensionsModal_EnablingWorkspaceExtension_RequiresConfirmation()
    {
        await SetupChatReady();
        await Frame(Char('/'), Char('e'), Char('x'), Char('t'), Char('e'), Char('n'), Char('s'), Char('i'), Char('o'), Char('n'), Char('s'), Named(Key.Enter));
        await Frame(Named(Key.Down), Named(Key.Down));

        var modal = Assert.IsType<ExtensionManagerModal>(_app.State.ActiveModal);
        Assert.Equal("workspace:sample.workspace", modal.SelectedExtension!.Id);

        await Frame(Named(Key.Enter));

        modal = Assert.IsType<ExtensionManagerModal>(_app.State.ActiveModal);
        Assert.True(modal.IsAwaitingWorkspaceEnableConfirmation);
        Assert.Equal(0, _backend.SetExtensionEnabledCallCount);

        await Frame(Named(Key.Enter));

        modal = Assert.IsType<ExtensionManagerModal>(_app.State.ActiveModal);
        Assert.Equal(1, _backend.SetExtensionEnabledCallCount);
        Assert.True(modal.SelectedExtension!.DesiredEnabled);
        Assert.True(modal.SelectedExtension.IsActive);
    }

    [Fact]
    public async Task ExtensionsModal_ActivationFailure_SurfacesErrorMessage()
    {
        _backend.ActivationFailureMessage = "activation failed";
        await SetupChatReady();
        await Frame(Char('/'), Char('e'), Char('x'), Char('t'), Char('e'), Char('n'), Char('s'), Char('i'), Char('o'), Char('n'), Char('s'), Named(Key.Enter));
        await Frame(Named(Key.Down), Named(Key.Down));
        await Frame(Named(Key.Enter));

        await Frame(Named(Key.Enter));

        var modal = Assert.IsType<ExtensionManagerModal>(_app.State.ActiveModal);
        Assert.Equal("activation failed", modal.SelectedExtension!.LoadError);
        Assert.Contains(_app.State.Messages, message => message.IsError && message.Content.ToString().Contains("Failed to activate sample.workspace"));
    }

    [Fact]
    public async Task ExtensionsModal_TogglingWhileTurnIsRunning_IsBlocked()
    {
        await SetupChatReady();
        await Frame(Char('h'), Char('i'), Named(Key.Enter));
        Assert.True(_app.State.IsTurnRunning);

        await Frame(Char('/'), Char('e'), Char('x'), Char('t'), Char('e'), Char('n'), Char('s'), Char('i'), Char('o'), Char('n'), Char('s'), Named(Key.Enter));
        var modal = Assert.IsType<ExtensionManagerModal>(_app.State.ActiveModal);
        Assert.True(modal.IsMutationBlocked);

        await Frame(Named(Key.Enter));

        modal = Assert.IsType<ExtensionManagerModal>(_app.State.ActiveModal);
        Assert.Equal(0, _backend.SetExtensionEnabledCallCount);
        Assert.Equal("Read-only while a turn is running.", modal.FeedbackMessage);

        for (var i = 0; i < 10 && _app.State.IsTurnRunning; i++)
        {
            Thread.Sleep(50);
            await Frame();
        }
    }
}
