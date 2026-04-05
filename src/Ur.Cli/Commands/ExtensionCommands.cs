using System.CommandLine;
using System.Text.Json;

namespace Ur.Cli.Commands;

/// <summary>
/// `ur extensions *` — manage the extension catalog.
///
/// Subcommands:
///   list                         tabular list of all discovered extensions and their state
///   enable &lt;extension-id&gt;       enable an extension for its tier (user or workspace)
///   disable &lt;extension-id&gt;      disable an extension for its tier
///   reset &lt;extension-id&gt;        remove any override and restore the tier default
///   settings &lt;extension-id&gt;     list all settings declared by an extension with their schemas and current values
///
/// Extension IDs take the form "&lt;tier&gt;:&lt;name&gt;" (e.g. "system:git",
/// "user:my-tools").  Run `ur extensions list` to see all IDs.
/// </summary>
internal static class ExtensionCommands
{
    public static Command Build()
    {
        var extensions = new Command("extensions", "Manage the extension catalog")
        {
            BuildList(),
            BuildEnable(),
            BuildDisable(),
            BuildReset(),
            BuildSettings()
        };

        return extensions;
    }

    // -------------------------------------------------------------------------
    // ur extensions list
    // -------------------------------------------------------------------------

    private static Command BuildList()
    {
        var cmd = new Command("list", "List all discovered extensions and their current state");

        cmd.SetAction(async (_, cancellationToken) =>
            await HostRunner.RunAsync((host, _) =>
            {
                var extensions = host.Extensions.List();

                if (extensions.Count == 0)
                {
                    Console.WriteLine("No extensions found.");
                    return Task.FromResult(0);
                }

                const int idWidth      = 30;
                const int nameWidth    = 20;
                const int tierWidth    = 10;
                const int versionWidth = 10;

                Console.WriteLine(
                    $"{"ID",-idWidth}  {"Name",-nameWidth}  {"Tier",-tierWidth}  {"Ver",-versionWidth}  {"Enabled",7}  {"Active",6}");
                Console.WriteLine(new string('-', idWidth + nameWidth + tierWidth + versionWidth + 26));

                foreach (var ext in extensions)
                {
                    var id      = Truncate(ext.Id, idWidth);
                    var name    = Truncate(ext.Name, nameWidth);
                    var tier    = ext.Tier.ToString().ToLowerInvariant();
                    var enabled = ext.DesiredEnabled ? "yes" : "no";
                    var active  = ext.IsActive       ? "yes" : "no";
                    var error   = ext.LoadError is not null ? " [ERROR]" : "";

                    Console.WriteLine(
                        $"{id,-idWidth}  {name,-nameWidth}  {tier,-tierWidth}  {ext.Version,-versionWidth}  {enabled,7}  {active,6}{error}");
                }

                Console.WriteLine();
                Console.WriteLine($"{extensions.Count} extension(s).");
                return Task.FromResult(0);
            }, cancellationToken));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // ur extensions enable <extension-id>
    // -------------------------------------------------------------------------

    private static Command BuildEnable()
    {
        var idArg = ExtensionIdArgument();
        var cmd   = new Command("enable", "Enable an extension") { idArg };

        cmd.SetAction(async (parseResult, cancellationToken) =>
            await HostRunner.RunAsync(async (host, ct) =>
            {
                var id   = parseResult.GetValue(idArg)!;
                var info = await host.Extensions.SetEnabledAsync(id, enabled: true, ct);
                Console.WriteLine($"Enabled {info.Id} (active: {(info.IsActive ? "yes" : "no")}).");
                return 0;
            }, cancellationToken));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // ur extensions disable <extension-id>
    // -------------------------------------------------------------------------

    private static Command BuildDisable()
    {
        var idArg = ExtensionIdArgument();
        var cmd   = new Command("disable", "Disable an extension") { idArg };

        cmd.SetAction(async (parseResult, cancellationToken) =>
            await HostRunner.RunAsync(async (host, ct) =>
            {
                var id   = parseResult.GetValue(idArg)!;
                var info = await host.Extensions.SetEnabledAsync(id, enabled: false, ct);
                Console.WriteLine($"Disabled {info.Id}.");
                return 0;
            }, cancellationToken));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // ur extensions reset <extension-id>
    // -------------------------------------------------------------------------

    private static Command BuildReset()
    {
        var idArg = ExtensionIdArgument();
        var cmd   = new Command("reset", "Remove any override and restore the tier default for an extension") { idArg };

        cmd.SetAction(async (parseResult, cancellationToken) =>
            await HostRunner.RunAsync(async (host, ct) =>
            {
                var id   = parseResult.GetValue(idArg)!;
                var info = await host.Extensions.ResetAsync(id, ct);
                Console.WriteLine(
                    $"Reset {info.Id}: enabled={info.DesiredEnabled}, active={info.IsActive}.");
                return 0;
            }, cancellationToken));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // ur extensions settings <extension-id>
    // -------------------------------------------------------------------------

    private static Command BuildSettings()
    {
        var idArg = ExtensionIdArgument();
        var cmd   = new Command("settings", "List settings declared by an extension, with schemas and current values") { idArg };

        cmd.SetAction(async (parseResult, cancellationToken) =>
            await HostRunner.RunAsync((host, _) =>
            {
                var id      = parseResult.GetValue(idArg)!;
                var schemas = host.Extensions.GetExtensionSettings(id);

                if (schemas.Count == 0)
                {
                    Console.WriteLine($"No settings defined for {id}.");
                    return Task.FromResult(0);
                }

                // Print each setting as: key name, current value (if any), then the
                // schema as pretty-printed JSON.  The schema describes the type, default,
                // and any constraints — together with the live value this is a one-stop
                // diagnostic view for extension configuration.
                foreach (var (key, schema) in schemas)
                {
                    Console.WriteLine($"--- {key} ---");

                    var current = host.Configuration.GetSetting(key);
                    Console.WriteLine(current.HasValue
                        ? $"Current value: {current.Value}"
                        : "Current value: (not set)");

                    var prettySchema = JsonSerializer.Serialize(schema, IndentedJsonOptions);
                    Console.WriteLine("Schema:");
                    Console.WriteLine(prettySchema);
                    Console.WriteLine();
                }

                return Task.FromResult(0);
            }, cancellationToken));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // Shared helpers
    // -------------------------------------------------------------------------

    // Reused for all JSON-schema pretty-printing — avoids allocating a new
    // JsonSerializerOptions on every extension settings display.
    private static readonly JsonSerializerOptions IndentedJsonOptions = new() { WriteIndented = true };

    private static Argument<string> ExtensionIdArgument() =>
        new("extension-id")
        {
            Description = "Extension ID in the form <tier>:<name> (e.g. system:git)"
        };

    private static string Truncate(string s, int maxLength) =>
        s.Length <= maxLength ? s : s[..(maxLength - 1)] + "…";
}
