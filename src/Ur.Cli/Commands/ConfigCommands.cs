using System.CommandLine;
using System.Text.Json;
using Ur.Configuration;

namespace Ur.Cli.Commands;

/// <summary>
/// `ur config *` — manage workspace and user-level configuration.
///
/// Subcommands:
///   set-api-key &lt;key&gt; [--provider]        store an API key in the system keyring
///   clear-api-key [--provider]             delete a stored API key
///   set-model &lt;model-id&gt; [--scope]        choose a default model
///   clear-model [--scope]                  remove the model preference for the given scope
///   get &lt;key&gt;                             print the merged JSON value for an arbitrary setting
///   set &lt;key&gt; &lt;value&gt; [--scope]          write a raw JSON value for an arbitrary setting
///   clear &lt;key&gt; [--scope]                 remove an arbitrary setting from the given scope
///
/// The --scope flag is accepted by commands that write settings and determines whether the
/// change is persisted to the user-level or workspace-level settings file.
/// </summary>
internal static class ConfigCommands
{
    public static Command Build()
    {
        var config = new Command("config", "Read and write configuration settings")
        {
            BuildSetApiKey(),
            BuildClearApiKey(),
            BuildSetModel(),
            BuildClearModel(),
            BuildGet(),
            BuildSet(),
            BuildClear()
        };

        return config;
    }

    // -------------------------------------------------------------------------
    // ur config set-api-key <key> [--provider <name>]
    // -------------------------------------------------------------------------

