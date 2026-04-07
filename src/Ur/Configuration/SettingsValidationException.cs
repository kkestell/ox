namespace Ur.Configuration;

/// <summary>
/// Thrown when one or more settings values don't match their registered JSON
/// schema types. Carries the full list of errors so the caller can report them.
/// <see cref="SettingsWriter"/> checks schemas before persisting, so a validation
/// failure means the write is rejected and the file is not modified.
/// </summary>
public sealed class SettingsValidationException(IReadOnlyList<string> errors)
    : Exception($"Settings validation failed:\n{string.Join("\n", errors)}")
{
    // ReSharper disable once UnusedMember.Global — public API for programmatic error inspection
    public IReadOnlyList<string> Errors { get; } = errors;
}
