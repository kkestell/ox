using dotenv.net;
using Ur;

DotEnv.Load(options: new DotEnvOptions(
    probeForEnv: true,
    probeLevelsToSearch: 8));

var host = await UrHost.StartAsync(Environment.CurrentDirectory);

Console.WriteLine($"ur — {host.WorkspacePath}");

Console.WriteLine($"Model catalog: {host.Configuration.AvailableModels.Count} models");
Console.WriteLine($"Chat ready: {host.Configuration.Readiness.CanRunTurns}");
