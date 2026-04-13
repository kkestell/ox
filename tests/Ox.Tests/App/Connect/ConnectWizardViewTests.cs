using Ox.App.Connect;
using Ox.App.Views;
using Ox.Terminal.Rendering;

namespace Ox.Tests.App.Connect;

/// <summary>
/// Rendering checks for the connect wizard chrome.
/// These tests lock in the flat monochrome modal treatment so the setup flow
/// matches the rest of the TUI's black-background box styling.
/// </summary>
public sealed class ConnectWizardViewTests
{
    private static readonly IReadOnlyList<(string Key, string Name)> Providers =
    [
        ("google", "Google"),
        ("ollama", "Ollama"),
        ("openai", "OpenAI"),
        ("openrouter", "OpenRouter"),
        ("zai-coding", "Z.AI"),
    ];

    private static readonly IReadOnlyList<(string Id, string Name)> Models =
    [
        ("gemini-pro", "Gemini Pro"),
        ("gemini-flash", "Gemini Flash"),
    ];

    [Fact]
    public void Render_ListMode_UsesSquareBorderAndBlackBodyWithoutShadow()
    {
        var palette = OxThemePalette.Ox;
        var buffer = new ConsoleBuffer(50, 14);
        var wizard = new ConnectWizardController();
        wizard.Start(Providers, required: false);

        new ConnectWizardView().Render(buffer, wizard);

        AssertCell(buffer, 9, 2, '┌', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 40, 2, '┐', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 9, 4, '├', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 40, 4, '┤', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 9, 10, '└', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 40, 10, '┘', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 12, 3, 'S', palette.Text, palette.Background);
        AssertCell(buffer, 11, 5, '>', palette.Text, palette.Background);
        AssertCell(buffer, 13, 9, 'Z', palette.StatusText, palette.Background);

        Assert.Equal(Cell.Empty, buffer.GetCell(41, 3));
        Assert.Equal(Cell.Empty, buffer.GetCell(10, 11));
    }

    [Fact]
    public void Render_InputMode_UsesBlackBodyAndSquareBorder()
    {
        var palette = OxThemePalette.Ox;
        var buffer = new ConsoleBuffer(50, 12);
        var wizard = new ConnectWizardController();
        wizard.Start(Providers, required: false);
        wizard.ProviderConfirmed("google", requiresApiKey: true, Models);
        wizard.KeyEditor.SetText("secret");

        new ConnectWizardView().Render(buffer, wizard);

        AssertCell(buffer, 9, 3, '┌', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 12, 4, 'A', palette.Text, palette.Background);
        AssertCell(buffer, 9, 5, '├', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 12, 6, 's', palette.Text, palette.Background);
        AssertCell(buffer, 40, 7, '┘', palette.ChromeBorder, palette.Background);
        Assert.Equal(Cell.Empty, buffer.GetCell(41, 6));
    }

    [Fact]
    public void Render_InputMode_WithStoredApiKeyMask_ShowsMaskedPlaceholder()
    {
        var palette = OxThemePalette.Ox;
        var buffer = new ConsoleBuffer(50, 12);
        var wizard = new ConnectWizardController();
        wizard.Start(Providers, required: false);
        wizard.ProviderConfirmed("google", requiresApiKey: true, Models, hasStoredApiKey: true);

        new ConnectWizardView().Render(buffer, wizard);

        AssertCell(buffer, 12, 6, '*', palette.Text, palette.Background);
        AssertCell(buffer, 28, 6, '*', palette.Text, palette.Background);
    }

    private static void AssertCell(ConsoleBuffer buffer, int x, int y, char rune, Color foreground, Color background)
    {
        var cell = buffer.GetCell(x, y);
        Assert.Equal(rune, cell.Rune);
        Assert.Equal(foreground, cell.Foreground);
        Assert.Equal(background, cell.Background);
    }
}
