using dotenv.net;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Ox.App;
using Ox.App.Configuration;
using Ox.Agent.AgentLoop;
using Ox.Agent.Configuration.Keyring;
using Ox.Agent.Hosting;
using Xunit.Abstractions;

namespace Ox.IntegrationTests;

/// <summary>
/// Smoke tests for each provider — sends a simple prompt and verifies a streaming
/// response comes back. These require live API keys (in .env or environment) and
/// a running Ollama instance.
///
/// Gated by the OX_RUN_PROVIDER_SMOKE_TESTS=1 environment variable so they don't
/// run in CI by default.
/// </summary>
public class MultiProviderSmokeTests : IDisposable
{
    private const string RunSmokeEnvVar = "OX_RUN_PROVIDER_SMOKE_TESTS";

    private readonly ITestOutputHelper _output;
    private readonly string _workspacePath;
    private readonly string _userDataDir;

    public MultiProviderSmokeTests(ITestOutputHelper output)
    {
        _output = output;
        DotEnv.Load(options: new DotEnvOptions(probeForEnv: true, probeLevelsToSearch: 8));

        // Create isolated temp directories for the host.
        var root = Path.Combine(Path.GetTempPath(), "ox-smoke-tests", Guid.NewGuid().ToString("N"));
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
        host.Configuration.SetSelectedModel("google/gemini-3-flash-preview");

        await VerifyStreamingResponse(host, _output);
    }

    [Fact]
    public async Task OpenAi_Gpt5Nano_StreamsResponse()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunSmokeEnvVar}=1 to run."); return; }

        var apiKey = Environment.GetEnvironmentVariable("OPENAI_API_KEY");
        if (apiKey is null) { _output.WriteLine("OPENAI_API_KEY not set. Skipping."); return; }

        var host = await CreateHostAsync("openai", apiKey);
        host.Configuration.SetSelectedModel("openai/gpt-5-nano");

        await VerifyStreamingResponse(host, _output);
    }

    [Fact]
    public async Task Ollama_Qwen3_4b_StreamsResponse()
    {
        if (!ShouldRun()) { _output.WriteLine($"Set {RunSmokeEnvVar}=1 to run."); return; }

        var host = await CreateHostAsync("ollama", apiKey: null);
        host.Configuration.SetSelectedModel("ollama/qwen3:4b");

        await VerifyStreamingResponse(host, _output);
    }

    // ─── Helpers ─────────────────────────────────────────────────────

    private async Task<OxHost> CreateHostAsync(string providerName, string? apiKey)
    {
        var keyring = new InMemoryKeyring();
        if (apiKey is not null)
            keyring.SetSecret("ox", providerName, apiKey);

        var builder = Host.CreateApplicationBuilder();

        var userSettingsPath = Path.Combine(_userDataDir, "settings.json");
        OxServices.AddSettingsSources(
            builder.Configuration,
            userSettingsPath,
            Path.Combine(_workspacePath, ".ox", "settings.json"));

        // Load providers.json from the default location (~/.ur/providers.json)
        // and register providers — same as Ox's production Program.cs.
        var defaultUserDataDir = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".ox");
        var providersJsonPath = Path.Combine(defaultUserDataDir, "providers.json");
        var providerConfig = ProviderConfig.Load(providersJsonPath);
        builder.Services.AddSingleton(providerConfig);
        builder.Services.AddProvidersFromConfig(providerConfig);
        builder.Services.AddSingleton<ModelCatalog>();
        builder.Services.AddSingleton<Func<string, int?>>(sp =>
            sp.GetRequiredService<ModelCatalog>().ResolveContextWindow);

        builder.Services.AddSingleton<IKeyring>(keyring);
        OxServices.Register(builder.Services, builder.Configuration, o =>
        {
            o.WorkspacePath = _workspacePath;
            o.UserDataDirectory = _userDataDir;
            o.UserSettingsPath = userSettingsPath;
        });

        var app = builder.Build();
        await app.StartAsync();
        return app.Services.GetRequiredService<OxHost>();
    }

    /// <summary>
    /// Sends a trivial prompt through the full OxHost → provider → SDK pipeline
    /// and verifies that at least one streaming response chunk arrives.
    /// </summary>
    private static async Task VerifyStreamingResponse(OxHost host, ITestOutputHelper output)
    {
        var session = host.CreateSession();
        var gotText = false;

        await foreach (var evt in session.RunTurnAsync("Say hello in exactly one word."))
        {
            if (evt is ResponseChunk chunk)
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
    /// in Ox.Tests but duplicated here to avoid cross-project dependencies.
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
