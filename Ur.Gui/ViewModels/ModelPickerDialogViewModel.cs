using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Ur.Providers;

namespace Ur.Gui.ViewModels;

public sealed partial class ModelPickerDialogViewModel : ObservableObject
{
    private readonly IReadOnlyList<ModelInfo> _allModels;

    [ObservableProperty]
    private string _filterText = string.Empty;

    [ObservableProperty]
    [NotifyCanExecuteChangedFor(nameof(SelectCommand))]
    private ModelInfo? _selectedModel;

    public ObservableCollection<ModelInfo> FilteredModels { get; } = [];

    public bool Submitted { get; private set; }

    public ModelPickerDialogViewModel(IReadOnlyList<ModelInfo> models)
    {
        _allModels = models;
        ApplyFilter();
    }

    partial void OnFilterTextChanged(string value) => ApplyFilter();

    private void ApplyFilter()
    {
        FilteredModels.Clear();

        var term = FilterText.Trim();
        var matches = string.IsNullOrEmpty(term)
            ? _allModels
            : _allModels.Where(m =>
                m.Name.Contains(term, StringComparison.OrdinalIgnoreCase) ||
                m.Id.Contains(term, StringComparison.OrdinalIgnoreCase));

        foreach (var m in matches.OrderByDescending(m => m.OutputCostPerMToken))
            FilteredModels.Add(m);

        if (SelectedModel is null || !FilteredModels.Contains(SelectedModel))
            SelectedModel = FilteredModels.FirstOrDefault();
    }

    private bool CanSelect => SelectedModel is not null;

    [RelayCommand(CanExecute = nameof(CanSelect))]
    private void Select()
    {
        Submitted = true;
    }
}
