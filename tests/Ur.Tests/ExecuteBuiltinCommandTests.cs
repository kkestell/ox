using Ur.Hosting;
using Ur.Tests.TestSupport;

namespace Ur.Tests;

/// <summary>
/// Tests for <see cref="Sessions.UrSession.ExecuteBuiltInCommand"/>.
///
/// Uses a real host (constructing via <see cref="TestHostBuilder"/>) so that
/// the full configuration and built-in registry are wired up exactly as in
/// production. The test providers.json is written by
/// <see cref="TestProviderConfig.DefaultJson"/> and contains known model IDs.
/// </summary>
public sealed class ExecuteBuiltinCommandTests
{
    // One of the model IDs registered in TestProviderConfig.DefaultJson.
    private const string KnownModelId = "openai/gpt-4o";

    // ─── Helpers ──────────────────────────────────────────────────────

    private static async Task<UrHost> CreateHostAsync(TempWorkspace workspace) =>
        await TestHostBuilder.CreateHostAsync(workspace);

    // ─── Tests ────────────────────────────────────────────────────────

    [Fact]
    public async Task ExecuteBuiltInCommand_ValidModel_SetsModelAndReturnsSuccess()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);
        var session = host.CreateSession();

        // The model ID is from the test providers.json so it is recognized as valid.
        var result = session.ExecuteBuiltInCommand("model", KnownModelId);

        Assert.NotNull(result);
        Assert.False(result.IsError);
        // The exact message is important — it's what the user sees in [info] confirmation.
        Assert.Equal($"Model set to {KnownModelId}.", result.Message);

        // Verify persistence: the model was actually written to the settings file.
        Assert.Equal(KnownModelId, host.Configuration.SelectedModelId);
    }

    [Fact]
    public async Task ExecuteBuiltInCommand_Clear_ReturnsNotImplementedError()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);
        var session = host.CreateSession();

        var result = session.ExecuteBuiltInCommand("clear", null);

        Assert.NotNull(result);
        Assert.True(result.IsError);
        Assert.Contains("clear", result.Message);
    }

    [Fact]
    public async Task ExecuteBuiltInCommand_Set_ReturnsNotImplementedError()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);
        var session = host.CreateSession();

        var result = session.ExecuteBuiltInCommand("set", null);

        Assert.NotNull(result);
        Assert.True(result.IsError);
        Assert.Contains("set", result.Message);
    }

    [Fact]
    public async Task ExecuteBuiltInCommand_UnrecognizedCommand_ReturnsNull()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);
        var session = host.CreateSession();

        // "nonexistent" is not registered in BuiltInCommandRegistry — must return null.
        var result = session.ExecuteBuiltInCommand("nonexistent", null);

        Assert.Null(result);
    }

    [Fact]
    public async Task ExecuteBuiltInCommand_CaseInsensitiveCommandName()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);
        var session = host.CreateSession();

        // Command names are case-insensitive: "Model" and "model" are the same built-in.
        var result = session.ExecuteBuiltInCommand("Model", KnownModelId);

        Assert.NotNull(result);
        Assert.False(result.IsError);
    }
}
