using Ur.Configuration.Keyring;
using Ur.Extensions;

namespace Ur.Tests.TestSupport;

internal sealed class TempExtensionEnvironment : IDisposable
{
    private readonly string _rootPath = Path.Combine(
        Path.GetTempPath(),
        "ur-extension-tests",
        Guid.NewGuid().ToString("N"));

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

    public async Task<UrHost> StartHostAsync(
        IKeyring? keyring = null,
        Func<string, Microsoft.Extensions.AI.IChatClient>? chatClientFactoryOverride = null,
        CancellationToken ct = default)
    {
        return await UrHost.StartAsync(
                WorkspacePath,
                keyring ?? new TestKeyring(),
                UserSettingsPath,
                chatClientFactoryOverride,
                additionalTools: null,
                systemExtensionsPath: SystemExtensionsPath,
                userExtensionsPath: UserExtensionsPath,
                userDataDirectory: UserDataDirectory,
                ct)
            .ConfigureAwait(false);
    }

    public void Dispose()
    {
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
    private readonly string _rootPath = Path.Combine(
        Path.GetTempPath(),
        "ur-tests",
        Guid.NewGuid().ToString("N"));

    public string WorkspacePath => Path.Combine(_rootPath, "workspace");
    public string UserDataDirectory => Path.Combine(_rootPath, "user-data");
    public string UserSettingsPath => Path.Combine(UserDataDirectory, "settings.json");

    public TempWorkspace()
    {
        Directory.CreateDirectory(WorkspacePath);
        Directory.CreateDirectory(UserDataDirectory);
    }

    public void Dispose()
    {
        if (Directory.Exists(_rootPath))
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
