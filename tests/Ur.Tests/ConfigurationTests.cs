using System.Text.Json;
using Ur.Configuration;
using Ur.Tests.TestSupport;

namespace Ur.Tests;

/// <summary>
/// Tests for <see cref="UrConfiguration"/> behavior: typed accessors, scope merging,
/// schema validation, and readiness checks. These verify the public API that the
/// CLI and TUI consume.
/// </summary>
public sealed class ConfigurationTests : IDisposable
{
    private readonly string _root = Path.Combine(
        Path.GetTempPath(),
        "ur-config-tests",
        Guid.NewGuid().ToString("N"));

    public ConfigurationTests()
    {
        Directory.CreateDirectory(_root);
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

    // ─── Settings schema validation ──────────────────────────────────

    [Fact]
    public async Task SetSetting_ValidationFailure_ThrowsException()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);

        // Register a schema that expects a boolean for "test.flag" via the
        // internally-exposed SettingsSchemaRegistry (same instance as DI container).
        var schema = JsonDocument.Parse("""{"type": "boolean"}""").RootElement.Clone();
        host.SettingsSchemas.Register("test.flag", schema);

        // Set a valid boolean value first.
        await host.Configuration.SetBoolSettingAsync("test.flag", true);
        Assert.True(host.Configuration.GetBoolSetting("test.flag"));

        // Try to set a string where boolean is expected — should fail.
        await Assert.ThrowsAsync<SettingsValidationException>(async () =>
            await host.Configuration.SetStringSettingAsync("test.flag", "not-a-bool"));

        // The valid boolean value should still be readable (write was rejected).
        Assert.True(host.Configuration.GetBoolSetting("test.flag"));
    }

    // ─── SelectedModelId via IOptionsMonitor ─────────────────────────

    [Fact]
    public async Task SelectedModelId_ReflectsSetModel()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);

        Assert.Null(host.Configuration.SelectedModelId);

        await host.Configuration.SetSelectedModelAsync("openai/gpt-4o");

        Assert.Equal("openai/gpt-4o", host.Configuration.SelectedModelId);
    }

    // ─── Helpers ──────────────────────────────────────────────────────

    private static Task<UrHost> CreateHostAsync(TempWorkspace workspace) =>
        TestHostBuilder.CreateHostAsync(workspace);
}
