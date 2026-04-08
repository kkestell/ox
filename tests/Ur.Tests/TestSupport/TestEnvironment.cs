using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Ur.Configuration.Keyring;
using Ur.Extensions;
using Ur.Hosting;

namespace Ur.Tests.TestSupport;

internal sealed class TempExtensionEnvironment : IDisposable
{
    private readonly string _rootPath = Path.Combine(
        Path.GetTempPath(),
        "ur-extension-tests",
        Guid.NewGuid().ToString("N"));

    private IHost? _host;

    private string UserDataDirectory => Path.Combine(_rootPath, "user-data");
    public string SystemExtensionsPath => Path.Combine(UserDataDirectory, "extensions", "system");
    public string UserExtensionsPath => Path.Combine(UserDataDirectory, "extensions", "user");
    public string WorkspacePath => Path.Combine(_rootPath, "workspace");
    public string WorkspaceExtensionsPath => Path.Combine(WorkspacePath, ".ur", "extensions");
    private string UserSettingsPath => Path.Combine(UserDataDirectory, "settings.json");
    public string GlobalOverridesPath => Path.Combine(UserDataDirectory, "extensions-state.json");

    public string WorkspaceOverridesPath => Path.Combine(
        UserDataDirectory,
        "workspaces",
        ExtensionOverrideStore.ComputeWorkspaceHash(WorkspacePath),
        "extensions-state.json");

    public TempExtensionEnvironment()
    {
        Directory.CreateDirectory(SystemExtensionsPath);
        Directory.CreateDirectory(UserExtensionsPath);
        Directory.CreateDirectory(WorkspacePath);
        Directory.CreateDirectory(UserDataDirectory);
    }

    public static async Task WriteManifestOnlyExtensionAsync(
        string parentDirectory,
        string directoryName,
        string manifestContents)
    {
        var extensionDirectory = Path.Combine(parentDirectory, directoryName);
        Directory.CreateDirectory(extensionDirectory);
        await File.WriteAllTextAsync(
                Path.Combine(extensionDirectory, "manifest.lua"),
                manifestContents)
            .ConfigureAwait(false);
    }

    public static async Task WriteExtensionAsync(
        string parentDirectory,
        string directoryName,
        string manifestContents,
        string? mainContents)
    {
        var extensionDirectory = Path.Combine(parentDirectory, directoryName);
        Directory.CreateDirectory(extensionDirectory);

        await File.WriteAllTextAsync(
                Path.Combine(extensionDirectory, "manifest.lua"),
                manifestContents)
            .ConfigureAwait(false);

        if (mainContents is not null)
        {
            await File.WriteAllTextAsync(
                    Path.Combine(extensionDirectory, "main.lua"),
                    mainContents)
                .ConfigureAwait(false);
        }
    }

    public static async Task WriteSampleExtensionAsync(
        string parentDirectory,
        string directoryName,
        string extensionName,
        string toolName,
        string settingKey,
        string? mainContents = null)
    {
        var manifest = (await File.ReadAllTextAsync(SampleExtensionPath("manifest.lua")).ConfigureAwait(false))
            .Replace("__EXTENSION_NAME__", extensionName, StringComparison.Ordinal)
            .Replace("__SETTING_KEY__", settingKey, StringComparison.Ordinal);
        var main = mainContents ?? (await File.ReadAllTextAsync(SampleExtensionPath("main.lua")).ConfigureAwait(false))
            .Replace("__TOOL_NAME__", toolName, StringComparison.Ordinal);

        await WriteExtensionAsync(parentDirectory, directoryName, manifest, main)
            .ConfigureAwait(false);
    }

    public async Task WriteGlobalOverridesAsync(string contents)
    {
        var directory = Path.GetDirectoryName(GlobalOverridesPath)!;
        Directory.CreateDirectory(directory);
        await File.WriteAllTextAsync(GlobalOverridesPath, contents).ConfigureAwait(false);
    }

