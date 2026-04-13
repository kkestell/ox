using Ox.App.Input;
using Ox.Terminal.Input;

namespace Ox.Tests.App.Input;

/// <summary>
/// Unit tests for <see cref="ChatInputMode"/>. Each test pokes a single key at
/// a fresh mode and asserts the callback / editor effect — there is no
/// coupling to <see cref="OxApp"/>, which was the whole point of splitting
/// these behaviors out of the god class.
/// </summary>
public sealed class ChatInputModeTests
{
    private sealed class Hooks
    {
        public bool ExitRequested;
        public bool CancelledTurn;
        public bool AutocompleteApplied;
        public int ScreenDumpCount;
        public int SubmitCount;

        public ChatInputMode Build(TextEditor editor, bool turnActive = false) => new(
            editor,
            turnActive: () => turnActive,
            requestExit: () => ExitRequested = true,
            cancelTurn: () => CancelledTurn = true,
            onScreenDumpShortcut: _ => ScreenDumpCount++,
            tryApplyAutocomplete: () => AutocompleteApplied = true,
            validateAndTake: () => { SubmitCount++; return string.Empty; });
    }

    private static KeyEventArgs Key(KeyCode code, char ch = '\0') =>
        new(code, ch);

    [Fact]
    public void CtrlC_RequestsExit()
    {
        var hooks = new Hooks();
        var mode = hooks.Build(new TextEditor());

        mode.HandleKey(Key(KeyCode.C | KeyCode.CtrlMask));

        Assert.True(hooks.ExitRequested);
    }

    [Fact]
    public void CtrlD_EmptyBuffer_RequestsExit()
    {
        var hooks = new Hooks();
        var editor = new TextEditor();
        var mode = hooks.Build(editor);

        mode.HandleKey(Key(KeyCode.D | KeyCode.CtrlMask));

        Assert.True(hooks.ExitRequested);
    }

    [Fact]
    public void CtrlD_WithText_DoesNotExit()
    {
        var hooks = new Hooks();
        var editor = new TextEditor();
        editor.InsertChar('a');
        var mode = hooks.Build(editor);

        mode.HandleKey(Key(KeyCode.D | KeyCode.CtrlMask));

        Assert.False(hooks.ExitRequested);
    }

    [Fact]
    public void Esc_DuringTurn_CancelsTurn()
    {
        var hooks = new Hooks();
        var mode = hooks.Build(new TextEditor(), turnActive: true);

        mode.HandleKey(Key(KeyCode.Esc));

        Assert.True(hooks.CancelledTurn);
    }

    [Fact]
    public void Esc_WithoutTurn_DoesNotCancel()
    {
        var hooks = new Hooks();
        var mode = hooks.Build(new TextEditor(), turnActive: false);

        mode.HandleKey(Key(KeyCode.Esc));

        Assert.False(hooks.CancelledTurn);
    }

    [Fact]
    public void Tab_AppliesAutocomplete()
    {
        var hooks = new Hooks();
        var mode = hooks.Build(new TextEditor());

        mode.HandleKey(Key(KeyCode.Tab));

        Assert.True(hooks.AutocompleteApplied);
    }

    [Fact]
    public void Enter_RunsSubmitPipeline()
    {
        var hooks = new Hooks();
        var mode = hooks.Build(new TextEditor());

        mode.HandleKey(Key(KeyCode.Enter));

        Assert.Equal(1, hooks.SubmitCount);
    }

    [Fact]
    public void PrintableChar_IsInserted()
    {
        var hooks = new Hooks();
        var editor = new TextEditor();
        var mode = hooks.Build(editor);

        mode.HandleKey(Key(KeyCode.A, 'a'));

        Assert.Equal("a", editor.Text);
    }

    [Fact]
    public void Backspace_DeletesPrevChar()
    {
        var hooks = new Hooks();
        var editor = new TextEditor();
        editor.InsertChar('a');
        editor.InsertChar('b');
        var mode = hooks.Build(editor);

        mode.HandleKey(Key(KeyCode.Backspace));

        Assert.Equal("a", editor.Text);
    }
}
