using System.Text.Json;
using Microsoft.Extensions.Configuration;
using Ur.Configuration;

namespace Ur.Tests;

/// <summary>
/// Tests for the configuration pipeline that replaced <c>SettingsLoader</c>:
/// <see cref="UrSettingsConfigurationProvider"/> (load + migration) and
/// <see cref="SettingsWriter"/> (write + validate + reload).
///
/// The merge contract is foundational to configuration: workspace values
/// must override user values for the same key, and validation must reject
/// values whose types don't match their registered schemas.
/// </summary>
public sealed class SettingsConfigurationTests : IDisposable
{
    private readonly string _root = Path.Combine(
        Path.GetTempPath(),
        "ur-settings-config-tests",
        Guid.NewGuid().ToString("N"));

    public SettingsConfigurationTests()
    {
        Directory.CreateDirectory(_root);
    }

    public void Dispose()
    {
        if (Directory.Exists(_root))
            Directory.Delete(_root, recursive: true);
    }

    // ─── Provider: Merge semantics ───────────────────────────────────

    [Fact]
    public void Provider_WorkspaceOverridesUser()
    {
        var userPath = WriteTempJson("""{"ur": {"color": "blue"}}""");
        var workspacePath = WriteTempJson("""{"ur": {"color": "red"}}""");

        var config = BuildConfig(userPath, workspacePath);

        Assert.Equal("red", config["ur:color"]);
    }

    [Fact]
    public void Provider_DisjointKeys_BothPresent()
    {
        var userPath = WriteTempJson("""{"user": {"key": "u"}}""");
        var workspacePath = WriteTempJson("""{"workspace": {"key": "w"}}""");

        var config = BuildConfig(userPath, workspacePath);

        Assert.Equal("u", config["user:key"]);
        Assert.Equal("w", config["workspace:key"]);
    }

    [Fact]
    public void Provider_EmptyWorkspace_ReturnsUserValues()
    {
        var userPath = WriteTempJson("""{"ns": {"name": "alice"}}""");
        var workspacePath = Path.Combine(_root, "nonexistent.json");

        var config = BuildConfig(userPath, workspacePath);

        Assert.Equal("alice", config["ns:name"]);
    }

    [Fact]
    public void Provider_MissingFiles_ReturnsEmpty()
    {
        var config = BuildConfig(
            Path.Combine(_root, "missing-user.json"),
            Path.Combine(_root, "missing-workspace.json"));

        Assert.Null(config["anything"]);
    }

    // ─── Writer: Validate ────────────────────────────────────────────

    [Fact]
    public async Task Writer_SetAsync_ValidationFailure_ThrowsAndDoesNotPersist()
    {
        var userPath = WriteTempJson("{}");

        var registry = BuildRegistryWithBoolSchema("test.flag");
        var writerWithSchema = BuildWriterWithRegistry(registry, userPath, null);

        // Write a string where boolean is expected — should throw.
        var stringValue = JsonSerializer.SerializeToElement("not-a-bool", SettingsJsonContext.Default.String);
        await Assert.ThrowsAsync<SettingsValidationException>(
            () => writerWithSchema.SetAsync("test.flag", stringValue, ConfigurationScope.User));

        // The failed write should not have modified the file. Verify by checking
        // the value is not present rather than comparing raw strings (avoids
        // sensitivity to JSON formatting).
        Assert.Null(writerWithSchema.Get("test.flag"));
    }

    [Fact]
    public async Task Writer_SetAsync_ValidValue_Persists()
    {
        var userPath = WriteTempJson("{}");
        var (writer, config) = BuildWriter(userPath, null);

        var stringValue = JsonSerializer.SerializeToElement("hello", SettingsJsonContext.Default.String);
        await writer.SetAsync("test.key", stringValue, ConfigurationScope.User);

        // Should be readable from IConfiguration after reload.
        Assert.Equal("hello", config["test:key"]);
    }

    [Fact]
    public async Task Writer_ClearAsync_RemovesKeyAndReloads()
    {
        var userPath = WriteTempJson("""{"test": {"key": "value"}}""");
        var (writer, config) = BuildWriter(userPath, null);

        Assert.Equal("value", config["test:key"]);

        await writer.ClearAsync("test.key", ConfigurationScope.User);

        Assert.Null(config["test:key"]);
    }

    [Fact]
    public async Task Writer_ScopeMerge_WorkspaceOverridesUser()
    {
        var userPath = WriteTempJson("""{"test": {"value": "from-user"}}""");
        var workspacePath = WriteTempJson("{}");
        var (writer, config) = BuildWriter(userPath, workspacePath);

        Assert.Equal("from-user", config["test:value"]);

        // Write to workspace scope.
        var wsValue = JsonSerializer.SerializeToElement("from-workspace", SettingsJsonContext.Default.String);
        await writer.SetAsync("test.value", wsValue, ConfigurationScope.Workspace);

        // Workspace should override user.
        Assert.Equal("from-workspace", config["test:value"]);

        // Clear workspace — user value surfaces again.
        await writer.ClearAsync("test.value", ConfigurationScope.Workspace);
        Assert.Equal("from-user", config["test:value"]);
    }

    [Fact]
    public async Task Writer_Get_ReturnsTypedJsonElement()
    {
        var userPath = WriteTempJson("""{"ns": {"flag": true, "name": "alice"}}""");
        var (writer, _) = BuildWriter(userPath, null);

        var flag = writer.Get("ns.flag");
        Assert.NotNull(flag);
        Assert.Equal(JsonValueKind.True, flag.Value.ValueKind);

        var name = writer.Get("ns.name");
        Assert.NotNull(name);
        Assert.Equal(JsonValueKind.String, name.Value.ValueKind);
        Assert.Equal("alice", name.Value.GetString());
    }

