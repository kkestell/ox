using Avalonia.Controls;
using Avalonia.Interactivity;
using Ur.Gui.ViewModels;

namespace Ur.Gui.Views;

public partial class ModelPickerDialog : Window
{
    public ModelPickerDialog()
    {
        InitializeComponent();
    }

    private void OnSelect(object? sender, RoutedEventArgs e)
    {
        if (DataContext is ModelPickerDialogViewModel { Submitted: true })
            Close(true);
    }

    private void OnCancel(object? sender, RoutedEventArgs e)
    {
        Close(false);
    }
}
