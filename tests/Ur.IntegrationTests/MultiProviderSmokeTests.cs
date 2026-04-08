using dotenv.net;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Ur.Configuration.Keyring;
using Ur.Hosting;
using Xunit.Abstractions;

namespace Ur.IntegrationTests;

/// <summary>
/// Smoke tests for each provider — sends a simple prompt and verifies a streaming
/// response comes back. These require live API keys (in .env or environment) and
/// a running Ollama instance.
///
/// Gated by the UR_RUN_PROVIDER_SMOKE_TESTS=1 environment variable so they don't
/// run in CI by default.
/// </summary>
public class MultiProviderSmokeTests : IDisposable
{
    private const string RunSmokeEnvVar = "UR_RUN_PROVIDER_SMOKE_TESTS";

    private readonly ITestOutputHelper _output;
    private readonly string _workspacePath;
    private readonly string _userDataDir;

    public MultiProviderSmokeTests(ITestOutputHelper output)
    {
        _output = output;
        DotEnv.Load(options: new DotEnvOptions(probeForEnv: true, probeLevelsToSearch: 8));

        // Create isolated temp directories for the host.
        var root = Path.Combine(Path.GetTempPath(), "ur-smoke-tests", Guid.NewGuid().ToString("N"));
        _workspacePath = Path.Combine(root, "workspace");
        _userDataDir = Path.Combine(root, "user-data");
        Directory.CreateDirectory(_workspacePath);
        Directory.CreateDirectory(_userDataDir);
    }

    public void Dispose()
    {
        var root = Path.GetDirectoryName(_workspacePath)!;
        if (Directory.Exists(root))
            Directory.Delete(root, recursive: true);
    }

    [Fact]
    public async Task Google_GeminiFlashPreview_StreamsResponse()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunSmokeEnvVar}=1 to run."); return; }

        var apiKey = Environment.GetEnvironmentVariable("GOOGLE_API_KEY");
        if (apiKey is null) { _output.WriteLine("GOOGLE_API_KEY not set. Skipping."); return; }

        var host = await CreateHostAsync("google", apiKey);
        await host.Configuration.SetSelectedModelAsync("google/gemini-3-flash-preview");

        await VerifyStreamingResponse(host, _output);
    }

    [Fact]
    public async Task OpenAi_Gpt5Nano_StreamsResponse()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunSmokeEnvVar}=1 to run."); return; }

        var apiKey = Environment.GetEnvironmentVariable("OPENAI_API_KEY");
        if (apiKey is null) { _output.WriteLine("OPENAI_API_KEY not set. Skipping."); return; }

        var host = await CreateHostAsync("openai", apiKey);
        await host.Configuration.SetSelectedModelAsync("openai/gpt-5-nano");

        await VerifyStreamingResponse(host, _output);
    }

    [Fact]
    public async Task Ollama_Qwen3_4b_StreamsResponse()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunSmokeEnvVar}=1 to run."); return; }

        var host = await CreateHostAsync("ollama", apiKey: null);
        await host.Configuration.SetSelectedModelAsync("ollama/qwen3:4b");

        await VerifyStreamingResponse(host, _output);
    }

    // ─── Helpers ─────────────────────────────────────────────────────

    private async Task<UrHost> CreateHostAsync(string providerName, string? apiKey)
    {
        var keyring = new InMemoryKeyring();
        if (apiKey is not null)
            keyring.SetSecret("ur", providerName, apiKey);

        var builder = Host.CreateApplicationBuilder();
        builder.Services.AddUr(new UrStartupOptions
        {
            WorkspacePath = _workspacePath,
            UserDataDirectory = _userDataDir,
            UserSettingsPath = Path.Combine(_userDataDir, "settings.json"),
            KeyringOverride = keyring,
        });

        var app = builder.Build();
        await app.StartAsync();
        return app.Services.GetRequiredService<UrHost>();
    }

    /// <summary>
    /// Sends a trivial prompt through the full UrHost → provider → SDK pipeline
    /// and verifies that at least one streaming response chunk arrives.
    /// </summary>
    private static async Task VerifyStreamingResponse(UrHost host, ITestOutputHelper output)
    {
        var session = host.CreateSession();
        var gotText = false;

        await foreach (var evt in session.RunTurnAsync("Say hello in exactly one word."))
        {
            if (evt is Ur.AgentLoop.ResponseChunk chunk)
            {
                output.WriteLine($"chunk: {chunk.Text}");
                gotText = true;
            }
        }

        Assert.True(gotText, "Expected at least one text chunk from the provider.");
    }

    private static bool ShouldRun() =>
        string.Equals(
            Environment.GetEnvironmentVariable(RunSmokeEnvVar),
            "1",
            StringComparison.Ordinal);

    /// <summary>
    /// Simple in-memory keyring for integration tests. Same as the TestKeyring
    /// in Ur.Tests but duplicated here to avoid cross-project dependencies.
    /// </summary>
    private sealed class InMemoryKeyring : IKeyring
    {
        private readonly Dictionary<(string, string), string> _secrets = new();

        public string? GetSecret(string service, string account) =>
            _secrets.GetValueOrDefault((service, account));

        public void SetSecret(string service, string account, string secret) =>
            _secrets[(service, account)] = secret;

        public void DeleteSecret(string service, string account) =>
            _secrets.Remove((service, account));
    }
}
