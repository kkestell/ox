namespace Ur.Widgets;

/// <summary>
/// Indicates how a modal dialog was dismissed.
/// Follows the WinForms convention: OK means the user confirmed,
/// Cancel means they backed out (via Cancel button or Escape key).
/// </summary>
public enum DialogResult
{
    OK,
    Cancel,
}
