using Microsoft.Extensions.AI;
using Ur.AgentLoop;
using Ur.Configuration.Keyring;
using Ur.Extensions;

namespace Ur.Tests;

public sealed class ExtensionSystemTests
{
    [Fact]
    public async Task DiscoverAllAsync_HigherTierWinsAndSkipsDirectoriesWithoutManifest()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteManifestOnlyExtensionAsync(
            env.SystemExtensionsPath,
            "system-shared",
            """
            return {
              name = "sample.shared",
              version = "1.0.0"
            }
            """);
        await env.WriteManifestOnlyExtensionAsync(
            env.UserExtensionsPath,
            "user-shared",
            """
            return {
              name = "sample.shared",
              version = "2.0.0"
            }
            """);
        Directory.CreateDirectory(Path.Combine(env.WorkspaceExtensionsPath, "missing-manifest"));
        await env.WriteManifestOnlyExtensionAsync(
            env.WorkspaceExtensionsPath,
            "workspace-only",
            """
            return {
              name = "sample.workspace",
              version = "1.0.0"
            }
            """);

        var extensions = await ExtensionLoader.DiscoverAllAsync(
            env.SystemExtensionsPath,
            env.UserExtensionsPath,
            env.WorkspaceExtensionsPath);

        Assert.Collection(
            extensions,
            extension =>
            {
                Assert.Equal("sample.shared", extension.Name);
                Assert.Equal(ExtensionTier.System, extension.Tier);
            },
            extension =>
            {
                Assert.Equal("sample.workspace", extension.Name);
                Assert.Equal(ExtensionTier.Workspace, extension.Tier);
            });
    }

    [Fact]
    public async Task DiscoverAllAsync_ManifestSettingsAreConvertedToJsonSchema()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteManifestOnlyExtensionAsync(
            env.UserExtensionsPath,
            "settings-ext",
            """
            return {
              name = "sample.settings",
              version = "1.0.0",
              description = "Settings sample",
              settings = {
                ["sample.enabled"] = {
                  type = "boolean",
                  description = "Enable the extension"
                }
              }
            }
            """);

        var extension = Assert.Single(await ExtensionLoader.DiscoverAllAsync(
            null,
            env.UserExtensionsPath,
            null));

        var schema = extension.SettingsSchemas["sample.enabled"];
        Assert.Equal("boolean", schema.GetProperty("type").GetString());
        Assert.Equal("Enable the extension", schema.GetProperty("description").GetString());
    }

    [Fact]
    public async Task DiscoverAllAsync_ManifestSandboxViolationSkipsExtension()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteManifestOnlyExtensionAsync(
            env.UserExtensionsPath,
            "sandboxed-out",
            """
            dofile("forbidden.lua")

            return {
              name = "sample.bad",
              version = "1.0.0"
            }
            """);

        var extensions = await ExtensionLoader.DiscoverAllAsync(
            null,
            env.UserExtensionsPath,
            null);

        Assert.Empty(extensions);
    }

    [Fact]
    public async Task InitializeAsync_TrustedExtensionRegistersInvocableTool()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteSampleExtensionAsync(
            env.UserExtensionsPath,
            "sample-echo",
            extensionName: "sample.echo",
            toolName: "sample.echo",
            settingKey: "sample.mode");

        var extension = Assert.Single(await ExtensionLoader.DiscoverAllAsync(
            null,
            env.UserExtensionsPath,
            null));
        var registry = new ToolRegistry();

        await ExtensionLoader.InitializeAsync(extension, registry);

        Assert.True(extension.Enabled);
        var tool = Assert.IsAssignableFrom<AIFunction>(registry.Get("sample.echo"));

        var result = await tool.InvokeAsync(
            new AIFunctionArguments(new Dictionary<string, object?> { ["text"] = "hello" }));

        Assert.Equal("hello", result);
    }

    [Fact]
    public async Task InitializeAsync_WorkspaceExtensionRemainsDisabled()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteSampleExtensionAsync(
            env.WorkspaceExtensionsPath,
            "sample-echo",
            extensionName: "sample.workspace",
            toolName: "sample.workspace.echo",
            settingKey: "sample.workspace.mode");

        var extension = Assert.Single(await ExtensionLoader.DiscoverAllAsync(
            null,
            null,
            env.WorkspaceExtensionsPath));
        var registry = new ToolRegistry();

        await ExtensionLoader.InitializeAsync(extension, registry);

        Assert.False(extension.Enabled);
        Assert.Single(extension.Tools);
        Assert.Null(registry.Get("sample.workspace.echo"));
    }

    [Fact]
    public async Task StartAsync_LoadsExtensionsAndKeepsWorkspaceExtensionsDisabledByDefault()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteSampleExtensionAsync(
            env.UserExtensionsPath,
            "user-sample",
            extensionName: "sample.user",
            toolName: "sample.user.echo",
            settingKey: "sample.mode");
        await env.WriteSampleExtensionAsync(
            env.WorkspaceExtensionsPath,
            "workspace-sample",
            extensionName: "sample.workspace",
            toolName: "sample.workspace.echo",
            settingKey: "sample.workspace.mode");
        await File.WriteAllTextAsync(
            env.UserSettingsPath,
            """
            {
              "sample.mode": "fast"
            }
            """);

        var host = await UrHost.StartAsync(
            env.WorkspacePath,
            new TestKeyring(),
            env.UserSettingsPath,
            chatClientFactoryOverride: null,
            tools: null,
            systemExtensionsPath: env.SystemExtensionsPath,
            userExtensionsPath: env.UserExtensionsPath);

        Assert.Collection(
            host.Extensions.OrderBy(extension => extension.Name),
            extension =>
            {
                Assert.Equal("sample.user", extension.Name);
                Assert.True(extension.Enabled);
            },
            extension =>
            {
                Assert.Equal("sample.workspace", extension.Name);
                Assert.False(extension.Enabled);
            });

        var trustedTool = Assert.IsAssignableFrom<AIFunction>(host.Tools.Get("sample.user.echo"));
        var trustedResult = await trustedTool.InvokeAsync(
            new AIFunctionArguments(new Dictionary<string, object?> { ["text"] = "hi" }));

        Assert.Equal("hi", trustedResult);
        Assert.Null(host.Tools.Get("sample.workspace.echo"));
    }

    private sealed class TempExtensionEnvironment : IDisposable
    {
        private readonly string _rootPath = Path.Combine(
            Path.GetTempPath(),
            "ur-extension-tests",
            Guid.NewGuid().ToString("N"));

        public string SystemExtensionsPath => Path.Combine(_rootPath, "system");
        public string UserExtensionsPath => Path.Combine(_rootPath, "user");
        public string WorkspacePath => Path.Combine(_rootPath, "workspace");
        public string WorkspaceExtensionsPath => Path.Combine(WorkspacePath, ".ur", "extensions");
        public string UserSettingsPath => Path.Combine(_rootPath, "settings", "user-settings.json");

        public TempExtensionEnvironment()
        {
            Directory.CreateDirectory(SystemExtensionsPath);
            Directory.CreateDirectory(UserExtensionsPath);
            Directory.CreateDirectory(WorkspacePath);
            Directory.CreateDirectory(Path.GetDirectoryName(UserSettingsPath)!);
        }

        public async Task WriteManifestOnlyExtensionAsync(
            string parentDirectory,
            string directoryName,
            string manifestContents)
        {
            var extensionDirectory = Path.Combine(parentDirectory, directoryName);
            Directory.CreateDirectory(extensionDirectory);
            await File.WriteAllTextAsync(
                Path.Combine(extensionDirectory, "manifest.lua"),
                manifestContents);
        }

        public async Task WriteSampleExtensionAsync(
            string parentDirectory,
            string directoryName,
            string extensionName,
            string toolName,
            string settingKey)
        {
            var extensionDirectory = Path.Combine(parentDirectory, directoryName);
            Directory.CreateDirectory(extensionDirectory);

            var manifest = (await File.ReadAllTextAsync(SampleExtensionPath("manifest.lua")))
                .Replace("__EXTENSION_NAME__", extensionName, StringComparison.Ordinal)
                .Replace("__SETTING_KEY__", settingKey, StringComparison.Ordinal);
            var main = (await File.ReadAllTextAsync(SampleExtensionPath("main.lua")))
                .Replace("__TOOL_NAME__", toolName, StringComparison.Ordinal);

            await File.WriteAllTextAsync(Path.Combine(extensionDirectory, "manifest.lua"), manifest);
            await File.WriteAllTextAsync(Path.Combine(extensionDirectory, "main.lua"), main);
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

    private sealed class TestKeyring : IKeyring
    {
        public string? GetSecret(string service, string account) => null;

        public void SetSecret(string service, string account, string secret)
        {
        }

        public void DeleteSecret(string service, string account)
        {
        }
    }
}
