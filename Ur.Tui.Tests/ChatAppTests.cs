using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Ur.Terminal.Rendering;
using Ur.Tui.Components;
using Ur.Tui.Dummy;
using Ur.Tui.State;

namespace Ur.Tui.Tests;

public class ChatAppTests
{
    private readonly DummyConfiguration _config = new();
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
        _app = new ChatApp(_config, _compositor, _baseLayer, _overlayLayer);
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

    private bool Frame(params KeyEvent[] keys)
    {
        return _app.ProcessFrame(keys.AsSpan());
    }

    /// <summary>
    /// Walk through the first-run flow: submit API key, select first model.
    /// After this, the app is in chat mode with no active modal.
    /// </summary>
    private void SetupChatReady()
    {
        Frame(); // Constructor already set ApiKeyModal
        // Submit API key
        Frame(Char('k'), Named(Key.Enter));
        // ModelPickerModal — select first model
        Frame(Named(Key.Enter));
    }

    [Fact]
    public void FirstRun_ShowsApiKeyModal()
    {
        Frame();
        Assert.IsType<ApiKeyModal>(_app.State.ActiveModal);
    }

    [Fact]
    public void AfterApiKey_ShowsModelPicker()
    {
        Frame();
        Frame(Char('k'), Char('e'), Char('y'), Named(Key.Enter));

        Assert.IsType<ModelPickerModal>(_app.State.ActiveModal);
    }

    [Fact]
    public void AfterModelSelect_EntersChat()
    {
        SetupChatReady();

        Assert.Null(_app.State.ActiveModal);
        Assert.Contains(_app.State.Messages,
            m => m.Role == MessageRole.System && m.Content.ToString().Contains("Ready"));
    }

    [Fact]
    public void SlashQuit_ExitsFalse()
    {
        SetupChatReady();

        var result = Frame(Char('/'), Char('q'), Char('u'), Char('i'), Char('t'), Named(Key.Enter));

        Assert.False(result);
    }

    [Fact]
    public void SlashModel_OpensModelPicker()
    {
        SetupChatReady();

        Frame(Char('/'), Char('m'), Char('o'), Char('d'), Char('e'), Char('l'), Named(Key.Enter));

        Assert.IsType<ModelPickerModal>(_app.State.ActiveModal);
    }

    [Fact]
    public void SubmitMessage_AddsToChatState()
    {
        SetupChatReady();

        Frame(Char('h'), Char('e'), Char('l'), Char('l'), Char('o'), Named(Key.Enter));

        Assert.Contains(_app.State.Messages,
            m => m.Role == MessageRole.User && m.Content.ToString() == "hello");
        Assert.Contains(_app.State.Messages,
            m => m.Role == MessageRole.Assistant && m.IsStreaming);
    }

    [Fact]
    public void CtrlC_AtIdle_ExitsFalse()
    {
        SetupChatReady();

        var result = Frame(CtrlC());

        Assert.False(result);
    }

    [Fact]
    public void CtrlC_DuringTurn_CancelsTurn()
    {
        SetupChatReady();

        // Submit a message to start a turn
        Frame(Char('h'), Char('i'), Named(Key.Enter));
        Assert.True(_app.State.IsTurnRunning);

        // Ctrl+C should cancel the turn, not exit
        var result = Frame(CtrlC());
        Assert.True(result);

        // Wait for cancellation to propagate, then drain repeatedly
        for (var i = 0; i < 10 && _app.State.IsTurnRunning; i++)
        {
            Thread.Sleep(50);
            Frame();
        }

        Assert.False(_app.State.IsTurnRunning);
    }

    [Fact]
    public void EscDuringFirstRun_ApiKeyModal_Exits()
    {
        Frame(); // ApiKeyModal shown

        var result = Frame(Named(Key.Escape));

        Assert.False(result);
    }

    [Fact]
    public void EscDuringChat_ModelPicker_Dismisses()
    {
        SetupChatReady();

        // Open model picker via /model
        Frame(Char('/'), Char('m'), Char('o'), Char('d'), Char('e'), Char('l'), Named(Key.Enter));
        Assert.IsType<ModelPickerModal>(_app.State.ActiveModal);

        // Esc should dismiss, not exit
        var result = Frame(Named(Key.Escape));
        Assert.True(result);
        Assert.Null(_app.State.ActiveModal);
    }

    [Fact]
    public void KittyDownArrow_MovesModelSelectionEndToEnd()
    {
        Frame();
        Frame(Char('k'), Named(Key.Enter));

        var result = Frame(Parsed(0x1B, 0x5B, 0x31, 0x3B, 0x31, 0x42), Named(Key.Enter));

        Assert.True(result);
        Assert.Null(_app.State.ActiveModal);
        Assert.Equal("anthropic/claude-opus-4-6", _config.SelectedModelId);
    }

    [Fact]
    public void KittyReleaseEvent_IsIgnoredByChatApp()
    {
        Frame();
        Frame(Char('k'), Named(Key.Enter));

        var result = Frame(Parsed(0x1B, 0x5B, 0x31, 0x3B, 0x31, 0x3A, 0x33, 0x42), Named(Key.Enter));

        Assert.True(result);
        Assert.Null(_app.State.ActiveModal);
        Assert.Equal("anthropic/claude-sonnet-4-6", _config.SelectedModelId);
    }

    [Fact]
    public void UnknownSlashCommand_ShowsErrorMessage()
    {
        SetupChatReady();

        Frame(Char('/'), Char('f'), Char('o'), Char('o'), Named(Key.Enter));

        Assert.Contains(_app.State.Messages,
            m => m.Role == MessageRole.System && m.Content.ToString().Contains("Unknown command"));
    }
}
