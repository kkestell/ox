namespace Ur.Configuration;

public sealed class SettingsValidationException : Exception
{
    public IReadOnlyList<string> Errors { get; }

    public SettingsValidationException(IReadOnlyList<string> errors)
        : base($"Settings validation failed:\n{string.Join("\n", errors)}")
    {
        Errors = errors;
    }
}
