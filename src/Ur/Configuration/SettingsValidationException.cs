namespace Ur.Configuration;

/// <summary>
/// Thrown when one or more settings values don't match their registered JSON
/// schema types. Carries the full list of errors so the caller can report them.
/// This exception triggers the rollback logic in <see cref="Settings.SetAsync"/>
/// and <see cref="Settings.ClearAsync"/> to prevent the process from running
/// with an invalid configuration.
/// </summary>
public sealed class SettingsValidationException(IReadOnlyList<string> errors)
    : Exception($"Settings validation failed:\n{string.Join("\n", errors)}")
{
    // ReSharper disable once UnusedMember.Global — public API for programmatic error inspection
    public IReadOnlyList<string> Errors { get; } = errors;
}
