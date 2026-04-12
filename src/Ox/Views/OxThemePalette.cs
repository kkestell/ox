using Te.Rendering;

namespace Ox.Views;

/// <summary>
/// Named color slots for the Ox color scheme. Maps semantic roles to Te
/// <see cref="Color"/> values so that view code never hard-codes colors.
/// </summary>
public enum OxThemeColor
{
    Black,
    White,
    Blue,
    Yellow,
    Green,
    Red,
    BrightBlack,
}

/// <summary>
/// Concrete color palette for the Ox TUI. Provides both Te <see cref="Color"/>
/// values for rendering and <see cref="OxThemeColor"/> enum values for tests
/// that don't want a dependency on Te's color types.
///
/// The palette is a singleton: <see cref="Ox"/> is the only instance. Views
/// read from it to stay consistent with the §11 color scheme.
/// </summary>
public sealed class OxThemePalette
{
    /// <summary>The single Ox theme instance.</summary>
    public static readonly OxThemePalette Ox = new();

    // ── Te Color values for rendering ────────────────────────────────

    /// <summary>Black terminal background.</summary>
    public Color Background { get; } = Color.Black;

    /// <summary>
    /// Shared panel surface color for the composer and modal interiors.
    /// Keeping this in the palette prevents the different floating surfaces
    /// from drifting apart as we tune the chrome.
    /// </summary>
    public Color Surface { get; } = Color.FromIndex(234);

    /// <summary>White foreground for normal text, assistant text, active throbber bits.</summary>
    public Color Text { get; } = Color.White;

    /// <summary>Blue circle prefix for user messages.</summary>
    public Color UserCircle { get; } = Color.Blue;

    /// <summary>Dark gray for tool signatures, tool results, splash logo, inactive throbber bits.</summary>
    public Color ToolSignature { get; } = Color.BrightBlack;

    /// <summary>Yellow circle for tool started / awaiting approval.</summary>
    public Color ToolCircleStarted { get; } = Color.Yellow;

    /// <summary>Green circle for tool completed successfully.</summary>
    public Color ToolCircleSuccess { get; } = Color.Green;

    /// <summary>Red circle for tool errors and error entry text.</summary>
    public Color ToolCircleError { get; } = Color.Red;

    /// <summary>Dark gray for splash logo.</summary>
    public Color SplashLogo { get; } = Color.BrightBlack;

    /// <summary>
    /// Border tone for floating chrome. This is lighter than the shadow while
    /// still dark enough to preserve the low-contrast Ox look.
    /// </summary>
    public Color Border { get; } = Color.FromIndex(234);

    /// <summary>
    /// The chat composer uses the same block-border tone as the modal so the
    /// surfaces feel like one visual system.
     /// </summary>
    public Color InputBorder { get; } = Color.FromIndex(234);

    /// <summary>Darker gray for internal dividers (Color256 index 240).</summary>
    public Color Divider { get; } = Color.FromIndex(240);

    /// <summary>Gray for status line text (Color256 index 245).</summary>
    public Color StatusText { get; } = Color.FromIndex(245);

    /// <summary>
    /// Shared gray border for the monochrome approval and composer chrome.
    /// Keeping this separate from the modal border lets those surfaces sit on
    /// a pure-black background without forcing the rest of the UI to match.
    /// </summary>
    public Color ChromeBorder { get; } = Color.BrightBlack;

    /// <summary>
    /// Darker shadow tone that sits just below the chrome in value so the
    /// offset cast reads without overpowering the panel itself.
    /// </summary>
    public Color Shadow { get; } = Color.FromIndex(233);

    /// <summary>White for active throbber bits.</summary>
    public Color ThrobberActive { get; } = Color.White;

    /// <summary>Dark gray for inactive throbber bits.</summary>
    public Color ThrobberInactive { get; } = Color.BrightBlack;

    // ── OxThemeColor enum values for test assertions ─────────────────

    /// <summary>Foreground color for editable text fields (white text).</summary>
    public OxThemeColor EditableForeground { get; } = OxThemeColor.White;

    /// <summary>Background color for editable text fields (black background).</summary>
    public OxThemeColor EditableBackground { get; } = OxThemeColor.Black;
}
