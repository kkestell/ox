using Ox.Agent.Configuration.Keyring;

namespace Ox.Tests.Agent.Configuration;

/// <summary>
/// Tests for <see cref="EnvironmentKeyring"/>. Verifies the env-var naming convention
/// and the no-op write behavior that makes this keyring safe for ephemeral containers.
/// </summary>
public sealed class EnvironmentKeyringTests : IDisposable
{
    // Track env vars we set so we can clean them up.
    private readonly List<string> _envVarsSet = [];

    public void Dispose()
    {
        foreach (var name in _envVarsSet)
            Environment.SetEnvironmentVariable(name, null);
    }

    private void SetEnvVar(string name, string value)
    {
        Environment.SetEnvironmentVariable(name, value);
        _envVarsSet.Add(name);
    }

    [Fact]
    public void GetSecret_ReadsEnvVar_WithUppercaseAccountName()
    {
        SetEnvVar("UR_API_KEY_GOOGLE", "test-google-key");
        var keyring = new EnvironmentKeyring();

        var secret = keyring.GetSecret("ur", "google");

        Assert.Equal("test-google-key", secret);
    }

    [Fact]
    public void GetSecret_ReplacesHyphensWithUnderscores()
    {
        // Provider names like "openai-compatible" should map to UR_API_KEY_OPENAI_COMPATIBLE.
        SetEnvVar("UR_API_KEY_OPENAI_COMPATIBLE", "test-key");
        var keyring = new EnvironmentKeyring();

        var secret = keyring.GetSecret("ur", "openai-compatible");

        Assert.Equal("test-key", secret);
    }

    [Fact]
    public void GetSecret_ReplacesDotsWithUnderscores()
    {
        SetEnvVar("UR_API_KEY_ZAI_CODING", "test-zai-key");
        var keyring = new EnvironmentKeyring();

        var secret = keyring.GetSecret("ur", "zai.coding");

        Assert.Equal("test-zai-key", secret);
    }

    [Fact]
    public void GetSecret_ReturnsNull_WhenEnvVarNotSet()
    {
        var keyring = new EnvironmentKeyring();

        var secret = keyring.GetSecret("ur", "nonexistent-provider");

        Assert.Null(secret);
    }

    [Fact]
    public void GetSecret_IgnoresServiceParameter()
    {
        // The service parameter is always "ur" in practice, but EnvironmentKeyring
        // ignores it entirely — the env var is derived only from the account.
        SetEnvVar("UR_API_KEY_OPENROUTER", "test-key");
        var keyring = new EnvironmentKeyring();

        Assert.Equal("test-key", keyring.GetSecret("anything", "openrouter"));
    }

    [Fact]
    public void SetSecret_IsNoOp()
    {
        var keyring = new EnvironmentKeyring();

        // Should not throw.
        keyring.SetSecret("ur", "google", "some-key");
    }

    [Fact]
    public void DeleteSecret_IsNoOp()
    {
        var keyring = new EnvironmentKeyring();

        // Should not throw.
        keyring.DeleteSecret("ur", "google");
    }
}
