using System.Text.Json;
using Ox;
using Ur.Hosting;
using Ur.Providers.Fake;
using Ur.Tests.TestSupport;

namespace Ur.Tests;

/// <summary>
/// Tests for <see cref="HeadlessRunner"/> behaviour, specifically the
/// <c>maxTurns</c> cap. Uses the fake provider with the hello scenario
/// so each user turn completes deterministically without external API calls.
///
/// Behaviour is verified by reading the session metrics file that UrSession
/// writes on dispose — this avoids Console.SetOut tricks and tests the
/// observable effect directly.
/// </summary>
public sealed class HeadlessRunnerTests
{
    private static async Task<UrHost> CreateFakeHostAsync(TempWorkspace workspace, string scenario)
    {
        return await TestHostBuilder.CreateHostAsync(
            workspace,
            fakeProvider: new FakeProvider(),
            selectedModelOverride: $"fake/{scenario}");
    }

    /// <summary>
    /// Reads the <c>turns</c> field from the metrics JSON that UrSession writes
    /// after HeadlessRunner completes. There should be exactly one metrics file
    /// in the sessions directory per run.
    /// </summary>
    private static int ReadTurnCount(TempWorkspace workspace)
    {
        var sessionsDir = Path.Combine(workspace.WorkspacePath, ".ur", "sessions");
        var metricsFiles = Directory.GetFiles(sessionsDir, "*.metrics.json");
        Assert.Single(metricsFiles); // exactly one run per test

        var json = File.ReadAllText(metricsFiles[0]);
        using var doc = JsonDocument.Parse(json);
        return doc.RootElement.GetProperty("turns").GetInt32();
    }

    [Fact]
    public async Task RunAsync_NoMaxTurns_ProcessesAllProvidedTurns()
    {
        // 3 user messages with no cap → all 3 should complete.
        using var workspace = new TempWorkspace();
        var host = await CreateFakeHostAsync(workspace, "hello");

        var runner = new HeadlessRunner(
            host,
            turns: ["first", "second", "third"],
            yolo: true,
            maxTurns: null);

        // Suppress output — we only care about metrics.
        Console.SetOut(TextWriter.Null);
        try { await runner.RunAsync(CancellationToken.None); }
        finally { Console.SetOut(new StreamWriter(Console.OpenStandardOutput()) { AutoFlush = true }); }

        Assert.Equal(3, ReadTurnCount(workspace));
    }

    [Fact]
    public async Task RunAsync_MaxTurnsBelowCount_StopsAfterLimit()
    {
        // Cap at 2 with 3 turns provided — only 2 should run and the metrics
        // file should record turns = 2.
        using var workspace = new TempWorkspace();
        var host = await CreateFakeHostAsync(workspace, "hello");

        var runner = new HeadlessRunner(
            host,
            turns: ["first", "second", "third"],
            yolo: true,
            maxTurns: 2);

        Console.SetOut(TextWriter.Null);
        try { await runner.RunAsync(CancellationToken.None); }
        finally { Console.SetOut(new StreamWriter(Console.OpenStandardOutput()) { AutoFlush = true }); }

        Assert.Equal(2, ReadTurnCount(workspace));
    }

    [Fact]
    public async Task RunAsync_MaxTurnsOne_ProcessesExactlyOneTurn()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateFakeHostAsync(workspace, "hello");

        var runner = new HeadlessRunner(
            host,
            turns: ["first", "second", "third"],
            yolo: true,
            maxTurns: 1);

        Console.SetOut(TextWriter.Null);
        try { await runner.RunAsync(CancellationToken.None); }
        finally { Console.SetOut(new StreamWriter(Console.OpenStandardOutput()) { AutoFlush = true }); }

        Assert.Equal(1, ReadTurnCount(workspace));
    }

    [Fact]
    public async Task RunAsync_MaxTurnsExceedsCount_ProcessesAll()
    {
        // A generous cap (10) does not artificially stop a 3-turn run.
        using var workspace = new TempWorkspace();
        var host = await CreateFakeHostAsync(workspace, "hello");

        var runner = new HeadlessRunner(
            host,
            turns: ["first", "second", "third"],
            yolo: true,
            maxTurns: 10);

        Console.SetOut(TextWriter.Null);
        try { await runner.RunAsync(CancellationToken.None); }
        finally { Console.SetOut(new StreamWriter(Console.OpenStandardOutput()) { AutoFlush = true }); }

        Assert.Equal(3, ReadTurnCount(workspace));
    }

    [Fact]
    public async Task RunAsync_MaxTurnsWithEmptyTurnsList_RunsNothing()
    {
        // An empty turns list with a maxTurns cap is safe — Take(n) on an empty
        // sequence is a no-op. No session is persisted, so no metrics file is written.
        // In production, Program.cs rejects empty turns before creating HeadlessRunner;
        // this test documents that HeadlessRunner itself also handles it gracefully.
        using var workspace = new TempWorkspace();
        var host = await CreateFakeHostAsync(workspace, "hello");

        var runner = new HeadlessRunner(
            host,
            turns: [],
            yolo: true,
            maxTurns: 5);

        Console.SetOut(TextWriter.Null);
        int exitCode;
        try { exitCode = await runner.RunAsync(CancellationToken.None); }
        finally { Console.SetOut(new StreamWriter(Console.OpenStandardOutput()) { AutoFlush = true }); }

        // Exit 0: no turns means no fatal error.
        Assert.Equal(0, exitCode);

        // No session was persisted, so no metrics file should be written.
        var sessionsDir = Path.Combine(workspace.WorkspacePath, ".ur", "sessions");
        var metricsFiles = Directory.Exists(sessionsDir)
            ? Directory.GetFiles(sessionsDir, "*.metrics.json")
            : [];
        Assert.Empty(metricsFiles);
    }
}
