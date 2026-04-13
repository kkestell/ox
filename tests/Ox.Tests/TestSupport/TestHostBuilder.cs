using Microsoft.Extensions.AI;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Ox.Configuration;
using Ur.Configuration.Keyring;
using Ur.Hosting;
using Ur.Providers;
using Ur.Tools;

namespace Ox.Tests.TestSupport;

/// <summary>
/// Convenience builder for tests that need a full <see cref="UrHost"/>.
///
/// Mirrors the same DI registration path used by production code so tests exercise
/// the real object graph. The <see cref="IHost"/> is attached to the
/// <see cref="TempWorkspace"/> so it is disposed when the workspace is disposed,
/// preventing leaks of DI-managed singletons.
///
/// Writes a default providers.json (via <see cref="TestProviderConfig"/>) into
/// the workspace's user-data directory unless the caller supplies a custom path.
/// </summary>
internal static class TestHostBuilder
{
    /// <summary>
    /// Creates a UrHost via the DI container, using the provided workspace for paths
    /// and optional overrides for keyring, chat client factory, additional tools,
    /// fake provider, and ephemeral model override.
    /// </summary>
    public static async Task<UrHost> CreateHostAsync(
        TempWorkspace workspace,
        IKeyring? keyring = null,
        Func<string, IChatClient>? chatClientFactory = null,
        ToolRegistry? additionalTools = null,
        IProvider? fakeProvider = null,
        string? selectedModelOverride = null,
        string? providersJsonPath = null)
    {
        // Write the default test providers.json unless the caller explicitly provides a path.
        var effectivePath = providersJsonPath
            ?? TestProviderConfig.Write(workspace.UserDataDirectory);

        var builder = Host.CreateApplicationBuilder();

        // Register Ur settings sources on the host's configuration root so
        // SettingsWriter.Reload() propagates to the same IConfigurationRoot
        // that the standard options pipeline reads from.
        builder.Configuration.AddUrSettings(
            workspace.UserSettingsPath,
            Path.Combine(workspace.WorkspacePath, ".ur", "settings.json"));

        // Load providers.json and register providers — same as Ox's Program.cs
        // does in production. Tests get real provider registrations so the
        // ProviderRegistry is populated correctly.
        var providerConfig = ProviderConfig.Load(effectivePath);
        builder.Services.AddSingleton(providerConfig);
        builder.Services.AddProvidersFromConfig(providerConfig);

        // OxConfiguration wraps model catalog queries (model listing, context windows).
        // Tests that exercise catalog or context window features get real behavior.
        builder.Services.AddSingleton<OxConfiguration>();
        builder.Services.AddSingleton<Func<string, int?>>(sp =>
            sp.GetRequiredService<OxConfiguration>().ResolveContextWindow);

        // Pre-register test overrides before AddUr — TryAddSingleton lets them win.
        builder.Services.AddSingleton(keyring ?? new TestKeyring());

        if (chatClientFactory is not null)
            builder.Services.AddSingleton(chatClientFactory);

        if (additionalTools is not null)
            builder.Services.AddSingleton(additionalTools);

        if (fakeProvider is not null)
            builder.Services.AddSingleton(fakeProvider);

        builder.Services.AddUr(builder.Configuration, o =>
        {
            o.WorkspacePath = workspace.WorkspacePath;
            o.UserDataDirectory = workspace.UserDataDirectory;
            o.UserSettingsPath = workspace.UserSettingsPath;
            o.SelectedModelOverride = selectedModelOverride;
        });

        var host = builder.Build();
        await host.StartAsync();

        // Attach to workspace so the host is disposed when the workspace is disposed.
        workspace.AttachHost(host);

        return host.Services.GetRequiredService<UrHost>();
    }
}
