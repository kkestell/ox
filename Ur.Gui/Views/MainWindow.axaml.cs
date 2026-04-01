using Avalonia.Controls;
using Avalonia.Threading;
using Ur.Gui.ViewModels;

namespace Ur.Gui.Views;

public partial class MainWindow : Window
{
    public MainWindow()
    {
        InitializeComponent();
        Opened += (_, _) => Dispatcher.UIThread.Post(Activate);
    }

    public void InitializeMenu()
    {
        BuildNativeMenu();
    }

    private MainWindowViewModel Vm => (MainWindowViewModel)DataContext!;

    private void BuildNativeMenu()
    {
        var changeModel = new NativeMenuItem("Change Model…");
        changeModel.Click += async (_, _) =>
        {
            var models = Vm.Host.Configuration.AvailableModels;
            var dialogVm = new ModelPickerDialogViewModel(models);
            var dialog = new ModelPickerDialog { DataContext = dialogVm };
            var result = await dialog.ShowDialog<bool>(this);
            if (result && dialogVm.SelectedModel is not null)
            {
                await Vm.Host.Configuration.SetSelectedModelAsync(dialogVm.SelectedModel.Id);
                Vm.RefreshSelectedModel();
            }
        };

        var sessionMenu = new NativeMenu();
        sessionMenu.Add(changeModel);

        var manageExtensions = new NativeMenuItem("Manage Extensions…");
        manageExtensions.Click += async (_, _) =>
        {
            var dialogVm = new ExtensionsDialogViewModel(Vm.Host.Extensions);
            var dialog = new ExtensionsDialog { DataContext = dialogVm };
            await dialog.ShowDialog(this);
        };

        var extensionsMenu = new NativeMenu();
        extensionsMenu.Add(manageExtensions);

        var menu = new NativeMenu();
        menu.Add(new NativeMenuItem("Session") { Menu = sessionMenu });
        menu.Add(new NativeMenuItem("Extensions") { Menu = extensionsMenu });

        NativeMenu.SetMenu(this, menu);
    }
}