    [Fact]
    public void Writer_Get_MissingKey_ReturnsNull()
    {
        var userPath = WriteTempJson("{}");
        var (writer, _) = BuildWriter(userPath, null);

        Assert.Null(writer.Get("nonexistent.key"));
    }

    [Fact]
    public async Task Writer_Validate_UnknownKey_Tolerated()
    {
        // Unknown keys should not cause errors — they may come from
        // extensions that are no longer installed.
        var userPath = WriteTempJson("{}");
        var registry = new SettingsSchemaRegistry();
        var writer = BuildWriterWithRegistry(registry, userPath, null);

        var stringValue = JsonSerializer.SerializeToElement("anything", SettingsJsonContext.Default.String);

        // Should not throw.
        await writer.SetAsync("unknown.key", stringValue, ConfigurationScope.User);
    }

    // ─── Writer: Round-trip type fidelity ───────────────────────────

    [Fact]
    public async Task Writer_SetAsync_BoolValue_RoundTripsWithCorrectType()
    {
        // Verifies that a boolean written via SetAsync is read back as a
        // boolean JsonElement, not as a string — the writer must preserve
        // JSON types through the file round-trip.
        var userPath = WriteTempJson("{}");
        var (writer, _) = BuildWriter(userPath, null);

        var boolValue = JsonSerializer.SerializeToElement(true, SettingsJsonContext.Default.Boolean);
        await writer.SetAsync("test.flag", boolValue, ConfigurationScope.User);

        var result = writer.Get("test.flag");
        Assert.NotNull(result);
        Assert.Equal(JsonValueKind.True, result.Value.ValueKind);
    }

    // ─── Writer: Namespace cleanup on clear ──────────────────────────

    [Fact]
    public async Task Writer_ClearAsync_RemovesEmptyNamespaceFromFile()
    {
        // When the last key in a namespace is cleared, the namespace object
        // itself should be removed from the file for cleanliness.
        var userPath = WriteTempJson("""{"test": {"only-key": "value"}}""");
        var (writer, _) = BuildWriter(userPath, null);

        await writer.ClearAsync("test.only-key", ConfigurationScope.User);

        // The file should not contain an empty "test" object.
        var json = File.ReadAllText(userPath);
        using var doc = JsonDocument.Parse(json);
        Assert.False(doc.RootElement.TryGetProperty("test", out _),
            "Expected the 'test' namespace to be removed after its last key was cleared");
    }

    // ─── UrOptionsMonitor direct tests ───────────────────────────────

    [Fact]
    public void OptionsMonitor_ReadsModelFromUrSection()
    {
        var userPath = WriteTempJson("""{"ur": {"model": "openai/gpt-4o"}}""");
        var config = BuildConfig(userPath, null);
        var monitor = new UrOptionsMonitor(config);

        Assert.Equal("openai/gpt-4o", monitor.CurrentValue.Model);
    }

    [Fact]
    public void OptionsMonitor_NullWhenModelNotSet()
    {
        var userPath = WriteTempJson("{}");
        var config = BuildConfig(userPath, null);
        var monitor = new UrOptionsMonitor(config);

        Assert.Null(monitor.CurrentValue.Model);
    }

    [Fact]
    public async Task OptionsMonitor_ReflectsReloadAfterWrite()
    {
        // Verifies that UrOptionsMonitor sees the new value after
        // SettingsWriter writes and reloads the IConfigurationRoot.
        var userPath = WriteTempJson("{}");
        var config = BuildConfig(userPath, null);
        var monitor = new UrOptionsMonitor(config);
        var writer = new SettingsWriter(new SettingsSchemaRegistry(), config, userPath, null);

        Assert.Null(monitor.CurrentValue.Model);

        var modelValue = JsonSerializer.SerializeToElement("anthropic/claude-3", SettingsJsonContext.Default.String);
        await writer.SetAsync("ur.model", modelValue, ConfigurationScope.User);

        Assert.Equal("anthropic/claude-3", monitor.CurrentValue.Model);
    }

    // ─── Helpers ──────────────────────────────────────────────────────

    private string WriteTempJson(string json)
    {
        var path = Path.Combine(_root, $"{Guid.NewGuid():N}.json");
        File.WriteAllText(path, json);
        return path;
    }

    private static IConfigurationRoot BuildConfig(string? userPath, string? workspacePath)
    {
        var builder = new ConfigurationBuilder();
        builder.Add(new UrSettingsConfigurationSource(userPath));
        if (workspacePath is not null)
            builder.Add(new UrSettingsConfigurationSource(workspacePath));
        return builder.Build();
    }

    private static (SettingsWriter Writer, IConfigurationRoot Config) BuildWriter(
        string? userPath, string? workspacePath)
    {
        var registry = new SettingsSchemaRegistry();
        var config = BuildConfig(userPath, workspacePath);
        var writer = new SettingsWriter(registry, config, userPath, workspacePath);
        return (writer, config);
    }

    private static SettingsWriter BuildWriterWithRegistry(
        SettingsSchemaRegistry registry,
        string? userPath,
        string? workspacePath)
    {
        var config = BuildConfig(userPath, workspacePath);
        return new SettingsWriter(registry, config, userPath, workspacePath);
    }

    private static SettingsSchemaRegistry BuildRegistryWithBoolSchema(string key)
    {
        var registry = new SettingsSchemaRegistry();
        using var doc = JsonDocument.Parse("""{"type": "boolean"}""");
        registry.Register(key, doc.RootElement.Clone());
        return registry;
    }
}