    public async Task WriteWorkspaceOverridesAsync(string contents)
    {
        var directory = Path.GetDirectoryName(WorkspaceOverridesPath)!;
        Directory.CreateDirectory(directory);
        await File.WriteAllTextAsync(WorkspaceOverridesPath, contents).ConfigureAwait(false);
    }

    /// <summary>
    /// Boots a full UrHost via the DI container, identical to how the CLI and TUI
    /// start up. Tests get the same initialization path as production code.
    /// </summary>
    public async Task<UrHost> StartHostAsync(
        IKeyring? keyring = null,
        Func<string, Microsoft.Extensions.AI.IChatClient>? chatClientFactoryOverride = null,
        CancellationToken ct = default)
    {
        var builder = Host.CreateApplicationBuilder();
        builder.Services.AddUr(new UrStartupOptions
        {
            WorkspacePath = WorkspacePath,
            UserDataDirectory = UserDataDirectory,
            UserSettingsPath = UserSettingsPath,
            SystemExtensionsPath = SystemExtensionsPath,
            UserExtensionsPath = UserExtensionsPath,
            KeyringOverride = keyring ?? new TestKeyring(),
            ChatClientFactoryOverride = chatClientFactoryOverride,
        });

        _host = builder.Build();
        await _host.StartAsync(ct);
        return _host.Services.GetRequiredService<UrHost>();
    }

    public void Dispose()
    {
        _host?.Dispose();

        if (Directory.Exists(_rootPath))
            Directory.Delete(_rootPath, recursive: true);
    }

    private static string SampleExtensionPath(string fileName) =>
        Path.Combine(
            AppContext.BaseDirectory,
            "TestData",
            "Extensions",
            "sample-echo",
            fileName);
}

internal sealed class TempWorkspace : IDisposable
{
    private readonly string? _rootPath;
    private IHost? _host;

    public string WorkspacePath { get; }
    public string UserDataDirectory { get; }
    public string UserSettingsPath { get; }

    /// <summary>
    /// Attaches an <see cref="Microsoft.Extensions.Hosting.IHost"/> to this workspace
    /// so it is disposed when the workspace is disposed. Called by
    /// <see cref="TestHostBuilder"/> to prevent host leaks.
    /// </summary>
    internal void AttachHost(IHost host) => _host = host;

    /// <summary>
    /// Creates a self-contained temp workspace with auto-generated paths.
    /// Dispose deletes everything.
    /// </summary>
    public TempWorkspace()
    {
        _rootPath = Path.Combine(
            Path.GetTempPath(),
            "ur-tests",
            Guid.NewGuid().ToString("N"));

        WorkspacePath = Path.Combine(_rootPath, "workspace");
        UserDataDirectory = Path.Combine(_rootPath, "user-data");
        UserSettingsPath = Path.Combine(UserDataDirectory, "settings.json");

        Directory.CreateDirectory(WorkspacePath);
        Directory.CreateDirectory(UserDataDirectory);
    }

    /// <summary>
    /// Wraps pre-existing paths without owning them. Dispose is a no-op.
    /// Used by tests that manage their own temp directories.
    /// </summary>
    public TempWorkspace(string workspacePath, string userDataDirectory, string userSettingsPath)
    {
        _rootPath = null;
        WorkspacePath = workspacePath;
        UserDataDirectory = userDataDirectory;
        UserSettingsPath = userSettingsPath;
    }

    public void Dispose()
    {
        _host?.Dispose();

        if (_rootPath is not null && Directory.Exists(_rootPath))
            Directory.Delete(_rootPath, recursive: true);
    }
}

internal sealed class TestKeyring : IKeyring
{
    private readonly Dictionary<(string Service, string Account), string> _secrets = new();

    public string? GetSecret(string service, string account) =>
        _secrets.GetValueOrDefault((service, account));

    public void SetSecret(string service, string account, string secret) =>
        _secrets[(service, account)] = secret;

    public void DeleteSecret(string service, string account) =>
        _secrets.Remove((service, account));
}
