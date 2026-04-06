namespace Ur.Tui.Rendering;

/// <summary>
/// SGR style attributes that can be combined on a single cell.
/// Terminal.Flush converts these flags to the corresponding SGR parameters (1, 2, 3, 4, 7).
/// </summary>
[Flags]
internal enum CellStyle
{
    None      = 0,
    Bold      = 1,  // SGR 1
    Dim       = 2,  // SGR 2
    Italic    = 4,  // SGR 3
    Underline = 8,  // SGR 4
    Reverse   = 16, // SGR 7
}
