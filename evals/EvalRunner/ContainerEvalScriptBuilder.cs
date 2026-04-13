using System.Text;
using EvalShared;

namespace EvalRunner;

/// <summary>
/// Builds the shell script that the eval container executes.
///
/// The script is kept in one place because the eval harness has to coordinate
/// three concerns that must share the same container environment:
/// Ox itself, any repo-specific bootstrap commands, and command-based
/// validation rules. Keeping the generated script centralized makes it easy to
/// unit test the orchestration contract instead of scattering string assembly
/// across <see cref="ContainerRunner"/>.
/// </summary>
internal static class ContainerEvalScriptBuilder
{
    internal const string ArtifactsDirectoryName = ".ox-eval";
    internal const string ScriptFileName = "run-eval.sh";
    internal const string ValidationReportFileName = "command-validation.tsv";

    /// <summary>
    /// Returns the script body that the container runs.
    ///
    /// Command-based validation now runs in the same container after Ox exits so
    /// validation sees the exact language toolchains and transient installs that
    /// the agent used while fixing the repo. That closes the gap that previously
    /// let host-local environments skew eval results.
    /// </summary>
    internal static string Build(ScenarioDefinition scenario)
    {
        var script = new StringBuilder();
        script.AppendLine("#!/usr/bin/env bash");
        script.AppendLine("set -uo pipefail");
        script.AppendLine();
        script.AppendLine("# Keep command-validation artifacts in a harness-owned directory so");
        script.AppendLine("# Ox session state and eval bookkeeping stay separate on the shared");
        script.AppendLine("# workspace volume.");
        script.AppendLine($"artifacts_dir=\"/workspace/{ArtifactsDirectoryName}\"");
        script.AppendLine("mkdir -p \"$artifacts_dir\"");
        script.AppendLine($"report_path=\"$artifacts_dir/{ValidationReportFileName}\"");
        script.AppendLine("rm -f \"$report_path\"");

        if (scenario.SetupCommands is { Count: > 0 })
        {
            script.AppendLine();
            script.AppendLine("# Repo bootstrap happens in the same container session as Ox so");
            script.AppendLine("# editable installs and language-specific dependency setup are");
            script.AppendLine("# visible to both the agent and the final command validation step.");
            for (var i = 0; i < scenario.SetupCommands.Count; i++)
            {
                script.AppendLine("# setup[" + i + "]");
                script.AppendLine(scenario.SetupCommands[i]);
            }
        }

        script.AppendLine();
        script.AppendLine("Ox \"$@\"");
        script.AppendLine("ox_status=$?");
        script.AppendLine("if [ \"$ox_status\" -ne 0 ]; then");
        script.AppendLine("  exit \"$ox_status\"");
        script.AppendLine("fi");

        var commandRules = scenario.ValidationRules
            .Where(RunsInsideContainer)
            .ToList();

        if (commandRules.Count == 0)
        {
            script.AppendLine("exit 0");
            return script.ToString();
        }

        script.AppendLine();
        script.AppendLine("# Record command-rule failures in a simple TSV so the host can recover");
        script.AppendLine("# the exact rule type and message without depending on container stderr.");
        script.AppendLine("validation_status=0");
        script.AppendLine("record_failure() {");
        script.AppendLine("  local rule_type=\"$1\"");
        script.AppendLine("  local message=\"$2\"");
        script.AppendLine("  printf '%s\\t%s\\n' \"$rule_type\" \"$(printf '%s' \"$message\" | base64 -w0)\" >> \"$report_path\"");
        script.AppendLine("  validation_status=1");
        script.AppendLine("}");

        for (var i = 0; i < commandRules.Count; i++)
        {
            script.AppendLine();
            switch (commandRules[i])
            {
                case CommandSucceedsRule rule:
                    AppendCommandSucceedsRule(script, rule, i);
                    break;
                case CommandOutputContainsRule rule:
                    AppendCommandOutputContainsRule(script, rule, i);
                    break;
            }
        }

        script.AppendLine();
        script.AppendLine("exit \"$validation_status\"");
        return script.ToString();
    }

    /// <summary>
    /// Command-based rules need the container's language toolchains, while file
    /// rules can be evaluated directly against the mounted workspace on the host.
    /// </summary>
    internal static bool RunsInsideContainer(ValidationRule rule) =>
        rule is CommandSucceedsRule or CommandOutputContainsRule;

    private static void AppendCommandSucceedsRule(StringBuilder script, CommandSucceedsRule rule, int index)
    {
        AppendLiteral(script, $"command_{index}", "OX_EVAL_COMMAND", index, rule.Command);
        script.AppendLine("output_" + index + "=\"\"");
        script.AppendLine("if output_" + index + "=$(bash -lc \"$command_" + index + "\" 2>&1); then");
        script.AppendLine("  :");
        script.AppendLine("else");
        script.AppendLine("  exit_code=$?");
        script.AppendLine("  message=$'Command failed (exit '\"${exit_code}\"$'): '\"$command_" + index + "\"$'\\n'\"$output_" + index + "\"");
        script.AppendLine("  record_failure \"" + rule.Type + "\" \"$message\"");
        script.AppendLine("fi");
    }

    private static void AppendCommandOutputContainsRule(StringBuilder script, CommandOutputContainsRule rule, int index)
    {
        AppendLiteral(script, $"command_{index}", "OX_EVAL_COMMAND", index, rule.Command);
        AppendLiteral(script, $"expected_{index}", "OX_EVAL_EXPECTED", index, rule.Output);
        script.AppendLine("output_" + index + "=\"\"");
        script.AppendLine("if output_" + index + "=$(bash -lc \"$command_" + index + "\" 2>&1); then");
        script.AppendLine("  if [[ \"$output_" + index + "\" != *\"$expected_" + index + "\"* ]]; then");
        script.AppendLine("    record_failure \"" + rule.Type + "\" \"Command output does not contain: $expected_" + index + "\"");
        script.AppendLine("  fi");
        script.AppendLine("else");
        script.AppendLine("  exit_code=$?");
        script.AppendLine("  message=$'Command failed (exit '\"${exit_code}\"$'): '\"$command_" + index + "\"$'\\n'\"$output_" + index + "\"");
        script.AppendLine("  record_failure \"" + rule.Type + "\" \"$message\"");
        script.AppendLine("fi");
    }

    private static void AppendLiteral(StringBuilder script, string variableName, string labelPrefix, int index, string value)
    {
        var label = $"{labelPrefix}_{index}";
        script.AppendLine(variableName + "=$(cat <<'" + label + "'");
        script.AppendLine(value);
        script.AppendLine(label);
        script.AppendLine(")");
    }
}
