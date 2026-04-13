using Ox.Connect;

namespace Ox.Tests.Connect;

/// <summary>
/// Unit tests for <see cref="ConnectWizardController"/>.
///
/// The controller is a pure state machine — no DI, no async, no I/O. Tests
/// drive it through the public mutation methods and assert on observable state.
/// </summary>
public sealed class ConnectWizardControllerTests
{
    // ── Helpers ───────────────────────────────────────────────────────────

    private static readonly IReadOnlyList<(string Key, string Name)> SampleProviders =
    [
        ("google", "Google"),
        ("ollama", "Ollama"),
        ("openai", "OpenAI"),
    ];

    private static readonly IReadOnlyList<(string Id, string Name)> GoogleModels =
    [
        ("gemini-pro", "Gemini Pro"),
        ("gemini-flash", "Gemini Flash"),
    ];

    private static readonly IReadOnlyList<(string Id, string Name)> OllamaModels =
    [
        ("qwen3:8b", "Qwen3 8B"),
    ];

    // ── Start ─────────────────────────────────────────────────────────────

    [Fact]
    public void Start_SetsActiveAndShowsProviders()
    {
        var wizard = new ConnectWizardController();

        wizard.Start(SampleProviders, required: false);

        Assert.True(wizard.IsActive);
        Assert.Equal(WizardStep.SelectProvider, wizard.CurrentStep);
        Assert.Equal(0, wizard.SelectedIndex);
        Assert.Equal(["Google", "Ollama", "OpenAI"], wizard.DisplayItems);
    }

    [Fact]
    public void Start_SetsRequiredFlag()
    {
        var wizard = new ConnectWizardController();

        wizard.Start(SampleProviders, required: true);

        Assert.True(wizard.IsRequired);
    }

    [Fact]
    public void Start_ResetsSelectionAndEditor()
    {
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.NavigateDown();
        wizard.KeyEditor.InsertChar('x');

        // Re-start should reset state.
        wizard.Start(SampleProviders, required: false);

        Assert.Equal(0, wizard.SelectedIndex);
        Assert.Equal("", wizard.KeyEditor.Text);
    }

    // ── Navigation ────────────────────────────────────────────────────────

    [Fact]
    public void NavigateDown_MovesSelectionForward()
    {
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);

        wizard.NavigateDown();

