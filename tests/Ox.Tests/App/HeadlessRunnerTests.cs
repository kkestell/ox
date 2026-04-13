using Ox.App;
using System.Text.Json;
using Ox;
using Ox.Agent.Hosting;
using Ox.Agent.Providers.Fake;
using Ox.Tests.TestSupport;

namespace Ox.Tests.App;

/// <summary>
/// Tests for <see cref="HeadlessRunner"/> behaviour, focusing on the
/// <c>maxIterations</c> cap. Uses the fake provider so all scenarios
/// run deterministically without external API calls.
///
/// The <c>tool-call</c> fake scenario is 2 AgentLoop iterations per user turn
/// (one LLM call that issues a tool call, one that gives the final response),
/// which makes it the right vehicle for iteration-cap tests. The <c>hello</c>
/// scenario is 1 iteration (direct text response) and is used to verify the
/// happy path where no cap is hit.
///
/// Behaviour is verified via the metrics JSON that OxSession writes on dispose,
/// avoiding Console.SetOut tricks and testing the observable effect directly.
/// </summary>
public sealed class HeadlessRunnerTests
{
    private static async Task<OxHost> CreateFakeHostAsync(TempWorkspace workspace, string scenario)
    {
        return await TestHostBuilder.CreateHostAsync(
            workspace,
            fakeProvider: new FakeProvider(),
            selectedModelOverride: $"fake/{scenario}");
    }

    /// <summary>
    /// Reads the <c>turns</c> field from the metrics JSON that OxSession writes
    /// after HeadlessRunner completes. There should be exactly one metrics file
    /// in the sessions directory per run.
    /// </summary>
    private static int ReadTurnCount(TempWorkspace workspace)
    {
        var sessionsDir = Path.Combine(workspace.WorkspacePath, ".ox", "sessions");
        var metricsFiles = Directory.GetFiles(sessionsDir, "*.metrics.json");
        Assert.Single(metricsFiles); // exactly one run per test

        var json = File.ReadAllText(metricsFiles[0]);
        using var doc = JsonDocument.Parse(json);
        return doc.RootElement.GetProperty("turns").GetInt32();
    }

    /// <summary>
    /// Reads the <c>error</c> field from the metrics JSON. Non-null when the
    /// session ended with a fatal TurnError.
    /// </summary>
    private static string? ReadErrorField(TempWorkspace workspace)
    {
        var sessionsDir = Path.Combine(workspace.WorkspacePath, ".ox", "sessions");
        var metricsFiles = Directory.GetFiles(sessionsDir, "*.metrics.json");
        Assert.Single(metricsFiles);

        var json = File.ReadAllText(metricsFiles[0]);
        using var doc = JsonDocument.Parse(json);
        var errorProp = doc.RootElement.GetProperty("error");
        return errorProp.ValueKind == JsonValueKind.Null ? null : errorProp.GetString();
    }

    [Fact]
    public async Task RunAsync_NoMaxIterations_SinglePromptCompletes()
    {
        // hello scenario: 1 LLM call (no tool calls), no cap → completes normally.
        // metrics.Turns == 1 because one RunTurnAsync call fired TurnCompleted.
        using var workspace = new TempWorkspace();
        var host = await CreateFakeHostAsync(workspace, "hello");

        var runner = new HeadlessRunner(
            host,
            prompt: "hello",
            yolo: true,
            maxIterations: null);

        Console.SetOut(TextWriter.Null);
        try { await runner.RunAsync(CancellationToken.None); }
        finally { Console.SetOut(new StreamWriter(Console.OpenStandardOutput()) { AutoFlush = true }); }

        Assert.Equal(1, ReadTurnCount(workspace));
    }

    [Fact]
    public async Task RunAsync_MaxIterationsNotReached_Completes()
    {
        // tool-call scenario: 2 LLM calls per turn. Cap set to 3 → well above the
        // scenario's actual iteration count. The turn completes normally.
        using var workspace = new TempWorkspace();
        var host = await CreateFakeHostAsync(workspace, "tool-call");

        var runner = new HeadlessRunner(
            host,
            prompt: "do the thing",
            yolo: true,
            maxIterations: 3);

        Console.SetOut(TextWriter.Null);
        int exitCode;
        try { exitCode = await runner.RunAsync(CancellationToken.None); }
        finally { Console.SetOut(new StreamWriter(Console.OpenStandardOutput()) { AutoFlush = true }); }

        // Exit 0: cap not hit, turn completed without error.
        Assert.Equal(0, exitCode);
        Assert.Equal(1, ReadTurnCount(workspace));
    }

    [Fact]
    public async Task RunAsync_MaxIterationsAtBoundary_Completes()
    {
        // tool-call scenario needs exactly 2 LLM calls. Cap set to 2 — the exact
        // number needed. This exercises the boundary between "cap not reached" and
        // "cap exceeded": ++iterationCount (2) > maxIterations (2) is false, so the
        // second call runs and the turn completes normally.
        using var workspace = new TempWorkspace();
        var host = await CreateFakeHostAsync(workspace, "tool-call");

        var runner = new HeadlessRunner(
            host,
            prompt: "do the thing",
            yolo: true,
            maxIterations: 2);

        Console.SetOut(TextWriter.Null);
        int exitCode;
        try { exitCode = await runner.RunAsync(CancellationToken.None); }
        finally { Console.SetOut(new StreamWriter(Console.OpenStandardOutput()) { AutoFlush = true }); }

        // Exit 0: exactly 2 iterations used, cap of 2 not exceeded.
        Assert.Equal(0, exitCode);
        Assert.Equal(1, ReadTurnCount(workspace));
    }

    [Fact]
    public async Task RunAsync_MaxIterationsExceeded_ReturnsError()
    {
        // tool-call scenario needs 2 LLM calls. Cap at 1 → the cap fires after the
        // first call (the tool-call response) before the second call can produce the
        // final text. AgentLoop yields a fatal TurnError and breaks.
        using var workspace = new TempWorkspace();
        var host = await CreateFakeHostAsync(workspace, "tool-call");

        var runner = new HeadlessRunner(
            host,
            prompt: "do the thing",
            yolo: true,
            maxIterations: 1);

        Console.SetOut(TextWriter.Null);
        int exitCode;
        try { exitCode = await runner.RunAsync(CancellationToken.None); }
        finally { Console.SetOut(new StreamWriter(Console.OpenStandardOutput()) { AutoFlush = true }); }

        // Exit 1: fatal TurnError from the iteration cap.
        Assert.Equal(1, exitCode);
        // The error message is recorded in metrics so callers can diagnose cap hits.
        Assert.Contains("iteration limit", ReadErrorField(workspace), StringComparison.OrdinalIgnoreCase);
    }
}
