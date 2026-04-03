namespace Ur.Configuration;

/// <summary>
/// Thrown when one or more settings values don't match their registered JSON
/// schema types. Carries the full list of errors so the caller can report them.
/// This exception triggers the rollback logic in <see cref="Settings.SetAsync"/>
/// and <see cref="Settings.ClearAsync"/> to prevent the process from running
/// with an invalid configuration.
/// </summary>
public sealed class SettingsValidationException : Exception
{
    public IReadOnlyList<string> Errors { get; }

    public SettingsValidationException(IReadOnlyList<string> errors)
        : base($"Settings validation failed:\n{string.Join("\n", errors)}")
    {
        Errors = errors;
    }
}