        Assert.Equal(1, wizard.SelectedIndex);
    }

    [Fact]
    public void NavigateDown_ClampsAtLastItem()
    {
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);

        wizard.NavigateDown();
        wizard.NavigateDown();
        wizard.NavigateDown(); // one past the end — should stay at 2

        Assert.Equal(2, wizard.SelectedIndex);
    }

    [Fact]
    public void NavigateUp_MovesSelectionBackward()
    {
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.NavigateDown();

        wizard.NavigateUp();

        Assert.Equal(0, wizard.SelectedIndex);
    }

    [Fact]
    public void NavigateUp_ClampsAtZero()
    {
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);

        wizard.NavigateUp(); // already at 0 — should stay

        Assert.Equal(0, wizard.SelectedIndex);
    }

    // ── Happy path: provider requires API key ─────────────────────────────

    [Fact]
    public void HappyPath_WithApiKey_ReturnsExpectedResult()
    {
        // Start → navigate to Google (index 0) → confirm → enter key → select
        // second model → confirm.
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);

        // Step 1: select Google (index 0, already selected).
        wizard.ProviderConfirmed("google", requiresApiKey: true, GoogleModels);

        Assert.Equal(WizardStep.EnterApiKey, wizard.CurrentStep);
        Assert.True(wizard.IsActive);

        // Step 2: type an API key and confirm.
        wizard.KeyEditor.InsertChar('s');
        wizard.KeyEditor.InsertChar('e');
        wizard.KeyEditor.InsertChar('c');
        wizard.ApiKeyConfirmed();

        Assert.Equal(WizardStep.SelectModel, wizard.CurrentStep);
        Assert.Equal(["Gemini Pro", "Gemini Flash"], wizard.DisplayItems);

        // Step 3: navigate down to Gemini Flash and confirm.
        wizard.NavigateDown();
        var result = wizard.ModelConfirmed();

        Assert.NotNull(result);
        Assert.Equal("google", result.Value.ProviderId);
        Assert.Equal("gemini-flash", result.Value.ModelId);
        Assert.Equal("sec", result.Value.ApiKey);
        Assert.False(wizard.IsActive);
    }

    // ── Happy path: provider skips API key step ───────────────────────────

    [Fact]
    public void HappyPath_WithoutApiKey_SkipsKeyStep()
    {
        // Ollama does not require an API key — ProviderConfirmed should jump
        // directly to SelectModel.
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);

        wizard.ProviderConfirmed("ollama", requiresApiKey: false, OllamaModels);

        Assert.Equal(WizardStep.SelectModel, wizard.CurrentStep);
        Assert.Equal(["Qwen3 8B"], wizard.DisplayItems);
    }

    [Fact]
    public void HappyPath_WithoutApiKey_ModelConfirmedReturnsNullApiKey()
    {
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.ProviderConfirmed("ollama", requiresApiKey: false, OllamaModels);

        var result = wizard.ModelConfirmed();

        Assert.NotNull(result);
        Assert.Equal("ollama", result.Value.ProviderId);
        Assert.Equal("qwen3:8b", result.Value.ModelId);
        Assert.Null(result.Value.ApiKey); // no key step → no key returned
    }

    // ── Empty API key ─────────────────────────────────────────────────────

    [Fact]
    public void ApiKeyConfirmed_WithStoredKeyMask_ReturnsNullApiKey()
    {
        // Pressing Enter on the stored-key mask means "keep the existing key"
        // — the caller checks for null and skips SetApiKey.
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.ProviderConfirmed("google", requiresApiKey: true, GoogleModels, hasStoredApiKey: true);

        // Do NOT type anything — just confirm the masked placeholder.
        wizard.ApiKeyConfirmed();

        // Step must have advanced; without this assertion a broken
        // AdvanceToSelectModel() call could go undetected.
        Assert.Equal(WizardStep.SelectModel, wizard.CurrentStep);

        var result = wizard.ModelConfirmed();

        Assert.NotNull(result);
        Assert.Null(result.Value.ApiKey);
    }

    [Fact]
    public void ProviderConfirmed_WithStoredApiKey_ShowsMaskedPlaceholder()
    {
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);

        wizard.ProviderConfirmed("google", requiresApiKey: true, GoogleModels, hasStoredApiKey: true);

        Assert.Equal(WizardStep.EnterApiKey, wizard.CurrentStep);
        Assert.Equal("*****************", wizard.KeyEditor.Text);
        Assert.True(wizard.IsShowingStoredApiKeyMask);
    }

    [Fact]
    public void TryConfirmApiKey_WithStoredApiKeyMask_AdvancesWithoutReplacingKey()
    {
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.ProviderConfirmed("google", requiresApiKey: true, GoogleModels, hasStoredApiKey: true);

        var advanced = wizard.TryConfirmApiKey();

        Assert.True(advanced);
        Assert.Equal(WizardStep.SelectModel, wizard.CurrentStep);
        var result = wizard.ModelConfirmed();
        Assert.NotNull(result);
        Assert.Null(result.Value.ApiKey);
    }

    [Fact]
    public void TryConfirmApiKey_WithoutStoredApiKeyAndEmptyField_DoesNotAdvance()
    {
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.ProviderConfirmed("google", requiresApiKey: true, GoogleModels, hasStoredApiKey: false);

        var advanced = wizard.TryConfirmApiKey();

        Assert.False(advanced);
        Assert.Equal(WizardStep.EnterApiKey, wizard.CurrentStep);
    }

    [Fact]
    public void InsertApiKeyChar_WhenStoredMaskVisible_ReplacesMaskWithTypedKey()
    {
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.ProviderConfirmed("google", requiresApiKey: true, GoogleModels, hasStoredApiKey: true);

        wizard.InsertApiKeyChar('x');

        Assert.Equal("x", wizard.KeyEditor.Text);
        Assert.False(wizard.IsShowingStoredApiKeyMask);
    }

    // ── Navigation at SelectModel step ───────────────────────────────────

    [Fact]
    public void NavigateDown_InSelectModel_MovesSelection()
    {
        // NavigateDown has a branch: SelectProvider uses _providers.Count;
        // everything else uses _models.Count. This test exercises the else branch.
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.ProviderConfirmed("google", requiresApiKey: false, GoogleModels);

        wizard.NavigateDown();

        Assert.Equal(1, wizard.SelectedIndex);
    }

    [Fact]
    public void NavigateDown_InSelectModel_ClampsAtLastModel()
    {
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.ProviderConfirmed("google", requiresApiKey: false, GoogleModels);

        wizard.NavigateDown();
        wizard.NavigateDown(); // past the end — should clamp at 1 (last index of 2 models)

        Assert.Equal(1, wizard.SelectedIndex);
    }

    [Fact]
    public void NavigateDown_InSelectModel_SingleModel_StaysAtZero()
    {
        // Edge case: provider with exactly one model — down should stay at 0.
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.ProviderConfirmed("ollama", requiresApiKey: false, OllamaModels);

        wizard.NavigateDown();

        Assert.Equal(0, wizard.SelectedIndex);
    }

    [Fact]
    public void NavigateUp_InSelectModel_MovesBack()
    {
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.ProviderConfirmed("google", requiresApiKey: false, GoogleModels);
        wizard.NavigateDown(); // → index 1

        wizard.NavigateUp();

        Assert.Equal(0, wizard.SelectedIndex);
    }

    // ── Provider key capture by index ─────────────────────────────────────

    [Fact]
    public void HappyPath_NonFirstProvider_ReturnsCorrectProviderKey()
    {
        // Navigating to a non-first provider should return that provider's key,
        // not the first provider's or the raw index.
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.NavigateDown(); // → index 1 = Ollama
        wizard.ProviderConfirmed("ollama", requiresApiKey: false, OllamaModels);

        var result = wizard.ModelConfirmed();

        Assert.NotNull(result);
        Assert.Equal("ollama", result.Value.ProviderId);
    }

    // ── ProviderConfirmed clears the key editor ───────────────────────────

    [Fact]
    public void ProviderConfirmed_ClearsKeyEditor()
    {
        // If the user opens the wizard twice, the second run should not inherit
        // leftover key text from the first. ProviderConfirmed must clear the editor.
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.ProviderConfirmed("google", requiresApiKey: true, GoogleModels);
        wizard.KeyEditor.InsertChar('s');

        // Re-open and confirm the provider again.
        wizard.Start(SampleProviders, required: false);
        wizard.ProviderConfirmed("google", requiresApiKey: true, GoogleModels);

        Assert.Equal("", wizard.KeyEditor.Text);
    }

    // ── AdvanceToSelectModel resets SelectedIndex ─────────────────────────

    [Fact]
    public void ProviderConfirmed_ResetsSelectedIndex()
    {
        // After navigating down in the provider list and confirming, the
        // wizard advances to model selection with SelectedIndex reset to 0.
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.NavigateDown(); // index → 1
        wizard.ProviderConfirmed("ollama", requiresApiKey: false, OllamaModels);

        Assert.Equal(0, wizard.SelectedIndex);
    }

    // ── ApiKeyConfirmed advances step ────────────────────────────────────

    [Fact]
    public void ApiKeyConfirmed_AdvancesToSelectModel()
    {
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.ProviderConfirmed("google", requiresApiKey: true, GoogleModels);
        wizard.InsertApiKeyChar('s');

        wizard.ApiKeyConfirmed();

        Assert.Equal(WizardStep.SelectModel, wizard.CurrentStep);
    }

    // ── Cancel ────────────────────────────────────────────────────────────

    [Fact]
    public void Cancel_DeactivatesWizard_WhenRequired()
    {
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: true);

        wizard.Cancel();

        // The wizard is inactive. OxApp is responsible for setting _exit when
        // IsRequired was true before Cancel() was called — so we just verify
        // the controller side here.
        Assert.False(wizard.IsActive);
    }

    [Fact]
    public void Cancel_DeactivatesWizard_WhenNotRequired()
    {
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);

        wizard.Cancel();

        Assert.False(wizard.IsActive);
    }

    // ── ModelConfirmed edge cases ──────────────────────────────────────────

    [Fact]
    public void ModelConfirmed_ResetsIsActive()
    {
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.ProviderConfirmed("ollama", requiresApiKey: false, OllamaModels);

        wizard.ModelConfirmed();

        Assert.False(wizard.IsActive);
    }

    [Fact]
    public void ModelConfirmed_WithEmptyModels_ReturnsNull()
    {
        // A provider with no models — ModelConfirmed should return null rather
        // than throwing an index-out-of-range exception.
        var wizard = new ConnectWizardController();
        wizard.Start(SampleProviders, required: false);
        wizard.ProviderConfirmed("google", requiresApiKey: false, []);

        var result = wizard.ModelConfirmed();

        Assert.Null(result);
    }
}
