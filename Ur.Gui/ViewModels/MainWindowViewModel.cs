using CommunityToolkit.Mvvm.ComponentModel;
using Ur;

namespace Ur.Gui.ViewModels;

public sealed partial class MainWindowViewModel : ObservableObject
{
    private UrHost? _host;

    [ObservableProperty]
    private bool _isLoading = true;

    [ObservableProperty]
    private SessionViewModel? _activeSession;

    [ObservableProperty]
    private string? _selectedModelId;

    public UrHost Host => _host ?? throw new InvalidOperationException("Not yet initialized.");

    public void Initialize(UrHost host)
    {
        _host = host;
        IsLoading = false;
        SelectedModelId = host.Configuration.SelectedModelId;
        ActiveSession = new SessionViewModel(host.CreateSession());
    }

    public void RefreshSelectedModel() =>
        SelectedModelId = _host?.Configuration.SelectedModelId;
}