    private static Command BuildSetApiKey()
    {
        var keyArg = new Argument<string>("key")
        {
            Description = "API key for the provider"
        };

        var providerOpt = new Option<string>("--provider", "-p")
        {
            Description = "Provider name (openrouter, openai, google). Defaults to openrouter.",
            DefaultValueFactory = _ => "openrouter"
        };

        var cmd = new Command("set-api-key", "Store an API key in the system keyring") { keyArg, providerOpt };

        cmd.SetAction(async (parseResult, cancellationToken) =>
            await HostRunner.RunAsync(async (host, _) =>
            {
                var key = parseResult.GetValue(keyArg)!;
                var provider = parseResult.GetValue(providerOpt)!;
                await host.Configuration.SetApiKeyAsync(key, provider, CancellationToken.None);
                Console.WriteLine($"API key saved for '{provider}'.");
                return 0;
            }, cancellationToken));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // ur config clear-api-key [--provider <name>]
    // -------------------------------------------------------------------------

    private static Command BuildClearApiKey()
    {
        var providerOpt = new Option<string>("--provider", "-p")
        {
            Description = "Provider name (openrouter, openai, google). Defaults to openrouter.",
            DefaultValueFactory = _ => "openrouter"
        };

        var cmd = new Command("clear-api-key", "Remove a stored API key") { providerOpt };

        cmd.SetAction(async (parseResult, cancellationToken) =>
            await HostRunner.RunAsync(async (host, _) =>
            {
                var provider = parseResult.GetValue(providerOpt)!;
                await host.Configuration.ClearApiKeyAsync(provider, CancellationToken.None);
                Console.WriteLine($"API key cleared for '{provider}'.");
                return 0;
            }, cancellationToken));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // ur config set-model <model-id> [--scope user|workspace]
    // -------------------------------------------------------------------------

    private static Command BuildSetModel()
    {
        var modelArg = new Argument<string>("model-id")
        {
            Description = "Model identifier (e.g. openai/gpt-4o)"
        };
        var scopeOpt = ScopeOption();

        var cmd = new Command("set-model", "Set the default model") { modelArg, scopeOpt };

        cmd.SetAction(async (parseResult, cancellationToken) =>
            await HostRunner.RunAsync(async (host, _) =>
            {
                var modelId = parseResult.GetValue(modelArg)!;
                var scope    = ParseScope(parseResult.GetValue(scopeOpt));
                await host.Configuration.SetSelectedModelAsync(modelId, scope, CancellationToken.None);
                Console.WriteLine($"Model set to \"{modelId}\" ({scope.ToString().ToLowerInvariant()}).");
                return 0;
            }, cancellationToken));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // ur config clear-model [--scope user|workspace]
    // -------------------------------------------------------------------------

    private static Command BuildClearModel()
    {
        var scopeOpt = ScopeOption();

        var cmd = new Command("clear-model", "Clear the selected model for a scope") { scopeOpt };

        cmd.SetAction(async (parseResult, cancellationToken) =>
            await HostRunner.RunAsync(async (host, _) =>
            {
                var scope = ParseScope(parseResult.GetValue(scopeOpt));
                await host.Configuration.ClearSelectedModelAsync(scope, CancellationToken.None);
                Console.WriteLine($"Model selection cleared ({scope.ToString().ToLowerInvariant()}).");
                return 0;
            }, cancellationToken));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // ur config get <key>
    // -------------------------------------------------------------------------

    private static Command BuildGet()
    {
        var keyArg = new Argument<string>("key")
        {
            Description = "Dot-separated settings key (e.g. ur.model)"
        };

        var cmd = new Command("get", "Get the merged value for a settings key") { keyArg };

        cmd.SetAction(async (parseResult, cancellationToken) =>
            await HostRunner.RunAsync((host, _) =>
            {
                var key   = parseResult.GetValue(keyArg)!;
                var value = host.Configuration.GetSetting(key);

                Console.WriteLine(value is null ? "(not set)" : value.Value.ToString());

                return Task.FromResult(0);
            }, cancellationToken));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // ur config set <key> <value> [--scope user|workspace]
    // -------------------------------------------------------------------------

    private static Command BuildSet()
    {
        var keyArg   = new Argument<string>("key")   { Description = "Dot-separated settings key" };
        var valueArg = new Argument<string>("value") { Description = "JSON value to store" };
        var scopeOpt = ScopeOption();

        var cmd = new Command("set", "Write a raw JSON value for a settings key") { keyArg, valueArg, scopeOpt };

        cmd.SetAction(async (parseResult, cancellationToken) =>
            await HostRunner.RunAsync(async (host, _) =>
            {
                var key   = parseResult.GetValue(keyArg)!;
                var raw   = parseResult.GetValue(valueArg)!;
                var scope = ParseScope(parseResult.GetValue(scopeOpt));

                JsonElement element;
                try
                {
                    element = JsonDocument.Parse(raw).RootElement.Clone();
                }
                catch (JsonException ex)
                {
                    // The value must be valid JSON — catch at the boundary so the user
                    // gets a clear message instead of an unhandled exception.
                    await Console.Error.WriteLineAsync($"Invalid JSON value: {ex.Message}");
                    return 1;
                }

                await host.Configuration.SetSettingAsync(key, element, scope, CancellationToken.None);
                Console.WriteLine($"Set {key} = {raw} ({scope.ToString().ToLowerInvariant()}).");
                return 0;
            }, cancellationToken));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // ur config clear <key> [--scope user|workspace]
    // -------------------------------------------------------------------------

    private static Command BuildClear()
    {
        var keyArg   = new Argument<string>("key") { Description = "Dot-separated settings key" };
        var scopeOpt = ScopeOption();

        var cmd = new Command("clear", "Remove a settings key from the given scope") { keyArg, scopeOpt };

        cmd.SetAction(async (parseResult, cancellationToken) =>
            await HostRunner.RunAsync(async (host, _) =>
            {
                var key   = parseResult.GetValue(keyArg)!;
                var scope = ParseScope(parseResult.GetValue(scopeOpt));
                await host.Configuration.ClearSettingAsync(key, scope, CancellationToken.None);
                Console.WriteLine($"Cleared {key} ({scope.ToString().ToLowerInvariant()}).");
                return 0;
            }, cancellationToken));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // Shared helpers
    // -------------------------------------------------------------------------

    /// <summary>
    /// Builds the <c>--scope</c> option shared by write commands.
    /// Defaults to "user" so the most common case requires no explicit flag.
    /// </summary>
    private static Option<string> ScopeOption() =>
        new("--scope", "-s")
        {
            Description            = "Configuration scope: user (default) or workspace",
            DefaultValueFactory    = _ => "user"
        };

    private static ConfigurationScope ParseScope(string? value) =>
        value?.ToLowerInvariant() switch
        {
            "workspace" => ConfigurationScope.Workspace,
            _           => ConfigurationScope.User
        };
}
