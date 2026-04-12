using Ox.Input;

namespace Ox.Connect;

/// <summary>
/// The three sequential steps the connect wizard moves through.
/// </summary>
public enum WizardStep
{
    SelectProvider,
    EnterApiKey,
    SelectModel,
}

/// <summary>
/// State machine for the connect wizard.
///
/// The wizard walks the user through three sequential steps — provider
/// selection, API key entry (skipped for providers like Ollama that don't
/// need one), and model selection — and returns the final (providerId, modelId,
/// apiKey?) triple when the user confirms. OxApp owns this controller and
/// mediates between it and UrConfiguration, the same way OxApp mediates
/// between the permission prompt and the permission system.
///
/// All state transitions are triggered by OxApp via the public mutation
/// methods; the view reads the public state properties to render the current
/// step without reaching back into OxApp.
/// </summary>
public sealed class ConnectWizardController
{
    // The full provider list, set when the wizard is started.
    private IReadOnlyList<(string Key, string Name)> _providers = [];

    // The model list for the currently selected provider, set in ProviderConfirmed.
    private IReadOnlyList<(string Id, string Name)> _models = [];

    // Selections accumulated across steps.
    private string? _selectedProviderKey;

    // API key entered by the user (null = not yet at that step; empty = user left it blank).
    private string? _selectedApiKey;

    /// <summary>Whether the wizard overlay is currently shown.</summary>
    public bool IsActive { get; private set; }

    /// <summary>
    /// True when the wizard was opened because no configuration exists (first run).
    /// When required, Escape exits the application rather than just dismissing the
    /// wizard — there is no usable config to fall back to.
    /// </summary>
    public bool IsRequired { get; private set; }

    /// <summary>The step currently being presented to the user.</summary>
    public WizardStep CurrentStep { get; private set; }

    /// <summary>
    /// Displayable strings for the current step's list. For SelectProvider
    /// these are provider display names; for SelectModel they are model names.
    /// Not meaningful during EnterApiKey.
    /// </summary>
    public IReadOnlyList<string> DisplayItems { get; private set; } = [];

    /// <summary>
    /// Zero-based index of the highlighted item in the current list step.
    /// Invariant: always in [0, DisplayItems.Count) while a list step is active.
    /// </summary>
    public int SelectedIndex { get; private set; }

    /// <summary>Text editor for the API key input field (EnterApiKey step).</summary>
    public TextEditor KeyEditor { get; } = new();

    // ── Lifecycle ────────────────────────────────────────────────────────

    /// <summary>
    /// Open the wizard at the SelectProvider step. Called on first run (required=true)
    /// when no configuration exists, and also by the /connect command (required=false)
    /// to let the user switch providers mid-session.
    /// </summary>
    public void Start(IReadOnlyList<(string Key, string Name)> providers, bool required)
    {
        _providers = providers;
        _models = [];
        _selectedProviderKey = null;
        _selectedApiKey = null;

        IsActive = true;
        IsRequired = required;
        CurrentStep = WizardStep.SelectProvider;
        SelectedIndex = 0;
        DisplayItems = providers.Select(p => p.Name).ToList();
        KeyEditor.Clear();
    }

    // ── Step transitions ─────────────────────────────────────────────────

    /// <summary>
    /// Called by OxApp after the user confirms a provider selection.
    /// Advances to EnterApiKey or SelectModel depending on whether the provider
    /// requires a key, and loads the model list for the chosen provider.
    /// </summary>
    public void ProviderConfirmed(
        string selectedKey,
        bool requiresApiKey,
        IReadOnlyList<(string Id, string Name)> models)
    {
        _selectedProviderKey = selectedKey;
        _models = models;

        if (requiresApiKey)
        {
            // Stay at EnterApiKey; the user types their key here.
            CurrentStep = WizardStep.EnterApiKey;
            KeyEditor.Clear();
        }
        else
        {
            // Skip the key step — jump straight to model selection.
            _selectedApiKey = null;
            AdvanceToSelectModel();
        }
    }

    /// <summary>
    /// Called by OxApp after the user presses Enter on the API key field.
    /// Captures whatever is in KeyEditor (may be empty — empty means "keep
    /// existing key") and advances to model selection.
    /// </summary>
    public void ApiKeyConfirmed()
    {
        // Preserve the entered text; OxApp decides whether to call SetApiKey
        // based on whether it is non-empty.
        _selectedApiKey = KeyEditor.Text;
        AdvanceToSelectModel();
    }

    /// <summary>
    /// Called by OxApp when the user confirms a model selection.
    /// Returns the final (providerId, modelId, apiKey?) triple and deactivates
    /// the wizard. Returns null only when the model list is somehow empty —
    /// callers should guard against that before calling this.
    /// </summary>
    public (string ProviderId, string ModelId, string? ApiKey)? ModelConfirmed()
    {
        if (SelectedIndex < 0 || SelectedIndex >= _models.Count)
            return null;

        var modelId = _models[SelectedIndex].Id;

        // A non-empty API key means the user typed one; empty means "leave
        // existing key in place" — OxApp skips SetApiKey in that case.
        var apiKey = string.IsNullOrEmpty(_selectedApiKey) ? null : _selectedApiKey;

        IsActive = false;
        return (_selectedProviderKey!, modelId, apiKey);
    }

    /// <summary>
    /// Dismiss the wizard without saving any changes. When IsRequired is true
    /// (first run), OxApp should exit the application after calling Cancel
    /// because there is no working config to fall back to.
    /// </summary>
    public void Cancel()
    {
        IsActive = false;
    }

    // ── Navigation ───────────────────────────────────────────────────────

    /// <summary>Move the selection cursor up one row (clamped at 0).</summary>
    public void NavigateUp()
    {
        if (SelectedIndex > 0)
            SelectedIndex--;
    }

    /// <summary>Move the selection cursor down one row (clamped at list end).</summary>
    public void NavigateDown()
    {
        var count = CurrentStep == WizardStep.SelectProvider ? _providers.Count : _models.Count;
        if (SelectedIndex < count - 1)
            SelectedIndex++;
    }

    // ── Helpers ──────────────────────────────────────────────────────────

    private void AdvanceToSelectModel()
    {
        CurrentStep = WizardStep.SelectModel;
        SelectedIndex = 0;
        DisplayItems = _models.Select(m => m.Name).ToList();
    }
}
