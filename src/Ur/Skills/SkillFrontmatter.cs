namespace Ur.Skills;

/// <summary>
/// Parses SKILL.md files into <see cref="SkillDefinition"/> instances.
///
/// A SKILL.md file has an optional YAML frontmatter block delimited by "---" lines,
/// followed by a markdown body that serves as the prompt template. The frontmatter
/// provides metadata (name, description, arguments, etc.) while the body is the
/// actual content that gets expanded and sent to the model.
///
/// Frontmatter is flat key: value pairs with hyphenated keys (e.g. "when-to-use: ...").
/// We parse it by hand rather than pulling in a YAML library — the format is simple
/// enough that a line-oriented parser keeps the dependency graph lean and AOT-clean.
///
/// If frontmatter is missing entirely, the skill gets default metadata derived
/// from its directory name — this allows minimal skills that are just a markdown file.
/// </summary>
internal static class SkillFrontmatter
{
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
        var fileName = Path.GetFileName(skillDirectory);
        var directoryName = string.IsNullOrEmpty(fileName) ? "unknown" : fileName;

        if (frontmatter is null)
        {
            return new SkillDefinition
            {
                Name = directoryName,
                Description = "",
                Content = body,
                SkillDirectory = skillDirectory,
                Source = source
            };
        }

        return ParseFrontmatter(frontmatter, directoryName, body, skillDirectory, source);
    }

    /// <summary>
    /// Parses the YAML frontmatter block into a <see cref="SkillDefinition"/>.
    /// The format is strictly flat "key: value" lines with hyphenated keys.
    /// Unknown keys are silently ignored so new frontmatter fields don't break
    /// older versions of the parser.
    /// </summary>
    private static SkillDefinition ParseFrontmatter(
        string yaml, string directoryName, string body, string skillDirectory, string source)
    {
        string? name = null, description = null, whenToUse = null;
        bool? userInvocable = null, disableModelInvocation = null;
        string? argumentHint = null, arguments = null, context = null;
        string? agent = null, paths = null, allowedTools = null;
        string? model = null, version = null;

        foreach (var rawLine in yaml.Split('\n'))
        {
            var line = rawLine.TrimEnd('\r').Trim();
            if (line.Length == 0)
                continue;

            // Split on the first colon to get "key" and "value".
            var colonIndex = line.IndexOf(':');
            if (colonIndex < 0)
                continue;

            var key = line[..colonIndex].Trim();
            var value = UnquoteYaml(line[(colonIndex + 1)..].Trim());

            switch (key)
            {
                case "name":                      name = value; break;
                case "description":               description = value; break;
                case "when-to-use":               whenToUse = value; break;
                case "user-invocable":            userInvocable = ParseBool(value); break;
                case "disable-model-invocation":  disableModelInvocation = ParseBool(value); break;
                case "argument-hint":             argumentHint = value; break;
                case "arguments":                 arguments = value; break;
                case "context":                   context = value; break;
                case "agent":                     agent = value; break;
                case "paths":                     paths = value; break;
                case "allowed-tools":             allowedTools = value; break;
                case "model":                     model = value; break;
                case "version":                   version = value; break;
                // Unknown keys are ignored — forward-compatibility.
            }
        }

        return new SkillDefinition
        {
            Name = name ?? directoryName,
            Description = description ?? "",
            WhenToUse = whenToUse,
            UserInvocable = userInvocable ?? true,
            DisableModelInvocation = disableModelInvocation ?? false,
            ArgumentHint = argumentHint,
            ArgumentNames = ParseCommaSeparated(arguments),
            Context = context,
            Agent = agent,
            Paths = ParseCommaSeparated(paths),
            AllowedTools = ParseCommaSeparated(allowedTools),
            Model = model,
            Version = version,
            Content = body,
            SkillDirectory = skillDirectory,
            Source = source
        };
    }

    /// <summary>
    /// Strips surrounding double-quotes from a YAML value, if present.
    /// YAML allows both <c>key: value</c> and <c>key: "value"</c> forms;
    /// the quoted form is used when the value contains characters that would
    /// otherwise be interpreted as YAML syntax (e.g. commas in path lists).
    /// </summary>
    private static string UnquoteYaml(string value) =>
        value is ['"', .., '"'] ? value[1..^1] : value;

    /// <summary>
    /// Parses a YAML boolean value. Accepts "true"/"false" (case-insensitive).
    /// Returns null for anything else, letting the caller fall back to a default.
    /// </summary>
    private static bool? ParseBool(string value) =>
        bool.TryParse(value, out var result) ? result : null;

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
    private static string[]? ParseCommaSeparated(string? value) =>
        string.IsNullOrWhiteSpace(value)
            ? null
            : value.Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries);
}
