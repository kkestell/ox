using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Ur;

namespace Ur.Gui.ViewModels;

public sealed partial class ExtensionsDialogViewModel : ObservableObject
{
    private readonly UrExtensionCatalog _catalog;

    public ObservableCollection<UrExtensionInfo> Extensions { get; } = [];

    [ObservableProperty]
    [NotifyCanExecuteChangedFor(nameof(ToggleCommand))]
    [NotifyCanExecuteChangedFor(nameof(ResetCommand))]
    private UrExtensionInfo? _selectedExtension;

    public ExtensionsDialogViewModel(UrExtensionCatalog catalog)
    {
        _catalog = catalog;
        Reload();
    }

    private bool HasSelection => SelectedExtension is not null;

    [RelayCommand(CanExecute = nameof(HasSelection))]
    private async Task ToggleAsync()
    {
        if (SelectedExtension is null) return;
        await ToggleExtensionAsync(SelectedExtension);
    }

    [RelayCommand]
    private async Task ToggleExtensionAsync(UrExtensionInfo ext)
    {
        var updated = await _catalog.SetEnabledAsync(ext.Id, !ext.DesiredEnabled);
        Replace(ext, updated);
        if (SelectedExtension == ext)
            SelectedExtension = updated;
    }

    [RelayCommand(CanExecute = nameof(HasSelection))]
    private async Task ResetAsync()
    {
        if (SelectedExtension is null) return;
        var updated = await _catalog.ResetAsync(SelectedExtension.Id);
        Replace(SelectedExtension, updated);
        SelectedExtension = updated;
    }

    private void Reload()
    {
        Extensions.Clear();
        foreach (var ext in _catalog.List())
            Extensions.Add(ext);
    }

    private void Replace(UrExtensionInfo old, UrExtensionInfo updated)
    {
        var idx = Extensions.IndexOf(old);
        if (idx >= 0)
            Extensions[idx] = updated;
    }
}
