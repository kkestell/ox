using Ox.Views;

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
}
