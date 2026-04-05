using System.CommandLine;
using Ur.Cli.Commands;

// CLI entry point. Uses System.CommandLine to parse arguments and dispatch to
// the appropriate subcommand. Each command is built by its own class in Commands/
// and follows the same pattern: define arguments/options, then use HostRunner
// to boot the Ur host before executing the command logic.

var root = new RootCommand("ur — AI agent framework");

root.Add(StatusCommand.Build());      // ur status
root.Add(ConfigCommands.Build());     // ur config set-api-key, set-model, get, set, clear
root.Add(ModelCommands.Build());      // ur models list, refresh, show
root.Add(SessionCommands.Build());    // ur sessions list, show
root.Add(ExtensionCommands.Build());  // ur extensions list, enable, disable, reset
root.Add(ChatCommand.Build());        // ur chat <message>

return await root.Parse(args).InvokeAsync();
