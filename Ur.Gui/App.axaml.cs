using Avalonia;
using Avalonia.Controls.ApplicationLifetimes;
using Avalonia.Markup.Xaml;
using dotenv.net;
using Ur;
using Ur.Gui.ViewModels;
using Ur.Gui.Views;

namespace Ur.Gui;

public class App : Application
{
    public override void Initialize()
    {
        AvaloniaXamlLoader.Load(this);
    }

    public override async void OnFrameworkInitializationCompleted()
    {
        if (ApplicationLifetime is IClassicDesktopStyleApplicationLifetime desktop)
        {
            var window = new MainWindow();
            desktop.MainWindow = window;
            base.OnFrameworkInitializationCompleted();

            DotEnv.Load(options: new DotEnvOptions(probeForEnv: true, probeLevelsToSearch: 8));

            var vm = new MainWindowViewModel();
            window.DataContext = vm;

            var host = await UrHost.StartAsync(Environment.CurrentDirectory);

            if (host.Configuration.AvailableModels.Count == 0)
                await host.Configuration.RefreshModelsAsync();

            // First-run: API key
            if (host.Configuration.Readiness.BlockingIssues.Contains(UrChatBlockingIssue.MissingApiKey))
            {
                var accepted = await ShowApiKeyDialogAsync(window, host);
                if (!accepted)
                {
                    desktop.Shutdown();
                    return;
                }
            }

            // First-run: model selection
            if (host.Configuration.Readiness.BlockingIssues.Contains(UrChatBlockingIssue.MissingModelSelection))
            {
                var accepted = await ShowModelPickerDialogAsync(window, host);
                if (!accepted)
                {
                    desktop.Shutdown();
                    return;
                }
            }

            vm.Initialize(host);
            window.InitializeMenu();
        }
        else
        {
            base.OnFrameworkInitializationCompleted();
        }
    }

    private static async Task<bool> ShowApiKeyDialogAsync(MainWindow owner, UrHost host)
    {
        var dialogVm = new ApiKeyDialogViewModel();
        var dialog = new ApiKeyDialog { DataContext = dialogVm };
        var result = await dialog.ShowDialog<bool>(owner);
        if (!result)
            return false;

        await host.Configuration.SetApiKeyAsync(dialogVm.ApiKey);
        return true;
    }

    private static async Task<bool> ShowModelPickerDialogAsync(MainWindow owner, UrHost host)
    {
        var dialogVm = new ModelPickerDialogViewModel(host.Configuration.AvailableModels);
        var dialog = new ModelPickerDialog { DataContext = dialogVm };
        var result = await dialog.ShowDialog<bool>(owner);
        if (!result || dialogVm.SelectedModel is null)
            return false;

        await host.Configuration.SetSelectedModelAsync(dialogVm.SelectedModel.Id);
        return true;
    }
}
