using Microsoft.Extensions.AI;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Ur.Configuration.Keyring;
using Ur.Hosting;
using Ur.Tools;

namespace Ur.Tests.TestSupport;

/// <summary>
/// Convenience builder for tests that need a full <see cref="UrHost"/>.
///
/// Mirrors the same DI registration path used by production code so tests exercise
/// the real object graph. The <see cref="IHost"/> is attached to the
/// <see cref="TempWorkspace"/> so it is disposed when the workspace is disposed,
/// preventing leaks of DI-managed singletons.
/// </summary>
internal static class TestHostBuilder
{
    /// <summary>
    /// Creates a UrHost via the DI container, using the provided workspace for paths
    /// and optional overrides for keyring, chat client factory, and additional tools.
    /// The underlying <see cref="IHost"/> is attached to <paramref name="workspace"/>
    /// and will be disposed when the workspace is disposed.
    /// </summary>
    public static async Task<UrHost> CreateHostAsync(
        TempWorkspace workspace,
        IKeyring? keyring = null,
        Func<string, IChatClient>? chatClientFactory = null,
        ToolRegistry? additionalTools = null)
    {
        var builder = Host.CreateApplicationBuilder();
        builder.Services.AddUr(new UrStartupOptions
        {
            WorkspacePath = workspace.WorkspacePath,
            UserDataDirectory = workspace.UserDataDirectory,
            UserSettingsPath = workspace.UserSettingsPath,
            KeyringOverride = keyring ?? new TestKeyring(),
            ChatClientFactoryOverride = chatClientFactory,
            AdditionalTools = additionalTools,
        });

        var host = builder.Build();
        await host.StartAsync();

        // Attach to workspace so the host is disposed when the workspace is disposed.
        workspace.AttachHost(host);

        return host.Services.GetRequiredService<UrHost>();
    }
}
