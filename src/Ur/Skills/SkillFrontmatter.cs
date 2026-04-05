using YamlDotNet.Serialization;
using YamlDotNet.Serialization.NamingConventions;

namespace Ur.Skills;

/// <summary>
/// Parses SKILL.md files into <see cref="SkillDefinition"/> instances.
///
/// A SKILL.md file has an optional YAML frontmatter block delimited by "---" lines,
/// followed by a markdown body that serves as the prompt template. The frontmatter
/// provides metadata (name, description, arguments, etc.) while the body is the
/// actual content that gets expanded and sent to the model.
///
/// If frontmatter is missing entirely, the skill gets default metadata derived
/// from its directory name — this allows minimal skills that are just a markdown file.
/// </summary>
internal static class SkillFrontmatter
{
    private static readonly IDeserializer YamlDeserializer = new DeserializerBuilder()
        .WithNamingConvention(HyphenatedNamingConvention.Instance)
        .IgnoreUnmatchedProperties()
        .Build();

    /// <summary>
    /// Parses a SKILL.md file's content into a <see cref="SkillDefinition"/>.
    /// </summary>
    /// <param name="fileContent">The raw text of the SKILL.md file.</param>
    /// <param name="skillDirectory">Absolute path to the directory containing the SKILL.md file.</param>
    /// <param name="source">"user" or "workspace" — where the skill was discovered.</param>
    /// <returns>A fully populated SkillDefinition.</returns>
    public static SkillDefinition Parse(string fileContent, string skillDirectory, string source)
    {
        var (frontmatter, body) = SplitFrontmatter(fileContent);

        // Derive a fallback name from the directory (e.g. "/home/user/.ur/skills/commit" → "commit").
        var directoryName = Path.GetFileName(skillDirectory) ?? "unknown";

        if (frontmatter is null)
        {
            return new SkillDefinition
            {
                Name = directoryName,
                Description = "",
                Content = body,
                SkillDirectory = skillDirectory,
                Source = source,
            };
        }

        var raw = YamlDeserializer.Deserialize<RawFrontmatter>(frontmatter) ?? new RawFrontmatter();

        return new SkillDefinition
        {
            Name = raw.Name ?? directoryName,
            Description = raw.Description ?? "",
            WhenToUse = raw.WhenToUse,
            UserInvocable = raw.UserInvocable ?? true,
            DisableModelInvocation = raw.DisableModelInvocation ?? false,
            ArgumentHint = raw.ArgumentHint,
            ArgumentNames = ParseCommaSeparated(raw.Arguments),
            Context = raw.Context,
            Agent = raw.Agent,
            Paths = ParseCommaSeparated(raw.Paths),
            AllowedTools = ParseCommaSeparated(raw.AllowedTools),
            Model = raw.Model,
            Version = raw.Version,
            Content = body,
            SkillDirectory = skillDirectory,
            Source = source,
        };
    }

    /// <summary>
    /// Splits a SKILL.md file into its frontmatter YAML string and body content.
    /// Frontmatter is delimited by lines that are exactly "---". If no frontmatter
    /// block is found, returns (null, entireContent).
    /// </summary>
    private static (string? Frontmatter, string Body) SplitFrontmatter(string content)
    {
        // Frontmatter must start at the very beginning of the file.
        if (!content.StartsWith("---", StringComparison.Ordinal))
            return (null, content);

        // Find the closing "---" delimiter. We skip the first line (the opening "---")
        // and search for a line that is exactly "---".
        var firstNewline = content.IndexOf('\n');
        if (firstNewline < 0)
            return (null, content);

        var closingIndex = -1;
        var searchStart = firstNewline + 1;

        while (searchStart < content.Length)
        {
            var lineEnd = content.IndexOf('\n', searchStart);
            var line = lineEnd < 0
                ? content[searchStart..]
                : content[searchStart..lineEnd];

            // Trim trailing \r for Windows line endings.
            line = line.TrimEnd('\r');

            if (line == "---")
            {
                closingIndex = searchStart;
                break;
            }

            // Move past this line.
            if (lineEnd < 0)
                break;
            searchStart = lineEnd + 1;
        }

        if (closingIndex < 0)
            return (null, content);

        var yaml = content[(firstNewline + 1)..closingIndex];

        // Body starts after the closing "---" line.
        var bodyStart = content.IndexOf('\n', closingIndex);
        var body = bodyStart < 0 ? "" : content[(bodyStart + 1)..];

        return (yaml, body);
    }

    /// <summary>
    /// Splits a comma-separated string into a trimmed array. Returns null for null/empty input.
    /// Used for "arguments", "paths", and "allowed-tools" frontmatter fields.
    /// </summary>
    private static string[]? ParseCommaSeparated(string? value)
    {
        if (string.IsNullOrWhiteSpace(value))
            return null;

        return value.Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries);
    }

    /// <summary>
    /// Intermediate deserialization target for YAML frontmatter. YamlDotNet maps
    /// hyphenated keys (e.g. "when-to-use") to these properties via the
    /// HyphenatedNamingConvention. All fields are nullable because any frontmatter
    /// key can be omitted.
    /// </summary>
    private sealed class RawFrontmatter
    {
        public string? Name { get; set; }
        public string? Description { get; set; }
        public string? WhenToUse { get; set; }
        public bool? UserInvocable { get; set; }
        public bool? DisableModelInvocation { get; set; }
        public string? ArgumentHint { get; set; }
        public string? Arguments { get; set; }
        public string? Context { get; set; }
        public string? Agent { get; set; }
        public string? Paths { get; set; }
        public string? AllowedTools { get; set; }
        public string? Model { get; set; }
        public string? Version { get; set; }
    }
}
