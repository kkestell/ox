using System.Runtime.InteropServices;
using dotenv.net;
using Microsoft.Extensions.AI;
using Ur;
using Ur.Configuration.Keyring;

DotEnv.Load(options: new DotEnvOptions(
    probeForEnv: true,
    probeLevelsToSearch: 8));

IKeyring keyring = RuntimeInformation.IsOSPlatform(OSPlatform.OSX)
    ? new MacOSKeyring()
    : RuntimeInformation.IsOSPlatform(OSPlatform.Linux)
        ? new LinuxKeyring()
        : throw new PlatformNotSupportedException("Ur requires macOS or Linux.");

var host = UrHost.Start(Environment.CurrentDirectory, keyring);

Console.WriteLine($"ur — {host.Workspace.RootPath}");

// Ensure model catalog is loaded (fetch from API if no cache).
await host.ModelCatalog.EnsureLoadedAsync();
Console.WriteLine($"Model catalog: {host.ModelCatalog.Models.Count} models");

try
{
    var client = host.CreateChatClient("anthropic/claude-sonnet-4");
    Console.WriteLine("Sending test message...");

    await foreach (var update in client.GetStreamingResponseAsync("Say hello in one sentence."))
    {
        Console.Write(update.Text);
    }
    Console.WriteLine();
}
catch (InvalidOperationException ex)
{
    Console.WriteLine(ex.Message);
}
