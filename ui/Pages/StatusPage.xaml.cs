using System;
using System.ComponentModel;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using Wpf.Ui.Controls;

namespace NoMoreDPI.Pages;

public partial class StatusPage : Page
{
    readonly AppState state = AppState.Current;

    public StatusPage()
    {
        InitializeComponent();
        DataContext = state;
        Tip.IsOpen = !Settings.Get("tipDismissed", false);
        // InfoBar has no Closed event; its close button only clears IsOpen.
        DependencyPropertyDescriptor.FromProperty(InfoBar.IsOpenProperty, typeof(InfoBar))
            .AddValueChanged(Tip, (_, _) => { if (!Tip.IsOpen) Settings.Set("tipDismissed", true); });
        Loaded += (_, _) => { state.StatusChanged += Render; Render(); };
        Unloaded += (_, _) => state.StatusChanged -= Render;
    }

    // The main button: pause/resume when the service answers, start the service when it is
    // stopped, nothing when it is missing.
    bool startsService;

    void Render()
    {
        var s = state.Status;
        if (s == null)
        {
            var svc = state.ServiceState;
            startsService = svc == ServiceRunState.Stopped && state.ServiceStartup != StartupType.Disabled;
            var (headline, message) = svc switch
            {
                ServiceRunState.NotInstalled => ("Service not installed", "The No More DPI service is missing. Reinstall No More DPI to fix it."),
                ServiceRunState.Stopped when state.ServiceStartup == StartupType.Disabled =>
                    ("Service disabled", "The service is disabled. Change its startup type in Settings → Service."),
                ServiceRunState.Stopped => ("Service stopped", "The service is stopped, so nothing is being bypassed. Start it to continue."),
                ServiceRunState.Starting => ("Service starting…", ""),
                _ => (state.ServiceError == null ? "Connecting…" : "Service not responding", state.ServiceError ?? ""),
            };
            SetState(Icons.Missing, headline, "The service is not reachable.");
            Problem.Title = headline;
            Problem.Message = message;
            Problem.Severity = svc == ServiceRunState.Stopped ? InfoBarSeverity.Warning : InfoBarSeverity.Error;
            Problem.IsOpen = state.ServiceError != null && svc != ServiceRunState.Starting;
            Toggle.Content = "Start service";
            Toggle.Icon = new SymbolIcon(SymbolRegular.Play24);
            Toggle.Appearance = ControlAppearance.Primary;
            Toggle.IsEnabled = startsService;
            DnsQueries.Text = DnsFaked.Text = DnsBlocked.Text = DnsLock.Text = "—";
            return;
        }
        startsService = false;
        Problem.IsOpen = !string.IsNullOrEmpty(s.Error);
        Problem.Severity = InfoBarSeverity.Error;
        Problem.Title = "Could not start";
        Problem.Message = s.Error ?? "";
        var version = string.IsNullOrEmpty(s.Version) ? "" : $"Version {s.Version} · ";
        if (!string.IsNullOrEmpty(s.Error)) SetState(Icons.Problem, "Error", version + "The service could not start bypassing");
        else if (s.Up) SetState(Icons.Active, "Active", version + Since(s.Since));
        else SetState(Icons.Paused, "Paused", version + "DPI bypass is off; all traffic goes out directly");
        Toggle.IsEnabled = true;
        Toggle.Content = s.Active ? "Pause" : "Resume";
        Toggle.Icon = new SymbolIcon(s.Active ? SymbolRegular.Pause24 : SymbolRegular.Play24);
        Toggle.Appearance = s.Active ? ControlAppearance.Secondary : ControlAppearance.Primary;
        DnsQueries.Text = s.Up ? s.DnsCount("queries").ToString("N0") : "—";
        DnsFaked.Text = s.Up ? s.DnsCount("faked").ToString("N0") : "—";
        DnsBlocked.Text = s.Up ? s.DnsCount("blockedDoh").ToString("N0") : "—";
        DnsLock.Text = s.Locked ? "On" : "Off";
    }

    void SetState(Color c, string headline, string detail)
    {
        Dot.Fill = new SolidColorBrush(c);
        Headline.Text = headline;
        Detail.Text = detail;
    }

    static string Since(string? since) =>
        DateTimeOffset.TryParse(since, out var t) ? "Running since " + t.ToLocalTime().ToString("g") : "DPI bypass is on for the selected domains";

    void Profile_Click(object sender, RoutedEventArgs e) =>
        (Application.Current.MainWindow as MainWindow)?.NavigateTo(typeof(ProfilesPage));

    async void Toggle_Click(object sender, RoutedEventArgs e)
    {
        Toggle.IsEnabled = false;
        try
        {
            if (startsService) await state.ServiceActionAsync(Service.StartAsync);
            else await state.SetActiveAsync(!(state.Status?.Active ?? false));
        }
        catch (Exception ex) { Dialogs.Error(ex.Message); }
        Render();
    }
}
