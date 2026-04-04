namespace Ur.Widgets;

/// <summary>
/// Defines a single column in a <see cref="Table{T}"/>. Each column has a header
/// label and a function that extracts the display text from a row item. Columns
/// can either have an explicit fixed width or share the remaining space equally
/// (when Width is null).
///
/// Follows a WinForms-inspired pattern: instead of DataGridViewTextBoxColumn +
/// DataPropertyName, we use a Func{T, string} value selector for a more functional,
/// type-safe API.
/// </summary>
/// <typeparam name="T">The row item type, matching the parent Table{T}.</typeparam>
public class TableColumn<T>
{
    /// <summary>
    /// Text displayed in the header row above this column.
    /// </summary>
    public string Header { get; }

    /// <summary>
    /// Extracts the display string for this column from a row item.
    /// Called once per visible row during Draw().
    /// </summary>
    public Func<T, string> ValueSelector { get; }

    /// <summary>
    /// Explicit column width in characters. When null, the column receives an
    /// equal share of the remaining space after fixed-width columns and chrome
    /// (separators, scrollbar) are accounted for.
    /// </summary>
    public int? Width { get; }

    public TableColumn(string header, Func<T, string> valueSelector, int? width = null)
    {
        Header = header ?? "";
        ValueSelector = valueSelector ?? throw new ArgumentNullException(nameof(valueSelector));
        Width = width;
    }
}
