using Ur.Providers;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Ur.Tui.Components;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Tests;

public class ModelPickerModalTests
{
    private static readonly List<ModelInfo> TestModels =
    [
        new("anthropic/claude-sonnet-4-6", "Claude Sonnet 4.6", 200_000, 8_192, 0.000003m, 0.000015m, [], "text->text"),
        new("anthropic/claude-opus-4-6", "Claude Opus 4.6", 200_000, 8_192, 0.000015m, 0.000075m, [], "text->text"),
        new("openai/gpt-4o", "GPT-4o", 128_000, 16_384, 0.0000025m, 0.00001m, [], "text->text"),
        new("openai/gpt-4o-mini", "GPT-4o Mini", 128_000, 16_384, 0.00000015m, 0.0000006m, [], "text->text"),
        new("google/gemini-2.5-pro", "Gemini 2.5 Pro", 1_000_000, 65_536, 0.00000125m, 0.00001m, [], "text->text"),
    ];

    private const int TestWidth = 72;
    private const int TestHeight = 20;

    private readonly ModelPickerModal _modal = new(TestModels);
    private readonly Buffer _buffer = new(TestWidth, TestHeight);
    private readonly Rect _area = new(0, 0, TestWidth, TestHeight);

    private static KeyEvent Char(char c) => new(Key.Unknown, Modifiers.None, c);
    private static KeyEvent Named(Key key) => new(key, Modifiers.None, null);

    private string ReadRow(Buffer buffer, int y, int startX, int width)
    {
        var chars = new char[width];
        for (var i = 0; i < width; i++)
            chars[i] = buffer.Get(startX + i, y).Char;
        return new string(chars).TrimEnd();
    }

    private static List<ModelInfo> BuildManyModels(int count)
    {
        var models = new List<ModelInfo>(count);
        for (var i = 0; i < count; i++)
        {
            models.Add(new ModelInfo(
                $"test/model-{i:D2}",
                $"Model {i:D2}",
                128_000,
                8_192,
                0.000001m,
                0.000002m,
                [],
                "text->text"));
        }

        return models;
    }

    [Fact]
    public void Render_ShowsModelList()
    {
        _modal.Render(_buffer, _area);

        var foundClaude = false;
        var foundGpt = false;
        for (var y = 0; y < _buffer.Height; y++)
        {
            var row = ReadRow(_buffer, y, 0, _buffer.Width);
            if (row.Contains("anthropic/claude-sonnet-4-6")) foundClaude = true;
            if (row.Contains("openai/gpt-4o")) foundGpt = true;
        }
        Assert.True(foundClaude, "Should show anthropic/claude-sonnet-4-6 in list");
        Assert.True(foundGpt, "Should show openai/gpt-4o in list");
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

    [Fact]
    public void Render_ScrolledList_ShowsSelectedItem()
    {
        var modal = new ModelPickerModal(BuildManyModels(count: 15));
        var mw = TestWidth;
        var mh = TestHeight;
        var buffer = new Buffer(mw, mh);
        var modalRect = new Rect(0, 0, mw, mh);

        for (var i = 0; i < 12; i++)
            modal.HandleKey(Named(Key.Down));

        modal.Render(buffer, modalRect);

        var found12 = false;
        for (var y = 0; y < mh; y++)
        {
            var row = ReadRow(buffer, y, 0, mw);
            if (row.Contains("model-12")) found12 = true;
        }
        Assert.True(found12, "Selected item 'model-12' should appear in the list");
    }

    [Fact]
    public void Render_ShowsColumnsAligned()
    {
        _modal.Render(_buffer, _area);

        var rows = new List<string>();
        for (var y = 0; y < _buffer.Height; y++)
        {
            var row = ReadRow(_buffer, y, 0, _buffer.Width);
            if (row.Contains("anthropic/") || row.Contains("openai/") || row.Contains("google/"))
                rows.Add(row);
        }

        Assert.True(rows.Count >= 3, "Should have at least 3 model rows");

        // Verify price columns appear in each row
        foreach (var row in rows)
            Assert.Contains("$", row);

        // Both anthropic models have "200k" context — verify they align at the same column
        var anthropicRows = rows.Where(r => r.Contains("anthropic/")).ToList();
        Assert.True(anthropicRows.Count >= 2);
        var ctxOffset0 = anthropicRows[0].IndexOf("200k", StringComparison.Ordinal);
        var ctxOffset1 = anthropicRows[1].IndexOf("200k", StringComparison.Ordinal);
        Assert.True(ctxOffset0 > 0, "Should contain '200k'");
        Assert.Equal(ctxOffset0, ctxOffset1);
    }
}
