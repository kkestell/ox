using System.Text.Json;
using Ur.Configuration;
using Ur.Tests.TestSupport;

namespace Ur.Tests;

/// <summary>
/// Tests for <see cref="UrConfiguration"/> and <see cref="Settings"/> behavior.
/// These verify the public API that the CLI and TUI consume: typed accessors,
/// scope merging, schema validation with rollback, and readiness checks.
/// </summary>
public sealed class ConfigurationTests : IDisposable
{
    private readonly string _root = Path.Combine(
        Path.GetTempPath(),
        "ur-config-tests",
        Guid.NewGuid().ToString("N"));

    private string UserSettingsPath => Path.Combine(_root, "user-settings.json");
    private string WorkspaceSettingsPath => Path.Combine(_root, "workspace", ".ur", "settings.json");

    public ConfigurationTests()
    {
        Directory.CreateDirectory(Path.GetDirectoryName(UserSettingsPath)!);
        Directory.CreateDirectory(Path.GetDirectoryName(WorkspaceSettingsPath)!);
    }

    public void Dispose()
    {
        if (Directory.Exists(_root))
            Directory.Delete(_root, recursive: true);
    }

    // ─── Typed string accessors ───────────────────────────────────────

    [Fact]
    public async Task GetStringSetting_AfterSetString_ReturnsValue()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);

        await host.Configuration.SetStringSettingAsync("test.name", "alice");

        Assert.Equal("alice", host.Configuration.GetStringSetting("test.name"));
    }

    [Fact]
    public async Task GetStringSetting_UnsetKey_ReturnsNull()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);

        Assert.Null(host.Configuration.GetStringSetting("nonexistent.key"));
    }

    // ─── Typed boolean accessors ──────────────────────────────────────

    [Fact]
    public async Task GetBoolSetting_True_ReturnsTrue()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);

        await host.Configuration.SetBoolSettingAsync("test.enabled", true);

        Assert.True(host.Configuration.GetBoolSetting("test.enabled"));
    }

    [Fact]
    public async Task GetBoolSetting_False_ReturnsFalse()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);

        await host.Configuration.SetBoolSettingAsync("test.enabled", false);

        Assert.False(host.Configuration.GetBoolSetting("test.enabled"));
    }

    [Fact]
    public async Task GetBoolSetting_UnsetKey_ReturnsNull()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);

        Assert.Null(host.Configuration.GetBoolSetting("nonexistent.key"));
    }

    [Fact]
    public async Task GetBoolSetting_NonBoolValue_ReturnsNull()
    {
        // A key set to a string should return null from the bool accessor —
        // callers should not get a wrong-type surprise.
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);

        await host.Configuration.SetStringSettingAsync("test.value", "not-a-bool");

        Assert.Null(host.Configuration.GetBoolSetting("test.value"));
    }

    // ─── Scope merging ────────────────────────────────────────────────

    [Fact]
    public async Task Settings_WorkspaceScopeOverridesUserScope()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);

        // Set at user scope, then override at workspace scope.
        await host.Configuration.SetStringSettingAsync("test.value", "user-value");
        await host.Configuration.SetStringSettingAsync(
            "test.value", "workspace-value", ConfigurationScope.Workspace);

        // Workspace wins in the merged view.
        Assert.Equal("workspace-value", host.Configuration.GetStringSetting("test.value"));

        // Clear workspace override — user value surfaces again.
        await host.Configuration.ClearSettingAsync("test.value", ConfigurationScope.Workspace);
        Assert.Equal("user-value", host.Configuration.GetStringSetting("test.value"));
    }

    // ─── ClearSetting ─────────────────────────────────────────────────

    [Fact]
    public async Task ClearSetting_RemovesKeyFromScope()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);

        await host.Configuration.SetStringSettingAsync("test.key", "value");
        Assert.NotNull(host.Configuration.GetStringSetting("test.key"));

        await host.Configuration.ClearSettingAsync("test.key");
        Assert.Null(host.Configuration.GetStringSetting("test.key"));
    }

    // ─── API key management ───────────────────────────────────────────

    [Fact]
    public async Task SetAndClearApiKey_AffectsReadiness()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);

        // Start with no API key — should be blocked.
        Assert.Contains(ChatBlockingIssue.MissingApiKey,
            host.Configuration.Readiness.BlockingIssues);

        // Set API key — MissingApiKey blocker should be removed.
        await host.Configuration.SetApiKeyAsync("test-key");
        Assert.DoesNotContain(ChatBlockingIssue.MissingApiKey,
            host.Configuration.Readiness.BlockingIssues);

        // Clear API key — blocker returns.
        await host.Configuration.ClearApiKeyAsync();
        Assert.Contains(ChatBlockingIssue.MissingApiKey,
            host.Configuration.Readiness.BlockingIssues);
    }

    // ─── Settings schema validation with rollback ─────────────────────

    [Fact]
    public async Task SetSetting_ValidationFailure_RollsBackInMemoryState()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);

        // Register a schema that expects a boolean for "test.flag".
        var schema = JsonDocument.Parse("""{"type": "boolean"}""").RootElement;
        host.Configuration.GetType()
            .Assembly.GetType("Ur.Configuration.SettingsSchemaRegistry")!
            .GetMethod("Register")!
            .Invoke(GetSchemaRegistry(host), [
                "test.flag",
                schema.Clone()
            ]);

        // Set a valid boolean value first.
        await host.Configuration.SetBoolSettingAsync("test.flag", true);
        Assert.True(host.Configuration.GetBoolSetting("test.flag"));

        // Try to set a string where boolean is expected — should fail and rollback.
        await Assert.ThrowsAsync<SettingsValidationException>(async () =>
            await host.Configuration.SetStringSettingAsync("test.flag", "not-a-bool"));

        // Value should still be the boolean from before the failed write.
        Assert.True(host.Configuration.GetBoolSetting("test.flag"));
    }

    // ─── Helpers ──────────────────────────────────────────────────────

    private static Task<UrHost> CreateHostAsync(TempWorkspace workspace) =>
        TestHostBuilder.CreateHostAsync(workspace);

    /// <summary>
    /// Reaches into the host to get the schema registry for validation tests.
    /// We access it via the Configuration property's internal wiring rather than
    /// creating one directly, so the test exercises the real object graph.
    /// </summary>
    private static object GetSchemaRegistry(UrHost host)
    {
        // Navigate: host.Configuration._settings._schemaRegistry
        var config = host.Configuration;
        var settingsField = config.GetType()
            .GetField("_settings", System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Instance)!;
        var settings = settingsField.GetValue(config)!;
        var registryField = settings.GetType()
            .GetField("_schemaRegistry", System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Instance)!;
        return registryField.GetValue(settings)!;
    }
}
