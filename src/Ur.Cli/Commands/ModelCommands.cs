using System.CommandLine;
using Ur.Providers;

namespace Ur.Cli.Commands;

/// <summary>
/// `ur models *` — browse the OpenRouter model catalog.
///
/// These commands only apply to the OpenRouter provider, which is the only provider
/// with a remote browsable model catalog. For other providers (openai, google, ollama),
/// you just need to know the model name — use `ur config set-model provider/model`.
///
/// Subcommands:
///   list [--all]         tabular list of OpenRouter models; filters to text+tool-capable by default
///   refresh              fetch the latest catalog from the OpenRouter API
///   show &lt;model-id&gt;     print complete metadata for one OpenRouter model
///
/// The catalog is cached on disk and loaded at host startup, so `list` is instant.
/// `refresh` is the only command that makes a network request.
/// </summary>
internal static class ModelCommands
{
    public static Command Build()
    {
        var models = new Command("models", "Browse the model catalog")
        {
            BuildList(),
            BuildRefresh(),
            BuildShow()
        };

        return models;
    }

    // -------------------------------------------------------------------------
    // ur models list [--all]
    // -------------------------------------------------------------------------

    private static Command BuildList()
    {
        var allOpt = new Option<bool>("--all")
        {
            Description = "Show every model in the catalog, not just text+tool capable ones"
        };

        var cmd = new Command("list", "List available models") { allOpt };

        cmd.SetAction(async (parseResult, cancellationToken) =>
            await HostRunner.RunAsync((host, _) =>
            {
                var showAll = parseResult.GetValue(allOpt);
                var models  = showAll
                    ? host.Configuration.AllModels
                    : host.Configuration.AvailableModels;

                if (models.Count == 0)
                {
                    Console.WriteLine("No models in catalog. Run: ur models refresh");
                    return Task.FromResult(0);
                }

                // Column widths — measured from typical OpenRouter IDs and names.
                const int idWidth   = 44;
                const int nameWidth = 36;

                Console.WriteLine(
                    $"{"ID",-idWidth}  {"Name",-nameWidth}  {"Context",8}  {"In $/M",8}  {"Out $/M",8}");
                Console.WriteLine(new string('-', idWidth + nameWidth + 38));

                foreach (var m in models)
                    PrintRow(m, idWidth, nameWidth);

                Console.WriteLine();
                var view = showAll ? "all models" : "text+tool-capable models";
                Console.WriteLine($"{models.Count} model(s) listed ({view}).");
                return Task.FromResult(0);
            }, cancellationToken));

        return cmd;
    }

    private static void PrintRow(ModelInfo m, int idWidth, int nameWidth)
    {
        // Truncate long IDs and names so the table stays readable in a normal terminal.
        // PadRight is used instead of interpolated alignment because alignment widths
        // must be constants in C# string interpolation.
        var id   = Truncate(m.Id, idWidth).PadRight(idWidth);
        var name = Truncate(m.Name, nameWidth).PadRight(nameWidth);
        Console.WriteLine(
            $"{id}  {name}  {m.ContextLength,8:N0}  {m.InputCostPerMToken,8:F2}  {m.OutputCostPerMToken,8:F2}");
    }

    // -------------------------------------------------------------------------
    // ur models refresh
    // -------------------------------------------------------------------------

    private static Command BuildRefresh()
    {
        var cmd = new Command("refresh", "Fetch the latest model catalog from OpenRouter");

        cmd.SetAction(async (_, cancellationToken) =>
            await HostRunner.RunAsync(async (host, _) =>
            {
                Console.Write("Refreshing model catalog... ");
                await host.Configuration.RefreshModelsAsync(CancellationToken.None);
                Console.WriteLine($"done. {host.Configuration.AvailableModels.Count} models available.");
                return 0;
            }, cancellationToken));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // ur models show <model-id>
    // -------------------------------------------------------------------------

    private static Command BuildShow()
    {
        var modelArg = new Argument<string>("model-id")
        {
            Description = "Model identifier (e.g. openai/gpt-4o)"
        };

        var cmd = new Command("show", "Print full metadata for a specific model") { modelArg };

        cmd.SetAction(async (parseResult, cancellationToken) =>
            await HostRunner.RunAsync(async (host, _) =>
            {
                var modelId = parseResult.GetValue(modelArg)!;
                var model   = host.Configuration.GetModel(modelId);

                if (model is null)
                {
                    await Console.Error.WriteLineAsync($"Model not found: {modelId}");
                    await Console.Error.WriteLineAsync("Run 'ur models list' to see available models, or 'ur models refresh' to update the catalog.");
                    return 1;
                }

                Console.WriteLine($"ID:           {model.Id}");
                Console.WriteLine($"Name:         {model.Name}");
                Console.WriteLine($"Context:      {model.ContextLength:N0} tokens");
                Console.WriteLine($"Max output:   {model.MaxOutputTokens:N0} tokens");
                Console.WriteLine($"Input cost:   ${model.InputCostPerMToken:F4}/M tokens");
                Console.WriteLine($"Output cost:  ${model.OutputCostPerMToken:F4}/M tokens");
                Console.WriteLine($"Parameters:   {string.Join(", ", model.SupportedParameters)}");
                Console.WriteLine($"Modality:     {model.Modality ?? "(not specified)"}");
                return 0;
            }, cancellationToken));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // Shared helpers
    // -------------------------------------------------------------------------

    private static string Truncate(string s, int maxLength) =>
        s.Length <= maxLength ? s : s[..(maxLength - 1)] + "…";
}
