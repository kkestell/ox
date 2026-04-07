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
    /// Builds the system prompt listing all model-invocable skills.
    /// Returns null if no skills are available, so the caller can skip
    /// injecting a system message entirely.
    /// </summary>
    public static string? Build(SkillRegistry skills)
    {
        var modelSkills = skills.ModelInvocable();
        if (modelSkills.Count == 0)
            return null;

        var sb = new StringBuilder();
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

        return sb.ToString();
    }
}
