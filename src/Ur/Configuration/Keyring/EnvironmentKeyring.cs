using System.Text.RegularExpressions;

namespace Ur.Configuration.Keyring;

/// <summary>
/// Keyring implementation for headless/container environments where no OS keyring
/// daemon is available. Reads API keys from environment variables of the form
/// <c>UR_API_KEY_{ACCOUNT_UPPER}</c> — e.g. <c>UR_API_KEY_GOOGLE</c> for the
/// "google" account.
///
/// SetSecret and DeleteSecret are no-ops: containers are ephemeral, keys come in
/// via environment variables, and writes would be lost on container exit.
///
/// The service parameter is ignored because all Ur keyring lookups use the same
/// service name ("ur"). We key only on the account (provider name) to keep the
/// env var naming simple.
/// </summary>
public sealed partial class EnvironmentKeyring : IKeyring
{
    /// <summary>
    /// Reads <c>UR_API_KEY_{ACCOUNT}</c> where the account name is uppercased
    /// and non-alphanumeric characters (hyphens, dots, etc.) are replaced with
    /// underscores. E.g. account "openai-compatible" → <c>UR_API_KEY_OPENAI_COMPATIBLE</c>.
    /// </summary>
    public string? GetSecret(string service, string account)
    {
        var envName = $"UR_API_KEY_{NormalizeAccount(account)}";
        return Environment.GetEnvironmentVariable(envName);
    }

    // No-ops — container state is ephemeral.
    public void SetSecret(string service, string account, string secret) { }
    public void DeleteSecret(string service, string account) { }

    /// <summary>
    /// Converts a provider account name to an environment-variable-safe suffix:
    /// uppercase, with any character that isn't alphanumeric or underscore replaced
    /// by an underscore.
    /// </summary>
    private static string NormalizeAccount(string account)
        => NonAlphanumericRegex().Replace(account.ToUpperInvariant(), "_");

    [GeneratedRegex("[^A-Z0-9_]")]
    private static partial Regex NonAlphanumericRegex();
}
