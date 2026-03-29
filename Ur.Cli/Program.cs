using dotenv.net;
using Microsoft.Extensions.AI;
using Ur;
using Ur.Cli;

DotEnv.Load(options: new DotEnvOptions(
    probeForEnv: true,
    probeLevelsToSearch: 8));

var endpoints = new Dictionary<string, Uri>
{
    ["openai"] = new("https://api.openai.com/v1"),
    ["openrouter"] = new("https://openrouter.ai/api/v1"),
};

var factory = new OpenAIChatClientFactory(endpoints);
var host = UrHost.Start(Environment.CurrentDirectory, factory);

Console.WriteLine($"ur — {host.Workspace.RootPath}");

// Quick smoke test: if an API key is available, do a single exchange.
var apiKey = Environment.GetEnvironmentVariable("OPENROUTER_API_KEY");
if (apiKey is not null)
{
    // Register a model so CreateChatClient can find it
    host.ProviderRegistry.AddProvider(new Ur.Providers.ProviderDefinition
    {
        Id = "openrouter",
        DisplayName = "OpenRouter",
        ModelIds = ["anthropic/claude-sonnet-4"],
    });
    host.ProviderRegistry.AddModel(new Ur.Providers.ModelDefinition
    {
        Id = "anthropic/claude-sonnet-4",
        ProviderId = "openrouter",
        Properties = new Ur.Providers.ModelProperties(
            MaxContextLength: 200_000,
            MaxOutputLength: 8_192,
            CostPerInputToken: 0.000003m,
            CostPerOutputToken: 0.000015m,
            SupportsToolCalling: true,
            SupportsStreaming: true),
        SettingsSchema = System.Text.Json.JsonDocument.Parse("{}").RootElement.Clone(),
    });

    var client = host.CreateChatClient("anthropic/claude-sonnet-4", apiKey);
    Console.WriteLine("Sending test message...");

    await foreach (var update in client.GetStreamingResponseAsync("Say hello in one sentence."))
    {
        Console.Write(update.Text);
    }
    Console.WriteLine();
}
else
{
    Console.WriteLine("Set OPENROUTER_API_KEY to test LLM interaction.");
}
