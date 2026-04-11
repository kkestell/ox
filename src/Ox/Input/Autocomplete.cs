namespace Ox.Input;

/// <summary>
/// Wraps <see cref="AutocompleteEngine"/> with the input-field integration
/// logic: computes ghost text for the current input and applies Tab completion.
/// </summary>
public sealed class Autocomplete(AutocompleteEngine engine)
{
    /// <summary>
    /// Get the ghost text suffix for the current input, or null if no
    /// completion is available.
    /// </summary>
    public string? GetGhostText(string input) => engine.GetCompletion(input);

    /// <summary>
    /// If a completion is available, apply it to the editor (append the suffix
    /// and move cursor to the end). Returns true if a completion was applied.
    /// </summary>
    public bool TryApply(TextEditor editor)
    {
        var suffix = engine.GetCompletion(editor.Text);
        if (suffix is null) return false;

        editor.SetText(editor.Text + suffix);
        return true;
    }
}
