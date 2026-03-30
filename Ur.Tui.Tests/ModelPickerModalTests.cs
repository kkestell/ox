using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Ur.Tui.Components;
using Ur.Tui.Dummy;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Tests;

public class ModelPickerModalTests
{
    private static readonly List<DummyModelInfo> TestModels =
    [
        new("anthropic/claude-sonnet-4-6", "Claude Sonnet 4.6", 200_000, 0.000003m, 0.000015m),
        new("anthropic/claude-opus-4-6", "Claude Opus 4.6", 200_000, 0.000015m, 0.000075m),
        new("openai/gpt-4o", "GPT-4o", 128_000, 0.0000025m, 0.00001m),
        new("openai/gpt-4o-mini", "GPT-4o Mini", 128_000, 0.00000015m, 0.0000006m),
        new("google/gemini-2.5-pro", "Gemini 2.5 Pro", 1_000_000, 0.00000125m, 0.00001m),
    ];

    private readonly ModelPickerModal _modal = new(TestModels);
    private readonly Buffer _buffer = new(80, 24);
    private readonly Rect _area = new(0, 0, 80, 24);

    private static KeyEvent Char(char c) => new(Key.Unknown, Modifiers.None, c);
    private static KeyEvent Named(Key key) => new(key, Modifiers.None, null);

    private string ReadRow(int y, int startX, int width)
    {
        var chars = new char[width];
        for (var i = 0; i < width; i++)
            chars[i] = _buffer.Get(startX + i, y).Char;
        return new string(chars).TrimEnd();
    }

    [Fact]
    public void Render_ShowsModelList()
    {
        _modal.Render(_buffer, _area);

        // Find model names in the rendered buffer
        var foundClaude = false;
        var foundGpt = false;
        for (var y = 0; y < _buffer.Height; y++)
        {
            var row = ReadRow(y, 0, _buffer.Width);
            if (row.Contains("Claude Sonnet 4.6")) foundClaude = true;
            if (row.Contains("GPT-4o")) foundGpt = true;
        }
        Assert.True(foundClaude, "Should show Claude Sonnet in list");
        Assert.True(foundGpt, "Should show GPT-4o in list");
    }

    [Fact]
    public void Filter_NarrowsList()
    {
        _modal.HandleKey(Char('c'));
        _modal.HandleKey(Char('l'));
        _modal.HandleKey(Char('a'));
        _modal.HandleKey(Char('u'));
        _modal.HandleKey(Char('d'));
        _modal.HandleKey(Char('e'));

        Assert.Equal(2, _modal.FilteredModels.Count);
        Assert.All(_modal.FilteredModels, m => Assert.Contains("Claude", m.Name));
    }

    [Fact]
    public void ArrowKeys_MoveSelection()
    {
        // Initially at index 0
        _modal.HandleKey(Named(Key.Down));
        _modal.HandleKey(Named(Key.Enter));

        Assert.True(_modal.Submitted);
        Assert.Equal("Claude Opus 4.6", _modal.SelectedModel!.Name);
    }

    [Fact]
    public void Enter_SetsSelectedModel()
    {
        var consumed = _modal.HandleKey(Named(Key.Enter));

        Assert.False(consumed);
        Assert.True(_modal.Submitted);
        Assert.Equal("Claude Sonnet 4.6", _modal.SelectedModel!.Name);
    }

    [Fact]
    public void Escape_SetsDismissed()
    {
        var consumed = _modal.HandleKey(Named(Key.Escape));

        Assert.False(consumed);
        Assert.True(_modal.Dismissed);
    }

    [Fact]
    public void Filter_ResetsSelection()
    {
        // Move selection down
        _modal.HandleKey(Named(Key.Down));
        _modal.HandleKey(Named(Key.Down));

        // Type a filter — selection should reset to 0
        _modal.HandleKey(Char('g'));

        _modal.HandleKey(Named(Key.Enter));
        Assert.True(_modal.Submitted);
        // After filtering for 'g', first match should be selected
        Assert.Equal("GPT-4o", _modal.SelectedModel!.Name);
    }

    [Fact]
    public void Filter_ByModelId()
    {
        _modal.HandleKey(Char('a'));
        _modal.HandleKey(Char('n'));
        _modal.HandleKey(Char('t'));
        _modal.HandleKey(Char('h'));
        _modal.HandleKey(Char('r'));
        _modal.HandleKey(Char('o'));
        _modal.HandleKey(Char('p'));
        _modal.HandleKey(Char('i'));
        _modal.HandleKey(Char('c'));

        // Should match by ID prefix "anthropic/"
        Assert.Equal(2, _modal.FilteredModels.Count);
    }

    [Fact]
    public void Backspace_RemovesFilterChar()
    {
        _modal.HandleKey(Char('x'));
        _modal.HandleKey(Char('y'));
        Assert.Empty(_modal.FilteredModels); // no match for "xy"

        _modal.HandleKey(Named(Key.Backspace));
        _modal.HandleKey(Named(Key.Backspace));
        Assert.Equal(5, _modal.FilteredModels.Count); // filter cleared
    }

    [Fact]
    public void ArrowUp_AtTop_StaysAtZero()
    {
        _modal.HandleKey(Named(Key.Up)); // Already at 0
        _modal.HandleKey(Named(Key.Enter));

        Assert.Equal("Claude Sonnet 4.6", _modal.SelectedModel!.Name);
    }

    [Fact]
    public void ArrowDown_AtBottom_StaysAtLast()
    {
        for (var i = 0; i < 10; i++) // More than the 5 models
            _modal.HandleKey(Named(Key.Down));

        _modal.HandleKey(Named(Key.Enter));
        Assert.Equal("Gemini 2.5 Pro", _modal.SelectedModel!.Name);
    }
}
