using dotenv.net;
using Ur;

DotEnv.Load(options: new DotEnvOptions(
    probeForEnv: true,
    probeLevelsToSearch: 8));

var host = UrHost.Start(Environment.CurrentDirectory);

Console.WriteLine($"ur — {host.Workspace.RootPath}");

await host.ModelCatalog.EnsureLoadedAsync();
Console.WriteLine($"Model catalog: {host.ModelCatalog.Models.Count} models");
