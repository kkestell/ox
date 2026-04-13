using Ox.Agent.Hosting;
using Ox.Agent.Permissions;
using Ox.Agent.Sessions;
using Ox.Agent.Skills;
using Ox.App.Commands;
using Ox.App.Configuration;
using Ox.App.Connect;
using Ox.App.Conversation;
using Ox.App.Input;
using Ox.App.Permission;
using Ox.App.Views;
using Ox.Terminal.Input;

namespace Ox.App;

/// <summary>
/// Main application loop for the Ox TUI.
///
/// The coordinator. After the Wave-3 refactor, OxApp runs the frame loop and
/// wires together focused collaborators — <see cref="InputRouter"/>,
/// <see cref="TurnController"/>, <see cref="AgentEventApplier"/>,
/// <see cref="PermissionPromptBridge"/>, <see cref="CommandDispatcher"/>, and
/// <see cref="RenderCompositor"/> — so input handling, turn lifecycle, event
/// application, permission prompting, slash-command dispatch, and frame
/// composition each live in their own class.
///
/// Runs a single-threaded render loop that wakes on three signals: stdin
/// input (via Te's InputCoordinator), Ur agent events (via ConcurrentQueue),
/// and a periodic tick (for the throbber animation). All state mutation
/// happens on the main thread when queues are drained — background threads
/// only enqueue.
/// </summary>
public sealed class OxApp : IDisposable
{
    // External inputs / hosts.
    private readonly InputCoordinator _coordinator;
    private readonly OxHost _host;
    private readonly ModelCatalog _oxConfig;

    // Views the coordinator mutates directly (adds error/user-message/cancel
    // entries after submit). Kept on OxApp rather than a collaborator because
    // the applier already owns the reference and both need to agree on it.
    private readonly ConversationView _conversationView = new();

    // Composer chrome — mutated from input-mode callbacks and surfaced to the
    // render compositor on each frame.
    private readonly TextEditor _editor = new();
    private readonly Throbber _throbber = new();
    private readonly Autocomplete _autocomplete;
    private readonly ConnectWizardController _wizard = new();

    // Collaborators — each owns a slice of what used to be OxApp's god-class state.
    private readonly RenderCompositor _renderCompositor;
    private readonly InputRouter _inputRouter;
    private readonly TurnController _turnController;
    private readonly AgentEventApplier _agentEventApplier;
    private readonly PermissionPromptBridge _permissionBridge;
    private readonly CommandDispatcher _commandDispatcher;

    // Wake signal for the main loop. Shared with TurnController and the
    // permission bridge via a wake-action callback so those collaborators
    // don't have to reference SemaphoreSlim themselves.
    private readonly SemaphoreSlim _wakeSignal = new(0);

    // Turn state owned by the applier. Held here so Render() can read the
    // currently active model and context-fill percentage for the status line.
    private readonly TurnState _turnState = new();

    private OxSession? _session;
    private bool _exit;

