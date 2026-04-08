using System.Globalization;
using System.Text;

namespace Ur.Skills;

/// <summary>
/// Constructs the system prompt that tells the model which skills are available.
/// The system prompt is rebuilt each turn (it's transient, not persisted) so that
/// changes to skill availability are reflected immediately.
///
/// The format mirrors common agent system prompts: a brief preamble followed by
/// a bulleted list of skills with their names, descriptions, and usage hints.
/// </summary>
internal static class SystemPromptBuilder
{
    private const int MaxWhenToUseLength = 250;

    /// <summary>
    /// Static guidance for the <c>todo_write</c> tool. Included in every system
    /// prompt so the model knows when and how to use task tracking.
    /// </summary>
    private const string TodoGuidance = """
        # Task tracking

        Use the `todo_write` tool to track progress on multi-step tasks (3+ steps).
        Each call replaces the entire list — always send all items, not just changed ones.
        - Mark items "in_progress" before starting work, "completed" when done.
        - Keep at most one item "in_progress" at a time.
        - Don't use for trivial single-step tasks.
        - When all work is complete, send an empty list to clear the sidebar.
        """;

    /// <summary>
    /// Builds the system prompt combining todo guidance and model-invocable skills.
    /// Always returns a non-null string because the todo guidance section is
    /// always included.
    /// </summary>
    public static string Build(SkillRegistry skills)
    {
        var sb = new StringBuilder();

        // Todo guidance is always present — the tool is always registered.
        sb.AppendLine(TodoGuidance);
        sb.AppendLine();

        var modelSkills = skills.ModelInvocable();
        if (modelSkills.Count > 0)
        {
            sb.AppendLine("The following skills are available for use with the skill tool:");
            sb.AppendLine();

            foreach (var skill in modelSkills)
            {
                sb.Append(CultureInfo.InvariantCulture, $"- {skill.Name}");

                if (!string.IsNullOrWhiteSpace(skill.Description))
                    sb.Append(CultureInfo.InvariantCulture, $": {skill.Description}");

                if (!string.IsNullOrWhiteSpace(skill.WhenToUse))
                {
                    var hint = skill.WhenToUse.Length > MaxWhenToUseLength
                        ? skill.WhenToUse[..MaxWhenToUseLength] + "..."
                        : skill.WhenToUse;
                    sb.Append(CultureInfo.InvariantCulture, $" — {hint}");
                }

                sb.AppendLine();
            }
        }

        return sb.ToString();
    }
}
