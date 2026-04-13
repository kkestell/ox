using Ox.Views;
using Te.Rendering;

namespace Ur.Tests;

/// <summary>
/// Layout regressions for the custom conversation renderer.
///
/// These tests target the pure text-layout core because the regression is about
/// segment composition rather than any particular UI framework surface.
/// </summary>
public sealed class ConversationViewLayoutTests
{
    [Fact]
    public void LayoutSegments_NewlinePrefixedSegments_DoesNotInsertSpacerRows()
    {
        var lines = ConversationTextLayout.LayoutSegments(
            [
                new LayoutFragment<string>("Read(\"Makefile\")", "text"),
                new LayoutFragment<string>("\n└─ .PHONY: format-docs inspect install", "text"),
                new LayoutFragment<string>("\n   install:", "text"),
                new LayoutFragment<string>("\n   @./scripts/install.sh", "text")
            ],
            width: 78)
            .Select(static line => string.Concat(line.Select(fragment => fragment.Text)))
            .ToList();

        Assert.Equal(
        [
            "Read(\"Makefile\")",
            "└─ .PHONY: format-docs inspect install",
            "   install:",
            "   @./scripts/install.sh"
        ],
        lines);
    }

    [Fact]
    public void LayoutSegments_ConsecutiveNewlines_PreservesIntentionalBlankLine()
    {
        var lines = ConversationTextLayout.LayoutSegments(
            [
                new LayoutFragment<string>("A", "text"),
                new LayoutFragment<string>("\n", "text"),
                new LayoutFragment<string>("\nB", "text")
            ],
            width: 20)
            .Select(static line => string.Concat(line.Select(fragment => fragment.Text)))
            .ToList();

        Assert.Equal(["A", "", "B"], lines);
    }

    [Fact]
    public void Render_WhenScrollbarVisible_UsesLightThumbAndDarkTrack()
    {
        var palette = OxThemePalette.Ox;
        var view = new ConversationView();
        var buffer = new ConsoleBuffer(20, 8);

        for (var i = 0; i < 12; i++)
            view.AddEntry(new Ox.Conversation.UserMessageEntry($"message {i}"));

        view.Render(buffer, x: 0, y: 0, width: 20, height: 8);

        Assert.Equal(Cell.Empty, buffer.GetCell(19, 0));

        Assert.Equal('│', buffer.GetCell(19, 1).Rune);
        Assert.Equal(palette.Border, buffer.GetCell(19, 1).Foreground);

        Assert.Equal('│', buffer.GetCell(19, 6).Rune);
        Assert.Equal(palette.Divider, buffer.GetCell(19, 6).Foreground);

        Assert.Equal(Cell.Empty, buffer.GetCell(19, 7));
    }

    [Fact]
    public void Render_AddsOneRowOfPaddingAboveAndBelowConversationList()
    {
        var view = new ConversationView();
        var buffer = new ConsoleBuffer(20, 8);
        view.AddEntry(new Ox.Conversation.UserMessageEntry("hello"));

        view.Render(buffer, x: 0, y: 0, width: 20, height: 8);

        Assert.Equal(Cell.Empty, buffer.GetCell(1, 0));
        Assert.Equal('●', buffer.GetCell(1, 1).Rune);
        Assert.Equal(Cell.Empty, buffer.GetCell(1, 7));
    }

    [Fact]
    public void Render_NewlineOnlyAssistantEntry_DoesNotRenderEmptyBubble()
    {
        // Regression: when an LLM streams a newline-only chunk between tool calls,
        // OxApp creates an AssistantTextEntry with text "\n". The old guard only
        // checked Text.Length == 0, so "\n" (length 1) slipped through and rendered
        // as a lone ● with no text.
        var view = new ConversationView();
        var buffer = new ConsoleBuffer(30, 8);

        view.AddEntry(new Ox.Conversation.UserMessageEntry("hello"));

        var newlineOnly = new Ox.Conversation.AssistantTextEntry();
        newlineOnly.Append("\n");
        view.AddEntry(newlineOnly);

        view.Render(buffer, x: 0, y: 0, width: 30, height: 8);

        // Row 1 (y=1): the user message circle.
        Assert.Equal('●', buffer.GetCell(1, 1).Rune);
        // Row 2 (y=2) onward should be empty — no second circle for the newline-only entry.
        Assert.Equal(Cell.Empty, buffer.GetCell(1, 2));
        Assert.Equal(Cell.Empty, buffer.GetCell(1, 3));
    }
}