    public OxApp(OxHost host, ModelCatalog oxConfig, InputCoordinator coordinator, int width, int height, string workspacePath)
    {
        _host = host;
        _oxConfig = oxConfig;
        _coordinator = coordinator;
        _renderCompositor = new RenderCompositor(width, height, workspacePath, OxThemePalette.Ox.Background);

        // Build autocomplete from the host's command registry. The
        // argument-completion dictionary maps command names (lowercase) to
        // their completable argument lists. The engine prefix-matches against
        // these lists when the input already contains a space (argument phase).
        var commandRegistry = new CommandRegistry(host.BuiltInCommands, host.Skills);
        var argumentCompletions = new Dictionary<string, IReadOnlyList<string>>
        {
            ["model"] = oxConfig.ListAllModelIds(),
        };
        _autocomplete = new Autocomplete(new AutocompleteEngine(commandRegistry, argumentCompletions));

        // Wire the collaborators. WakeMainLoop is threaded into both the
        // turn controller and the permission bridge so each can signal the
        // UI without depending on the semaphore's concrete type.
        void WakeMainLoop() => _wakeSignal.Release();

        _permissionBridge = new PermissionPromptBridge(WakeMainLoop) { View = { WorkspacePath = workspacePath } };
        _turnController = new TurnController(WakeMainLoop);
        _agentEventApplier = new AgentEventApplier(_conversationView, oxConfig, () => _session?.ActiveModelId);
        _commandDispatcher = new CommandDispatcher(() => _session, commandRegistry, oxConfig);

        _inputRouter = new InputRouter(new IInputMode[]
        {
            // Permission prompt wins over everything else — it's modal.
            new PermissionInputMode(_permissionBridge),
            // Connect wizard intercepts input whenever visible.
            new WizardInputMode(_wizard, AdvanceWizard, () => _exit = true),
            // Default composer surface.
            new ChatInputMode(
                _editor,
                turnActive: () => _turnController.IsActive,
                requestExit: () => _exit = true,
                cancelTurn: CancelTurn,
                onScreenDumpShortcut: _ => _renderCompositor.CaptureScreenDump(),
                tryApplyAutocomplete: () => _autocomplete.TryApply(_editor),
                validateAndTake: TryTakeAndSubmit),
        });

        // Create session with permission callback. The bridge implements
        // RequestPermissionAsync so the background turn task blocks on the TCS
        // until the main loop resolves it from a keystroke.
        var callbacks = new TurnCallbacks
        {
            RequestPermissionAsync = _permissionBridge.RequestAsync,
        };
        _session = host.CreateSession(callbacks);

        // On first run (or when the model is not configured), open the connect
        // wizard immediately so the user can configure a provider without ever
        // seeing a plain-console prompt. IsRequired=true means Escape exits
        // the app rather than dismissing back to an un-configured chat screen.
        if (!host.Configuration.Readiness.CanRunTurns)
            _wizard.Start(oxConfig.ListProviders(), required: true);
    }

    /// <summary>Run the main application loop until exit is signalled.</summary>
    public async Task RunAsync()
    {
        while (!_exit)
        {
            _renderCompositor.CheckResize();
            DrainInput();
            DrainAgentEvents();
            _renderCompositor.Render(
                _conversationView, _permissionBridge, _editor, _autocomplete,
                _throbber, _wizard,
                turnActive: _turnController.IsActive,
                contextPercent: _turnState.ContextPercent,
                statusModelId: _turnController.IsActive
                    ? _session?.ActiveModelId
                    : _host.Configuration.SelectedModelId);

            // Break before blocking if exit was requested during this
            // iteration (e.g. by Ctrl+C or /quit). Without this check the
            // loop would hang on the await below, requiring an extra keypress
            // to unblock.
            if (_exit) break;

            // Wait for the next signal: input, Ur event, or tick.
            var inputReady = _coordinator.Reader.WaitToReadAsync().AsTask();
            var wakeReady = _wakeSignal.WaitAsync();

            // 1-second tick when turn is active (for throbber animation).
            var tick = _turnController.IsActive
                ? Task.Delay(TimeSpan.FromSeconds(1))
                : Task.Delay(Timeout.Infinite);

            await Task.WhenAny(inputReady, wakeReady, tick);

            if (_turnController.IsActive)
                _throbber.Tick();
        }
    }

    public void Dispose()
    {
        // Write session metrics before shutting down. DisposeAsync is cheap
        // (one file write) so blocking here is acceptable — the TUI is
        // already exiting.
        _session?.DisposeAsync().AsTask().GetAwaiter().GetResult();
        _turnController.Dispose();
        _wakeSignal.Dispose();
    }

    // Drains stdin events and routes them to either the input router (keys)
    // or a direct mouse handler (no modal floating panel currently cares
    // about mouse input, so the router is bypassed).
    private void DrainInput()
    {
        while (_coordinator.Reader.TryRead(out var inputEvent))
        {
            switch (inputEvent)
            {
                case KeyInputEvent keyEvent:
                    _inputRouter.HandleKey(keyEvent.Key);
                    break;
                case MouseInputEvent mouseEvent:
                    // Scroll wheel → manual scroll.
                    if (mouseEvent.Mouse.HasFlag(MouseFlags.WheeledUp))
                        _conversationView.ScrollUp(3);
                    else if (mouseEvent.Mouse.HasFlag(MouseFlags.WheeledDown))
                        _conversationView.ScrollDown(3);
                    break;
            }
        }
    }

    private void DrainAgentEvents()
    {
        while (_turnController.Events.TryDequeue(out var evt))
        {
            var outcome = _agentEventApplier.Apply(evt, _turnState);
            if (outcome is DrainOutcome.TurnEnded or DrainOutcome.TurnEndedFatal)
                OnTurnEnded();
        }
    }

