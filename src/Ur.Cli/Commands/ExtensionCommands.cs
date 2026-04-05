using System.CommandLine;

namespace Ur.Cli.Commands;

/// <summary>
/// `ur extensions *` — manage the extension catalog.
///
/// Subcommands:
///   list                         tabular list of all discovered extensions and their state
///   enable &lt;extension-id&gt;       enable an extension for its tier (user or workspace)
///   disable &lt;extension-id&gt;      disable an extension for its tier
///   reset &lt;extension-id&gt;        remove any override and restore the tier default
///
/// Extension IDs take the form "&lt;tier&gt;:&lt;name&gt;" (e.g. "system:git",
/// "user:my-tools").  Run `ur extensions list` to see all IDs.
/// </summary>
internal static class ExtensionCommands
{
    public static Command Build()
    {
        var extensions = new Command("extensions", "Manage the extension catalog");

        extensions.Add(BuildList());
        extensions.Add(BuildEnable());
        extensions.Add(BuildDisable());
        extensions.Add(BuildReset());

        return extensions;
    }

    // -------------------------------------------------------------------------
    // ur extensions list
    // -------------------------------------------------------------------------

    private static Command BuildList()
    {
        var cmd = new Command("list", "List all discovered extensions and their current state");

        cmd.SetAction(async (_, cancellationToken) =>
            await HostRunner.RunAsync(async (host, _) =>
            {
                var extensions = host.Extensions.List();

                if (extensions.Count == 0)
                {
                    Console.WriteLine("No extensions found.");
                    return 0;
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
                return 0;
            }, cancellationToken));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // ur extensions enable <extension-id>
    // -------------------------------------------------------------------------

    private static Command BuildEnable()
    {
        var idArg = ExtensionIdArgument();
        var cmd   = new Command("enable", "Enable an extension");
        cmd.Add(idArg);

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
        var cmd   = new Command("disable", "Disable an extension");
        cmd.Add(idArg);

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
        var cmd   = new Command("reset", "Remove any override and restore the tier default for an extension");
        cmd.Add(idArg);

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
    // Shared helpers
    // -------------------------------------------------------------------------

    private static Argument<string> ExtensionIdArgument() =>
        new Argument<string>("extension-id")
        {
            Description = "Extension ID in the form <tier>:<name> (e.g. system:git)"
        };

    private static string Truncate(string s, int maxLength) =>
        s.Length <= maxLength ? s : s[..(maxLength - 1)] + "…";
}
