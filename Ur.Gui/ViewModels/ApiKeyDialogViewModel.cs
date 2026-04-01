using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;

namespace Ur.Gui.ViewModels;

public sealed partial class ApiKeyDialogViewModel : ObservableObject
{
    [ObservableProperty]
    [NotifyCanExecuteChangedFor(nameof(SubmitCommand))]
    private string _apiKey = string.Empty;

    public bool Submitted { get; private set; }

    private bool CanSubmit => !string.IsNullOrWhiteSpace(ApiKey);

    [RelayCommand(CanExecute = nameof(CanSubmit))]
    private void Submit()
    {
        Submitted = true;
    }
}
