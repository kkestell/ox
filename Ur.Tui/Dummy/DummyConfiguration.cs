namespace Ur.Tui.Dummy;

public sealed class DummyConfiguration
{
    public string? ApiKey { get; private set; }
    public string? SelectedModelId { get; private set; }

    public DummyReadiness Readiness => new(GetBlockingIssues());

    public IReadOnlyList<DummyModelInfo> AvailableModels { get; } = new List<DummyModelInfo>
    {
        new("anthropic/claude-sonnet-4-6", "Claude Sonnet 4.6", 200_000, 0.000003m, 0.000015m),
        new("anthropic/claude-opus-4-6", "Claude Opus 4.6", 200_000, 0.000015m, 0.000075m),
        new("anthropic/claude-haiku-4-5", "Claude Haiku 4.5", 200_000, 0.0000008m, 0.000004m),
        new("openai/gpt-4o", "GPT-4o", 128_000, 0.0000025m, 0.00001m),
        new("openai/gpt-4o-mini", "GPT-4o Mini", 128_000, 0.00000015m, 0.0000006m),
        new("openai/o3", "o3", 200_000, 0.00001m, 0.00004m),
        new("openai/o3-mini", "o3 Mini", 200_000, 0.0000011m, 0.0000044m),
        new("google/gemini-2.5-pro", "Gemini 2.5 Pro", 1_000_000, 0.00000125m, 0.00001m),
        new("google/gemini-2.5-flash", "Gemini 2.5 Flash", 1_000_000, 0.00000015m, 0.0000006m),
        new("meta/llama-4-maverick", "Llama 4 Maverick", 1_000_000, 0.0000002m, 0.0000008m),
        new("meta/llama-4-scout", "Llama 4 Scout", 512_000, 0.00000015m, 0.0000006m),
        new("mistral/mistral-large", "Mistral Large", 128_000, 0.000002m, 0.000006m),
        new("mistral/codestral", "Codestral", 256_000, 0.0000003m, 0.0000009m),
        new("deepseek/deepseek-chat-v3", "DeepSeek V3", 128_000, 0.0000003m, 0.0000009m),
        new("deepseek/deepseek-r1", "DeepSeek R1", 128_000, 0.0000008m, 0.0000024m),
        new("cohere/command-r-plus", "Command R+", 128_000, 0.0000025m, 0.00001m),
        new("qwen/qwen3-235b", "Qwen3 235B", 128_000, 0.0000008m, 0.0000024m),
        new("nvidia/llama-3.1-nemotron-70b", "Nemotron 70B", 128_000, 0.0000002m, 0.0000008m),
        new("perplexity/sonar-pro", "Sonar Pro", 200_000, 0.000003m, 0.000015m),
        new("x-ai/grok-3", "Grok 3", 128_000, 0.000003m, 0.000015m),
    };

    public void SetApiKey(string apiKey) => ApiKey = apiKey;

    public void SetSelectedModel(string modelId) => SelectedModelId = modelId;

    private List<DummyBlockingIssue> GetBlockingIssues()
    {
        var issues = new List<DummyBlockingIssue>();

        if (string.IsNullOrWhiteSpace(ApiKey))
            issues.Add(DummyBlockingIssue.MissingApiKey);

        if (string.IsNullOrWhiteSpace(SelectedModelId))
            issues.Add(DummyBlockingIssue.MissingModelSelection);

        return issues;
    }
}
