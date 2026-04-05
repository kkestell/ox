using System.CommandLine;
using System.Text.Json;
using Ur.Configuration;

namespace Ur.Cli.Commands;

/// <summary>
/// `ur config *` — manage workspace and user-level configuration.
///
/// Subcommands:
///   set-api-key &lt;key&gt;                     write the OpenRouter API key to the system keyring
///   clear-api-key                          delete the stored API key
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
        var config = new Command("config", "Read and write configuration settings");

        config.Add(BuildSetApiKey());
        config.Add(BuildClearApiKey());
        config.Add(BuildSetModel());
        config.Add(BuildClearModel());
        config.Add(BuildGet());
        config.Add(BuildSet());
        config.Add(BuildClear());

        return config;
    }

    // -------------------------------------------------------------------------
    // ur config set-api-key <key>
    // -------------------------------------------------------------------------

    private static Command BuildSetApiKey()
    {
        var keyArg = new Argument<string>("key")
        {
            Description = "OpenRouter API key"
        };

        var cmd = new Command("set-api-key", "Store the OpenRouter API key in the system keyring");
        cmd.Add(keyArg);

        cmd.SetAction(async (parseResult, cancellationToken) =>
            await HostRunner.RunAsync(async (host, _) =>
            {
                var key = parseResult.GetValue(keyArg)!;
                await host.Configuration.SetApiKeyAsync(key, CancellationToken.None);
                Console.WriteLine("API key saved.");
                return 0;
            }, cancellationToken));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // ur config clear-api-key
    // -------------------------------------------------------------------------

    private static Command BuildClearApiKey()
    {
        var cmd = new Command("clear-api-key", "Remove the stored OpenRouter API key");

        cmd.SetAction(async (_, cancellationToken) =>
            await HostRunner.RunAsync(async (host, _) =>
            {
                await host.Configuration.ClearApiKeyAsync(CancellationToken.None);
                Console.WriteLine("API key cleared.");
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

        var cmd = new Command("set-model", "Set the default model");
        cmd.Add(modelArg);
        cmd.Add(scopeOpt);

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

        var cmd = new Command("clear-model", "Clear the selected model for a scope");
        cmd.Add(scopeOpt);

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

        var cmd = new Command("get", "Get the merged value for a settings key");
        cmd.Add(keyArg);

        cmd.SetAction(async (parseResult, cancellationToken) =>
            await HostRunner.RunAsync(async (host, _) =>
            {
                var key   = parseResult.GetValue(keyArg)!;
                var value = host.Configuration.GetSetting(key);

                if (value is null)
                    Console.WriteLine("(not set)");
                else
                    Console.WriteLine(value.Value.ToString());

                return 0;
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

        var cmd = new Command("set", "Write a raw JSON value for a settings key");
        cmd.Add(keyArg);
        cmd.Add(valueArg);
        cmd.Add(scopeOpt);

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
                    Console.Error.WriteLine($"Invalid JSON value: {ex.Message}");
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

        var cmd = new Command("clear", "Remove a settings key from the given scope");
        cmd.Add(keyArg);
        cmd.Add(scopeOpt);

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
        new Option<string>("--scope", "-s")
        {
            Description            = "Configuration scope: user (default) or workspace",
            DefaultValueFactory    = _ => "user",
        };

    private static ConfigurationScope ParseScope(string? value) =>
        value?.ToLowerInvariant() switch
        {
            "workspace" => ConfigurationScope.Workspace,
            _           => ConfigurationScope.User,
        };
}
