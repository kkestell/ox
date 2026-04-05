using System.Text.Json;
using Microsoft.Extensions.AI;
using Ur.Tools;

namespace Ur.Skills;

/// <summary>
/// Built-in tool that the model calls to invoke a skill. This is the bridge
/// between the tool system and the skills subsystem — the model sees skills
/// listed in the system prompt and invokes them by name through this tool.
///
/// The tool expands the skill's content template with the provided arguments
/// and returns the expanded prompt text as the tool result. This is a read-only
/// operation (no side effects beyond prompt expansion), so it uses
/// ReadInWorkspace permission and is auto-allowed without a user prompt.
///
/// TODO: Allowed-tools and model override — the skill definition stores these
/// fields but they aren't enforced yet. Enforcement requires AgentLoop to
/// accept per-turn tool/model overrides.
///
/// TODO: Forked execution (context: "fork") — always runs inline for now.
/// Requires a sub-agent mechanism to run a skill in a separate context.
/// </summary>
internal sealed class SkillTool : AIFunction
{
    private static readonly JsonElement Schema = JsonDocument.Parse("""
        {
            "type": "object",
            "properties": {
                "skill": {
                    "type": "string",
                    "description": "The name of the skill to invoke."
                },
                "args": {
                    "type": "string",
                    "description": "Optional arguments to pass to the skill."
                }
            },
            "required": ["skill"],
            "additionalProperties": false
        }
        """).RootElement.Clone();

    private readonly SkillRegistry _skills;
    private readonly string _sessionId;

    public SkillTool(SkillRegistry skills, string sessionId)
    {
        _skills = skills;
        _sessionId = sessionId;
    }

    public override string Name => "skill";

    public override string Description =>
        """
        Execute a skill within the main conversation.

        When users ask you to perform tasks, check if any of the available skills match.
        Skills provide specialized capabilities and domain knowledge.

        When users reference a "slash command" or "/<something>", they are referring to
        a skill. Use this tool to invoke it.

        How to invoke:
        - Use this tool with the skill name and optional arguments
        - Examples:
          - skill: "commit", args: "-m 'Fix bug'"
          - skill: "review-pr", args: "123"

        Important:
        - Available skills are listed in the system message
        - When a skill matches the user's request, invoke it immediately
        - Do not invoke a skill that is already running
        """;

    public override JsonElement JsonSchema => Schema;

    protected override ValueTask<object?> InvokeCoreAsync(
        AIFunctionArguments arguments,
        CancellationToken cancellationToken)
    {
        var skillName = ToolArgHelpers.GetRequiredString(arguments, "skill");
        var args = ToolArgHelpers.GetOptionalString(arguments, "args") ?? "";

        // Strip leading "/" in case the model includes it (e.g. "/commit" instead of "commit").
        if (skillName.StartsWith('/'))
            skillName = skillName[1..];

        var skill = _skills.Get(skillName);

        if (skill is null)
            return new ValueTask<object?>($"Unknown skill: {skillName}");

        if (skill.DisableModelInvocation)
            return new ValueTask<object?>($"Skill '{skillName}' cannot be invoked by the model.");

        var expanded = SkillExpander.Expand(skill, args, _sessionId);
        return new ValueTask<object?>(expanded);
    }
}
