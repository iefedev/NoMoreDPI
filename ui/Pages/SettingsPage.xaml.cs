using System;
using System.Diagnostics;
using System.Linq;
using System.Reflection;
using System.Threading.Tasks;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;

namespace NoMoreDPI.Pages;

public partial class SettingsPage : Page
{
    /// <summary>Set at build time (see NoMoreDPI.csproj); empty in builds without it.</summary>
    static readonly string ProjectUrl =
        Assembly.GetExecutingAssembly().GetCustomAttributes<AssemblyMetadataAttribute>()
            .FirstOrDefault(a => a.Key == "ProjectUrl")?.Value ?? "";

    readonly AppState state = AppState.Current;
    bool loading = true;
    bool busy;

    public SettingsPage()
    {
        InitializeComponent();
        AutostartSwitch.IsChecked = Autostart.Enabled;
        TraySwitch.IsChecked = Settings.CloseToTray;
        var theme = Settings.Get("theme", "system");
        ThemeBox.SelectedItem = ThemeBox.Items.Cast<ComboBoxItem>().FirstOrDefault(i => (string)i.Tag == theme) ?? ThemeBox.Items[0];
        LogDirText.Text = Service.LogDir;
        ProjectButton.Visibility = ProjectUrl.StartsWith("https://", StringComparison.OrdinalIgnoreCase) ? Visibility.Visible : Visibility.Collapsed;
        Loaded += (_, _) => { state.StatusChanged += Render; Render(); };
        Unloaded += (_, _) => state.StatusChanged -= Render;
        loading = false;
    }

    static string AppVersion =>
        Assembly.GetExecutingAssembly().GetCustomAttribute<AssemblyInformationalVersionAttribute>()?.InformationalVersion.Split('+')[0] ?? "";

    void Render()
    {
        loading = true;
        var svc = state.ServiceState;
        var startup = state.ServiceStartup;
        ServiceStateText.Text = svc switch
        {
            ServiceRunState.Running => "Service running",
            ServiceRunState.Stopped => "Service stopped",
            ServiceRunState.Starting => "Service starting…",
            ServiceRunState.Stopping => "Service stopping…",
            ServiceRunState.NotInstalled => "Service not installed",
            _ => "Service state unknown",
        };
        ServiceDot.Fill = new SolidColorBrush(svc switch
        {
            ServiceRunState.Running => Icons.Active,
            ServiceRunState.NotInstalled => Icons.Problem,
            _ => Icons.Missing,
        });
        bool installed = svc != ServiceRunState.NotInstalled;
        StartButton.IsEnabled = !busy && svc == ServiceRunState.Stopped && startup != StartupType.Disabled;
        StopButton.IsEnabled = !busy && svc == ServiceRunState.Running;
        RestartButton.IsEnabled = !busy && svc == ServiceRunState.Running;
        StartupBox.IsEnabled = !busy && installed;
        StartupBox.SelectedItem = startup == null ? null
            : StartupBox.Items.Cast<ComboBoxItem>().FirstOrDefault(i => (string)i.Tag == startup.ToString());

        var about = $"Bypass DPI for the domains you choose, system-wide. App version {AppVersion}";
        if (state.Status?.Version is { Length: > 0 } v) about += $", service version {v}";
        AboutText.Text = about + ".";
        loading = false;
    }

    async Task Run(Func<Task> action)
    {
        busy = true;
        Render();
        try { await state.ServiceActionAsync(action); }
        catch (OperationCanceledException) { }
        catch (Exception ex) { Dialogs.Error(ex.Message); }
        finally
        {
            busy = false;
            Render();
        }
    }

    async void Start_Click(object sender, RoutedEventArgs e) => await Run(Service.StartAsync);

    async void Stop_Click(object sender, RoutedEventArgs e)
    {
        if (await Dialogs.ConfirmAsync("Stop the service", "DPI bypass stops and your DNS settings go back to normal until the service is started again. If its startup type is Automatic, it also starts again with Windows.", "Stop"))
            await Run(Service.StopAsync);
    }

    async void Restart_Click(object sender, RoutedEventArgs e) => await Run(Service.RestartAsync);

    async void Startup_Changed(object sender, SelectionChangedEventArgs e)
    {
        if (loading || StartupBox.SelectedItem is not ComboBoxItem { Tag: string tag } || !Enum.TryParse<StartupType>(tag, out var type)) return;
        if (type == state.ServiceStartup) return;
        await Run(() => Service.SetStartupAsync(type));
    }

    void Autostart_Changed(object sender, RoutedEventArgs e)
    {
        if (loading) return;
        try { Autostart.Enabled = AutostartSwitch.IsChecked == true; }
        catch (Exception ex) { Dialogs.Error(ex.Message); }
    }

    void Tray_Changed(object sender, RoutedEventArgs e)
    {
        if (!loading) Settings.CloseToTray = TraySwitch.IsChecked == true;
    }

    void Theme_Changed(object sender, SelectionChangedEventArgs e)
    {
        if (loading || ThemeBox.SelectedItem is not ComboBoxItem { Tag: string theme }) return;
        Settings.Set("theme", theme);
        Settings.ApplyTheme(theme);
    }

    void OpenLogs_Click(object sender, RoutedEventArgs e) => Open("explorer.exe", $"\"{Service.LogDir}\"");

    void Project_Click(object sender, RoutedEventArgs e) => Open(ProjectUrl);

    static void Open(string file, string? args = null)
    {
        try { Process.Start(new ProcessStartInfo(file, args ?? "") { UseShellExecute = true }); }
        catch (Exception ex) { Dialogs.Error(ex.Message); }
    }

    async void Uninstall_Click(object sender, RoutedEventArgs e)
    {
        if (!await Dialogs.ConfirmAsync("Uninstall No More DPI", "The service, the tunnel adapter and the app are removed, and your DNS and browser settings are put back. Continue?", "Uninstall"))
            return;
        if (Service.Uninstall()) (Application.Current as App)?.Quit();
    }
}
