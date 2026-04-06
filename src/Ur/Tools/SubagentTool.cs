using System.Text.Json;
using Microsoft.Extensions.AI;
using Ur.AgentLoop;
using Ur.Permissions;

namespace Ur.Tools;

/// <summary>
/// Built-in tool that delegates a sub-task to a fresh, isolated agent loop
/// and returns the result as a string.
///
/// The LLM calls this tool with a plain-text task description. SubagentRunner
/// spawns a new AgentLoop with an empty message history, runs it to completion,
/// and returns the final text response. The parent turn never sees the
/// sub-agent's intermediate tool calls — only the final prose result.
///
/// Architecture: This class depends on <see cref="ISubagentRunner"/> (not on
/// the concrete SubagentRunner) to keep the Tools layer free of direct AgentLoop
/// references. The interface breaks the Ur.Tools → Ur.AgentLoop → Ur.Tools cycle.
/// UrHost (above both layers) wires the concrete runner to this tool.
///
/// Permission classification: Execute (prompts once per call, never remembered).
/// Spawning a sub-agent is an autonomous action that carries the same blast-radius
/// risk as running a shell command — not something to auto-allow.
/// </summary>
internal sealed class SubagentTool(ISubagentRunner runner) : AIFunction, IToolMeta
{
    // The name constant is referenced from SubagentRunner to avoid a hard-coded
    // string literal in two places. Use the constant here as the Name property too,
    // so both stay in sync automatically.
    internal const string ToolName = "run_subagent";

    // Read: the subagent does not bypass the permission system — every tool call
    // it makes is individually gated. Classifying the spawn itself as Execute
    // adds no security value and would prompt on every use. Treating it as Read
    // (in-workspace, auto-allowed) matches the actual blast-radius of the call.
    OperationType IToolMeta.OperationType => OperationType.Read;

    // The task string is the target for the permission prompt, but it can be
    // arbitrarily long — truncate so it fits on a single terminal line.
    ITargetExtractor IToolMeta.TargetExtractor => TargetExtractors.FromKeyTruncated("task");

    private static readonly JsonElement Schema = JsonDocument.Parse("""
        {
            "type": "object",
            "properties": {
                "task": {
                    "type": "string",
                    "description": "A clear, self-contained description of the task for the sub-agent to complete. Include all necessary context — the sub-agent has no access to the parent conversation."
                }
            },
            "required": ["task"],
            "additionalProperties": false
        }
        """).RootElement.Clone();

    public override string Name => ToolName;

    public override string Description =>
        """
        Delegate a well-scoped sub-task to a fresh, independent agent that runs its
        own LLM-tool loop and returns its final response as a string.

        Use this tool to decompose complex work — for example, "research how X works,
        then summarize the findings" — without polluting the current conversation with
        the sub-agent's intermediate tool calls. The sub-agent receives only the task
        description; it has no access to the current conversation history.

        The sub-agent has the same tools available as the current agent (except
        run_subagent itself, to prevent recursion). It shares the current workspace
        and inherits any permissions already granted in this session.

        Provide a complete, self-contained task description. Vague tasks produce vague results.
        """;

    public override JsonElement JsonSchema => Schema;

    protected override async ValueTask<object?> InvokeCoreAsync(
        AIFunctionArguments arguments,
        CancellationToken cancellationToken)
    {
        var task = ToolArgHelpers.GetRequiredString(arguments, "task");
        // RunAsync handles all error cases internally and always returns a string.
        // Exceptions propagate up through ToolInvoker's error-result wrapper.
        return await runner.RunAsync(task, cancellationToken);
    }
}
