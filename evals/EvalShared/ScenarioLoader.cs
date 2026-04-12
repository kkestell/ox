using YamlDotNet.Serialization;
using YamlDotNet.Serialization.NamingConventions;

namespace EvalShared;

/// <summary>
/// Deserializes YAML scenario files into <see cref="ScenarioDefinition"/> instances.
///
/// Uses YamlDotNet with underscore naming convention so YAML fields like
/// <c>timeout_seconds</c> map to C# properties like <c>TimeoutSeconds</c>.
/// Validation rules use a <c>type</c> discriminator field to select the concrete
/// subtype (e.g. <c>command_succeeds</c> → <see cref="CommandSucceedsRule"/>).
/// </summary>
public static class ScenarioLoader
{
    /// <summary>
    /// Loads a single scenario from a YAML file.
    /// </summary>
    public static ScenarioDefinition LoadFile(string path)
    {
        var yaml = File.ReadAllText(path);
        return Load(yaml);
    }

    /// <summary>
    /// Loads all scenarios from a directory of YAML files.
    /// </summary>
    public static List<ScenarioDefinition> LoadDirectory(string directoryPath)
    {
        var files = Directory.GetFiles(directoryPath, "*.yaml")
            .Concat(Directory.GetFiles(directoryPath, "*.yml"))
            .OrderBy(f => f)
            .ToList();

        return files.Select(LoadFile).ToList();
    }

    /// <summary>
    /// Deserializes a scenario from a YAML string.
    /// </summary>
    public static ScenarioDefinition Load(string yaml)
    {
        var deserializer = new DeserializerBuilder()
            .WithNamingConvention(UnderscoredNamingConvention.Instance)
            .IgnoreUnmatchedProperties()
            .Build();

        var raw = deserializer.Deserialize<RawScenario>(yaml);
        return MapToDefinition(raw);
    }

    private static ScenarioDefinition MapToDefinition(RawScenario raw)
    {
        // Repository and WorkspaceFiles are mutually exclusive — one defines the
        // workspace source. Having both is a scenario authoring error.
        if (raw.Repository is not null && raw.WorkspaceFiles is not null)
            throw new InvalidOperationException("Scenario cannot specify both 'repository' and 'workspace_files'");

        return new ScenarioDefinition
        {
            Name = raw.Name ?? throw new InvalidOperationException("Scenario 'name' is required"),
            Description = raw.Description,
            Category = raw.Category,
            Models = raw.Models ?? throw new InvalidOperationException("Scenario 'models' is required"),
            Prompt = raw.Prompt ?? throw new InvalidOperationException("Scenario 'prompt' is required"),
            Repository = raw.Repository is not null
                ? new RepositoryRef
                {
                    Url = raw.Repository.Url ?? throw new InvalidOperationException("Repository 'url' is required"),
                    Commit = raw.Repository.Commit ?? throw new InvalidOperationException("Repository 'commit' is required"),
                    FixCommit = raw.Repository.FixCommit,
                }
                : null,
            WorkspaceFiles = raw.WorkspaceFiles?.Select(wf => new WorkspaceFile
            {
                Path = wf.Path ?? throw new InvalidOperationException("WorkspaceFile 'path' is required"),
                Content = wf.Content ?? throw new InvalidOperationException("WorkspaceFile 'content' is required"),
            }).ToList(),
            ValidationRules = raw.ValidationRules?.Select(MapRule).ToList()
                ?? throw new InvalidOperationException("Scenario 'validation_rules' is required"),
            TimeoutSeconds = raw.TimeoutSeconds ?? 120,
            MaxIterations = raw.MaxIterations,
        };
    }

    /// <summary>
    /// Maps a raw YAML validation rule (with a <c>type</c> discriminator) to the
    /// correct concrete <see cref="ValidationRule"/> subtype.
    /// </summary>
    private static ValidationRule MapRule(RawValidationRule raw)
    {
        return raw.Type?.ToLowerInvariant() switch
        {
            "file_exists" => new FileExistsRule
            {
                Path = raw.Path ?? throw new InvalidOperationException("file_exists rule requires 'path'"),
            },
            "file_not_exists" => new FileNotExistsRule
            {
                Path = raw.Path ?? throw new InvalidOperationException("file_not_exists rule requires 'path'"),
            },
            "file_contains" => new FileContainsRule
            {
                Path = raw.Path ?? throw new InvalidOperationException("file_contains rule requires 'path'"),
                Content = raw.Content ?? throw new InvalidOperationException("file_contains rule requires 'content'"),
            },
            "file_matches" => new FileMatchesRule
            {
                Path = raw.Path ?? throw new InvalidOperationException("file_matches rule requires 'path'"),
                Pattern = raw.Pattern ?? throw new InvalidOperationException("file_matches rule requires 'pattern'"),
            },
            "command_succeeds" => new CommandSucceedsRule
            {
                Command = raw.Command ?? throw new InvalidOperationException("command_succeeds rule requires 'command'"),
            },
            "command_output_contains" => new CommandOutputContainsRule
            {
                Command = raw.Command ?? throw new InvalidOperationException("command_output_contains rule requires 'command'"),
                Output = raw.Output ?? throw new InvalidOperationException("command_output_contains rule requires 'output'"),
            },
            null => throw new InvalidOperationException("Validation rule requires 'type'"),
            _ => throw new InvalidOperationException($"Unknown validation rule type: {raw.Type}"),
        };
    }

    // ── Raw YAML DTOs ────────────────────────────────────────────────
    // YamlDotNet deserializes into these flat classes; MapToDefinition converts
    // them to the typed domain model. This avoids complex YamlDotNet custom
    // deserializer registration for the polymorphic ValidationRule hierarchy.

    // ReSharper disable ClassNeverInstantiated.Local — instantiated by YamlDotNet
    // ReSharper disable UnusedAutoPropertyAccessor.Local — set by YamlDotNet
#pragma warning disable CA1812 // internal class never instantiated — instantiated by YamlDotNet

    private sealed class RawScenario
    {
        public string? Name { get; set; }
        public string? Description { get; set; }
        public string? Category { get; set; }
        public List<string>? Models { get; set; }
        public string? Prompt { get; set; }
        public RawRepository? Repository { get; set; }
        public List<RawWorkspaceFile>? WorkspaceFiles { get; set; }
        public List<RawValidationRule>? ValidationRules { get; set; }
        public int? TimeoutSeconds { get; set; }
        public int? MaxIterations { get; set; }
    }

    private sealed class RawRepository
    {
        public string? Url { get; set; }
        public string? Commit { get; set; }
        public string? FixCommit { get; set; }
    }

    private sealed class RawWorkspaceFile
    {
        public string? Path { get; set; }
        public string? Content { get; set; }
    }

    private sealed class RawValidationRule
    {
        public string? Type { get; set; }
        public string? Path { get; set; }
        public string? Content { get; set; }
        public string? Pattern { get; set; }
        public string? Command { get; set; }
        public string? Output { get; set; }
    }

#pragma warning restore CA1812
    // ReSharper restore UnusedAutoPropertyAccessor.Local
    // ReSharper restore ClassNeverInstantiated.Local
}
