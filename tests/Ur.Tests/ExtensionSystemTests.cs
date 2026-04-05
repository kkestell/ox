using Microsoft.Extensions.AI;
using Ur.AgentLoop;
using Ur.Extensions;
using Ur.Tests.TestSupport;

namespace Ur.Tests;

public sealed class ExtensionSystemTests
{
    [Fact]
    public void ExtensionId_SerializesAndParsesRoundTrip()
    {
        var extensionId = new ExtensionId(ExtensionTier.Workspace, "sample.echo");

        var serialized = extensionId.ToString();
        var reparsed = ExtensionId.Parse(serialized);

        Assert.Equal("workspace:sample.echo", serialized);
        Assert.Equal(extensionId, reparsed);
    }

    [Fact]
    public async Task DiscoverAllAsync_HigherTierWinsAndStableOrderIsPreserved()
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
            "user-zulu",
            """
            return {
              name = "sample.zulu",
              version = "1.0.0"
            }
            """);
        await env.WriteManifestOnlyExtensionAsync(
            env.UserExtensionsPath,
            "user-alpha",
            """
            return {
              name = "sample.alpha",
              version = "1.0.0"
            }
            """);
        await env.WriteManifestOnlyExtensionAsync(
            env.WorkspaceExtensionsPath,
            "workspace-zulu",
            """
            return {
              name = "sample.workspace.zulu",
              version = "1.0.0"
            }
            """);
        await env.WriteManifestOnlyExtensionAsync(
            env.WorkspaceExtensionsPath,
            "workspace-alpha",
            """
            return {
              name = "sample.workspace.alpha",
              version = "1.0.0"
            }
            """);
        await env.WriteManifestOnlyExtensionAsync(
            env.UserExtensionsPath,
            "user-shadowed",
            """
            return {
              name = "sample.shared",
              version = "2.0.0"
            }
            """);
        Directory.CreateDirectory(Path.Combine(env.WorkspaceExtensionsPath, "missing-manifest"));

        var extensions = await ExtensionLoader.DiscoverAllAsync(
            env.SystemExtensionsPath,
            env.UserExtensionsPath,
            env.WorkspaceExtensionsPath);

        // ReSharper disable ParameterOnlyUsedForPreconditionCheck.Local — Assert.Collection lambda validates properties
        Assert.Collection(
            extensions,
            extension =>
            {
                Assert.Equal("system:sample.shared", extension.Id);
                Assert.Equal(ExtensionTier.System, extension.Tier);
            },
            extension =>
            {
                Assert.Equal("user:sample.alpha", extension.Id);
                Assert.Equal(ExtensionTier.User, extension.Tier);
            },
            extension =>
            {
                Assert.Equal("user:sample.zulu", extension.Id);
                Assert.Equal(ExtensionTier.User, extension.Tier);
            },
            extension =>
            {
                Assert.Equal("workspace:sample.workspace.alpha", extension.Id);
                Assert.Equal(ExtensionTier.Workspace, extension.Tier);
            },
            extension =>
            {
                Assert.Equal("workspace:sample.workspace.zulu", extension.Id);
                Assert.Equal(ExtensionTier.Workspace, extension.Tier);
            }
            );
        // ReSharper restore ParameterOnlyUsedForPreconditionCheck.Local
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
    public async Task ActivateAsync_TrustedExtensionRegistersInvocableTool()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteSampleExtensionAsync(
            env.UserExtensionsPath,
            "sample-echo",
            extensionName: "sample.echo",
            toolName: "sample_echo",
            settingKey: "sample.mode");

        var extension = Assert.Single(await ExtensionLoader.DiscoverAllAsync(
            null,
            env.UserExtensionsPath,
            null));
        extension.SetDesiredState(desiredEnabled: true, hasOverride: false);

        await ExtensionLoader.ActivateAsync(extension);

        Assert.True(extension.IsActive);

        // Verify the tool is accessible via RegisterToolsInto.
        var registry = new ToolRegistry();
        extension.RegisterToolsInto(registry);
        var tool = Assert.IsAssignableFrom<AIFunction>(registry.Get("sample_echo"));

        var result = await tool.InvokeAsync(
            new AIFunctionArguments(new Dictionary<string, object?> { ["text"] = "hello" }));

        Assert.Equal("hello", result);
    }

    [Fact]
    public async Task Deactivate_ClearsToolsAndRuntimeState()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteSampleExtensionAsync(
            env.UserExtensionsPath,
            "sample-echo",
            extensionName: "sample.echo",
            toolName: "sample_echo",
            settingKey: "sample.mode");

        var extension = Assert.Single(await ExtensionLoader.DiscoverAllAsync(
            null,
            env.UserExtensionsPath,
            null));
        extension.SetDesiredState(desiredEnabled: true, hasOverride: false);
        await ExtensionLoader.ActivateAsync(extension);

        ExtensionLoader.Deactivate(extension);

        Assert.False(extension.IsActive);
        Assert.Empty(extension.Tools);
        Assert.Null(extension.LuaState);
    }

    [Fact]
    public async Task StartAsync_DisabledWorkspaceExtensionDoesNotExecuteMainLua()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteExtensionAsync(
            env.WorkspaceExtensionsPath,
            "workspace-broken",
            """
            return {
              name = "sample.workspace",
              version = "1.0.0"
            }
            """,
            """
            this is not valid lua
            """);

        var host = await env.StartHostAsync();
        var extension = Assert.Single(host.Extensions.List());

        Assert.Equal("workspace:sample.workspace", extension.Id);
        Assert.False(extension.DesiredEnabled);
        Assert.False(extension.IsActive);
        Assert.Null(extension.LoadError);
        Assert.Null(host.BuildSessionToolRegistry("test").Get("sample_workspace"));
    }
}
