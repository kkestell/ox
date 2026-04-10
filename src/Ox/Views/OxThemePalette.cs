namespace Ox.Views;

/// <summary>
/// The declarative color contract for Ox chrome.
///
/// Keeping the palette in a Terminal.Gui-free type lets unit tests lock in the
/// intended app colors without depending on the UI runtime, which currently
/// does not load cleanly inside the test host.
/// </summary>
internal static class OxThemePalette
{
    /// <summary>
    /// Ox uses the same white-on-black treatment for both normal text and
    /// editable controls so the composer does not fall back to Terminal.Gui's
    /// default gray editable background.
    /// </summary>
    public static readonly OxThemeScheme Ox = new(
        NormalForeground: OxThemeColor.White,
        NormalBackground: OxThemeColor.Black,
        EditableForeground: OxThemeColor.White,
        EditableBackground: OxThemeColor.Black);
}

internal readonly record struct OxThemeScheme(
    OxThemeColor NormalForeground,
    OxThemeColor NormalBackground,
    OxThemeColor EditableForeground,
    OxThemeColor EditableBackground);

internal enum OxThemeColor
{
    Black,
    White
}
