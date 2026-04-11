using Microsoft.Extensions.AI;
using Ur.Configuration.Keyring;
using Ur.Providers;
using Ur.Tools;

namespace Ur.Hosting;

/// <summary>
/// Options passed to <see cref="ServiceCollectionExtensions.AddUr"/> to configure
/// how the Ur service tree is assembled. Mandatory parameters are marked
/// <see langword="required"/>; everything else has sensible defaults derived
/// from the platform at registration time.
///
/// Test harnesses use the optional overrides (KeyringOverride, ChatClientFactoryOverride,
/// path overrides) to substitute fakes without conditional logic in production code.
/// </summary>
public sealed class UrStartupOptions
{
    /// <summary>
    /// Root of the workspace directory (usually the current working directory).
    /// Determines where .ur/ state, sessions, and skills are stored.
    /// </summary>
    public required string WorkspacePath { get; init; }

    /// <summary>
    /// Override the default user data directory (~/.ur/). Used by tests to isolate
    /// state in a temp directory.
    /// </summary>
    public string? UserDataDirectory { get; init; }

    /// <summary>
    /// Override the default user settings file path. Used by tests.
    /// </summary>
    public string? UserSettingsPath { get; init; }

    /// <summary>
    /// Inject a keyring implementation instead of the platform default (macOS Keychain /
    /// Linux libsecret). Tests use this to avoid touching the real OS keyring.
    /// </summary>
    public IKeyring? KeyringOverride { get; init; }

    /// <summary>
    /// Inject a chat client factory for testing. When set, UrHost.CreateChatClient
    /// delegates to this instead of routing through the provider registry.
    /// </summary>
    public Func<string, IChatClient>? ChatClientFactoryOverride { get; init; }

    /// <summary>
    /// Additional tools to merge into every session's tool registry. Used by tests
    /// to inject fake tools without changing the production code path.
    /// </summary>
    public ToolRegistry? AdditionalTools { get; init; }

    /// <summary>
    /// A fake provider to register alongside the real providers. When set, the
    /// provider is added to the DI container as an additional <see cref="IProvider"/>
    /// and becomes available through the normal ProviderRegistry lookup path.
    /// This keeps fake-provider mode exercising the same composition path as production.
    /// </summary>
    internal IProvider? FakeProvider { get; init; }

    /// <summary>
    /// Ephemeral override for the selected model ID. When set, UrConfiguration
    /// returns this value instead of reading from persisted settings. This lets
    /// test mode (e.g. --fake-provider) select a model at startup without
    /// rewriting user or workspace settings files.
    /// </summary>
    public string? SelectedModelOverride { get; init; }

    /// <summary>
    /// Override path to providers.json. When null, defaults to
    /// {UserDataDirectory}/providers.json. Tests use this to point at
    /// a temp-directory providers.json.
    /// </summary>
    public string? ProvidersJsonPath { get; init; }
}
