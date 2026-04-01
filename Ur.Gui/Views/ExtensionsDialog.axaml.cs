using Avalonia.Controls;
using Avalonia.Interactivity;
using Ur;
using Ur.Gui.ViewModels;

namespace Ur.Gui.Views;

public partial class ExtensionsDialog : Window
{
    public ExtensionsDialog()
    {
        InitializeComponent();
    }

    private ExtensionsDialogViewModel Vm => (ExtensionsDialogViewModel)DataContext!;

    private void OnToggleChecked(object? sender, RoutedEventArgs e)
    {
        if (sender is CheckBox { DataContext: UrExtensionInfo ext })
            Vm.ToggleExtensionCommand.Execute(ext);
    }

    private void OnClose(object? sender, RoutedEventArgs e) => Close();
}
