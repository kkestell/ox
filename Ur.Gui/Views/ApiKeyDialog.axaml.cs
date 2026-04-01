using Avalonia.Controls;
using Avalonia.Interactivity;
using Ur.Gui.ViewModels;

namespace Ur.Gui.Views;

public partial class ApiKeyDialog : Window
{
    public ApiKeyDialog()
    {
        InitializeComponent();
    }

    private void OnSubmit(object? sender, RoutedEventArgs e)
    {
        if (DataContext is ApiKeyDialogViewModel { Submitted: true })
            Close(true);
    }

    private void OnCancel(object? sender, RoutedEventArgs e)
    {
        Close(false);
    }
}
