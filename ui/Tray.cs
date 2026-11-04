using System;
using System.Linq;
using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Controls.Primitives;
using System.Windows.Interop;
using Forms = System.Windows.Forms;
using Wpf.Ui.Controls;
using MenuItem = Wpf.Ui.Controls.MenuItem;

namespace NoMoreDPI;

/// <summary>The notification-area icon. The right-click menu is a WPF ContextMenu so it
/// gets the Fluent style instead of a Win32 menu.</summary>
public sealed class Tray : IDisposable
{
    readonly Forms.NotifyIcon icon = new();
    readonly ContextMenu menu = new();
    readonly MenuItem statusItem = new() { IsEnabled = false };
    readonly MenuItem toggleItem = new() { Header = "Pause", Icon = new SymbolIcon(SymbolRegular.Pause24) };
    bool active;
    // The toggle starts the service instead of pausing/resuming while the service is stopped.
    bool startsService;

    public Tray(Action show, Action quit)
    {
        var open = new MenuItem { Header = "Open", Icon = new SymbolIcon(SymbolRegular.Open24) };
        open.Click += (_, _) => show();
        toggleItem.Click += async (_, _) =>
        {
            toggleItem.IsEnabled = false;
            try
            {
                if (startsService) await AppState.Current.ServiceActionAsync(Service.StartAsync);
                else await AppState.Current.SetActiveAsync(!active);
            }
            catch (Exception ex) { Dialogs.Error(ex.Message); }
            Update();
        };
        var exit = new MenuItem { Header = "Quit", Icon = new SymbolIcon(SymbolRegular.Dismiss24), ToolTip = "Closes the tray icon; the service keeps running" };
        exit.Click += (_, _) => quit();
        menu.Items.Add(statusItem);
        menu.Items.Add(new Separator());
        menu.Items.Add(open);
        menu.Items.Add(toggleItem);
        menu.Items.Add(new Separator());
        menu.Items.Add(exit);
        // Without being the foreground window the menu would not close on an outside click.
        menu.Opened += (_, _) =>
        {
            if (PresentationSource.FromVisual(menu) is HwndSource src) SetForegroundWindow(src.Handle);
        };

        icon.Text = "No More DPI";
        icon.Icon = Icons.Tray(Icons.Paused);
        icon.MouseUp += (_, e) =>
        {
            if (e.Button == Forms.MouseButtons.Left) show();
            else if (e.Button == Forms.MouseButtons.Right) OpenMenu();
        };
        icon.Visible = true;

        AppState.Current.StatusChanged += Update;
        Update();
    }

    void OpenMenu()
    {
        menu.Placement = PlacementMode.MousePoint;
        menu.IsOpen = true;
    }

    void Update()
    {
        var st = AppState.Current;
        var s = st.Status;
        string tip;
        if (s == null)
        {
            icon.Icon = Icons.Tray(Icons.Missing);
            var svc = st.ServiceState;
            statusItem.Header = svc switch
            {
                ServiceRunState.NotInstalled => "Service not installed",
                ServiceRunState.Stopped when st.ServiceStartup == StartupType.Disabled => "Service disabled",
                ServiceRunState.Stopped => "Service stopped",
                ServiceRunState.Starting => "Service starting…",
                _ => st.ServiceError == null ? "Connecting…" : "Service not responding",
            };
            tip = "No More DPI: " + statusItem.Header;
            startsService = svc == ServiceRunState.Stopped && st.ServiceStartup != StartupType.Disabled;
            toggleItem.Header = "Start service";
            toggleItem.Icon = new SymbolIcon(SymbolRegular.Play24);
            toggleItem.IsEnabled = startsService;
            Text(tip);
            return;
        }
        startsService = false;
        if (!string.IsNullOrEmpty(s.Error))
        {
            icon.Icon = Icons.Tray(Icons.Problem);
            statusItem.Header = "Error: " + Truncate(s.Error, 60);
            tip = "No More DPI: " + s.Error;
        }
        else if (s.Up)
        {
            icon.Icon = Icons.Tray(Icons.Active);
            int n = s.Profiles?.Count(p => p.Enabled && p.Running) ?? 0;
            statusItem.Header = n == 1 ? "Active — 1 profile running" : $"Active — {n} profiles running";
            tip = "No More DPI: active";
        }
        else
        {
            icon.Icon = Icons.Tray(Icons.Paused);
            statusItem.Header = "Paused";
            tip = "No More DPI: paused";
        }
        Text(tip);
        active = s.Active;
        toggleItem.IsEnabled = true;
        toggleItem.Header = active ? "Pause" : "Resume";
        toggleItem.Icon = new SymbolIcon(active ? SymbolRegular.Pause24 : SymbolRegular.Play24);
    }

    // NotifyIcon.Text throws above 127 characters.
    void Text(string s) => icon.Text = Truncate(s, 120);

    static string Truncate(string s, int n) => s.Length <= n ? s : s[..n] + "…";

    public void Notify(string title, string text) => icon.ShowBalloonTip(3000, title, text, Forms.ToolTipIcon.Info);

    bool disposed;

    public void Dispose()
    {
        if (disposed) return;
        disposed = true;
        AppState.Current.StatusChanged -= Update;
        icon.Visible = false;
        icon.Dispose();
    }

    [DllImport("user32.dll")]
    static extern bool SetForegroundWindow(IntPtr hWnd);
}
