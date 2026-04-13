using System.Globalization;
using System.Text;

namespace Ox.Agent.Skills;

/// <summary>
///     Formats the skills-specific section of the system prompt.
///     The skills subsystem owns how model-invocable skills are described to the
///     model, but it does not own the rest of the agent's operating policy.
///     Returning just this section keeps the boundary narrow and lets the session
///     compose the full prompt from independent concerns.
/// </summary>
internal static class SkillPromptSectionBuilder
{
    private const int MaxWhenToUseLength = 250;

    /// <summary>
    ///     Builds the system-prompt section that advertises model-invocable skills.
    ///     Returns an empty string when no skills are visible to the model so the
    ///     caller can omit the section entirely without special casing nulls.
    /// </summary>
    public static string Build(SkillRegistry skills)
    {
        var modelSkills = skills.ModelInvocable();
        if (modelSkills.Count == 0)
            return "";

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