    // Central place for "a turn just finished" cleanup: stop the throbber,
    // clear streaming entries, and kick off any input queued while the turn
    // was running. Also used by CancelTurn after adding the cancellation
    // entry so the post-turn state is consistent either way.
    private void OnTurnEnded()
    {
        _turnController.MarkEnded();
        _throbber.Reset();
        _turnState.ResetStreamingEntries();

        var queued = _turnController.TakeQueuedInput();
        if (queued is not null)
        {
            _conversationView.AddEntry(new UserMessageEntry(queued));
            StartTurn(queued);
        }
    }

    private void CancelTurn()
    {
        _turnController.Cancel();
        _throbber.Reset();
        _turnState.ResetStreamingEntries();
        _conversationView.AddEntry(new CancellationEntry());
    }

    // Validates the current editor text via the command dispatcher, then —
    // if valid — clears the editor and runs the dispatcher. Called by
    // ChatInputMode on Enter. Returns the submitted text (or empty string
    // when validation rejected the line) for future callback uses.
    private string TryTakeAndSubmit()
    {
        var pending = _editor.Text.Trim();
        if (pending.Length == 0 || !_commandDispatcher.TryValidateForSubmission(pending))
            return string.Empty;

        _editor.Clear();

        switch (_commandDispatcher.Dispatch(pending))
        {
            case CommandOutcome.Exit:
                _exit = true;
                break;

            case CommandOutcome.OpenWizard:
                // Required=false: Escape dismisses back to the existing chat
                // session without exiting the app.
                _wizard.Start(_oxConfig.ListProviders(), required: false);
                break;

            case CommandOutcome.Handled handled:
                if (handled.InvalidatedContextWindow)
                    _agentEventApplier.InvalidateContextWindowCache();
                break;

            case CommandOutcome.Unknown unknown:
                _conversationView.AddEntry(new ErrorEntry($"Unknown command: {unknown.Name}"));
                break;

            case CommandOutcome.StartTurn st:
                _conversationView.AddEntry(new UserMessageEntry(st.Text));
                if (_turnController.IsActive)
                    _turnController.QueueWhileActive(st.Text);
                else
                    StartTurn(st.Text);
                break;
        }

        return pending;
    }

    private void StartTurn(string input)
    {
        if (_session is null) return;

        _turnState.ResetStreamingEntries();
        _throbber.Start();
        _turnController.Start(_session, input);
    }

    /// <summary>
    /// Advances the wizard state machine one step. Called by
    /// <see cref="WizardInputMode"/> on Enter during list steps.
    ///
    /// Lives here (rather than inside the mode) because advancing requires
    /// reading from <see cref="ModelCatalog"/> and writing to
    /// <see cref="Ox.Agent.Configuration.OxConfiguration"/> — coordinator-level
    /// concerns the mode has no business owning.
    /// </summary>
    private void AdvanceWizard()
    {
        if (_wizard.CurrentStep == WizardStep.SelectProvider)
        {
            var providers = _oxConfig.ListProviders();
            if (_wizard.SelectedIndex >= providers.Count) return;

            var (key, _) = providers[_wizard.SelectedIndex];
            var requiresKey = _oxConfig.ProviderRequiresApiKey(key);
            var models = _oxConfig.ListModelsForProvider(key);
            var hasStoredApiKey = requiresKey && _host.Configuration.HasApiKey(key);
            _wizard.ProviderConfirmed(key, requiresKey, models, hasStoredApiKey);
        }
        else if (_wizard.CurrentStep == WizardStep.SelectModel)
        {
            var result = _wizard.ModelConfirmed();
            if (result is null) return;

            var (providerId, modelId, apiKey) = result.Value;

            // Persist the selections. SetApiKey is skipped when apiKey is
            // null — an empty key field means "keep whatever is already in
            // the keyring".
            _host.Configuration.SetSelectedModel($"{providerId}/{modelId}");
            if (apiKey is not null)
                _host.Configuration.SetApiKey(apiKey, providerId);

            // Invalidate the context-window cache so the status line picks
            // up the new model's window size on the next render.
            _agentEventApplier.InvalidateContextWindowCache();
        }
    }
}
