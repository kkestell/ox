using System.Text.Json;
using Ur.Configuration;

namespace Ur.Tests;

/// <summary>
/// Tests for <see cref="SettingsLoader"/> merge and validation logic.
/// The merge contract is foundational to configuration: workspace values
/// must override user values for the same key, and validation must reject
/// values whose types don't match their registered schemas.
/// </summary>
public sealed class SettingsLoaderTests : IDisposable
{
    private readonly string _root = Path.Combine(
        Path.GetTempPath(),
        "ur-settings-loader-tests",
        Guid.NewGuid().ToString("N"));

    public SettingsLoaderTests()
    {
        Directory.CreateDirectory(_root);
    }

    public void Dispose()
    {
        if (Directory.Exists(_root))
            Directory.Delete(_root, recursive: true);
    }

    // ─── Merge ────────────────────────────────────────────────────────

    [Fact]
    public void Merge_WorkspaceOverridesUser()
    {
        var user = ParseDict("""{"color": "blue"}""");
        var workspace = ParseDict("""{"color": "red"}""");

        var merged = SettingsLoader.Merge(user, workspace);

        Assert.Equal("red", merged["color"].GetString());
    }

    [Fact]
    public void Merge_DisjointKeys_BothPresent()
    {
        var user = ParseDict("""{"user.key": "u"}""");
        var workspace = ParseDict("""{"workspace.key": "w"}""");

        var merged = SettingsLoader.Merge(user, workspace);

        Assert.Equal("u", merged["user.key"].GetString());
        Assert.Equal("w", merged["workspace.key"].GetString());
    }

    [Fact]
    public void Merge_EmptyWorkspace_ReturnsUserValues()
    {
        var user = ParseDict("""{"name": "alice"}""");
        var workspace = new Dictionary<string, JsonElement>();

        var merged = SettingsLoader.Merge(user, workspace);

        Assert.Single(merged);
        Assert.Equal("alice", merged["name"].GetString());
    }

    // ─── Validate ─────────────────────────────────────────────────────

    [Fact]
    public void Validate_CorrectType_Succeeds()
    {
        var registry = new SettingsSchemaRegistry();
        RegisterBoolSchema(registry, "test.flag");

        var values = ParseDict("""{"test.flag": true}""");

        // Should not throw.
        SettingsLoader.Validate(registry, values);
    }

    [Fact]
    public void Validate_WrongType_ThrowsValidationException()
    {
        var registry = new SettingsSchemaRegistry();
        RegisterBoolSchema(registry, "test.flag");

        // "hello" is a string, but schema expects boolean.
        var values = ParseDict("""{"test.flag": "hello"}""");

        Assert.Throws<SettingsValidationException>(
            () => SettingsLoader.Validate(registry, values));
    }

    [Fact]
    public void Validate_UnknownKey_ToleratedSilently()
    {
        var registry = new SettingsSchemaRegistry();
        // Don't register any schemas — the key is unknown.

        var values = ParseDict("""{"unknown.key": "anything"}""");

        // Unknown keys from uninstalled extensions should not cause errors.
        SettingsLoader.Validate(registry, values);
    }

    [Fact]
    public void Validate_MultipleErrors_AllReported()
    {
        var registry = new SettingsSchemaRegistry();
        RegisterBoolSchema(registry, "a.flag");
        RegisterStringSchema(registry, "b.name");

        // Both values have wrong types.
        var values = ParseDict("""{"a.flag": "not-bool", "b.name": 42}""");

        var ex = Assert.Throws<SettingsValidationException>(
            () => SettingsLoader.Validate(registry, values));

        Assert.Contains("a.flag", ex.Message);
        Assert.Contains("b.name", ex.Message);
    }

    // ─── Load from files ──────────────────────────────────────────────

    [Fact]
    public void Load_MissingFiles_ReturnsEmptySettings()
    {
        var registry = new SettingsSchemaRegistry();
        var loader = new SettingsLoader(registry);

        var settings = loader.Load(
            Path.Combine(_root, "nonexistent-user.json"),
            Path.Combine(_root, "nonexistent-workspace.json"));

        // No crash, and settings should be empty.
        Assert.Null(settings.GetString("anything"));
    }

    [Fact]
    public void Load_ValidFiles_MergesCorrectly()
    {
        var userPath = Path.Combine(_root, "user.json");
        var workspacePath = Path.Combine(_root, "workspace.json");

        File.WriteAllText(userPath, """{"ur.model": "user-model", "ur.theme": "dark"}""");
        File.WriteAllText(workspacePath, """{"ur.model": "workspace-model"}""");

        var registry = new SettingsSchemaRegistry();
        var loader = new SettingsLoader(registry);
        var settings = loader.Load(userPath, workspacePath);

        // Workspace overrides user for "ur.model".
        Assert.Equal("workspace-model", settings.GetString("ur.model"));
        // User-only key is preserved.
        Assert.Equal("dark", settings.GetString("ur.theme"));
    }

    // ─── Helpers ──────────────────────────────────────────────────────

    private static Dictionary<string, JsonElement> ParseDict(string json)
    {
        using var doc = JsonDocument.Parse(json);
        var result = new Dictionary<string, JsonElement>(StringComparer.Ordinal);
        foreach (var prop in doc.RootElement.EnumerateObject())
            result[prop.Name] = prop.Value.Clone();
        return result;
    }

    private static void RegisterBoolSchema(SettingsSchemaRegistry registry, string key)
    {
        using var doc = JsonDocument.Parse("""{"type": "boolean"}""");
        registry.Register(key, doc.RootElement.Clone());
    }

    private static void RegisterStringSchema(SettingsSchemaRegistry registry, string key)
    {
        using var doc = JsonDocument.Parse("""{"type": "string"}""");
        registry.Register(key, doc.RootElement.Clone());
    }
}
