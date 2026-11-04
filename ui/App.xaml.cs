using System;
using System.IO;
using System.Linq;
using System.Threading;
using System.Windows;

namespace NoMoreDPI;

public partial class App : Application
{
    // One tray per session; a second launch asks the running one to show its window.
    string MutexName = @"Local\NoMoreDPITray";
    string ShowEventName = @"Local\NoMoreDPITrayShow";

    Mutex? mutex;
    Tray? tray;
    MainWindow? window;
    bool exiting;

    protected override void OnStartup(StartupEventArgs e)
    {
        base.OnStartup(e);
        // Setup runs `--autostart on|off` as the signed-in user: set the Run entry and exit.
        int i = Array.FindIndex(e.Args, a => a.Equals("--autostart", StringComparison.OrdinalIgnoreCase));
        if (i >= 0)
        {
            try { Autostart.Enabled = i + 1 < e.Args.Length && e.Args[i + 1].Equals("on", StringComparison.OrdinalIgnoreCase); }
            catch { }
            Shutdown();
            return;
        }
        // A UI bug must not take the tray icon down with it.
        DispatcherUnhandledException += (_, ex) =>
        {
            ex.Handled = true;
            try
            {
                var dir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "No More DPI");
                Directory.CreateDirectory(dir);
                File.AppendAllText(Path.Combine(dir, "ui-error.log"), $"{DateTime.Now:s} {ex.Exception}{Environment.NewLine}");
            }
            catch { }
            Dialogs.Error("Something went wrong in the window: " + ex.Exception.Message);
        };
        // Logon (Run key) starts with --minimized: tray only. Any other launch opens the window.
        bool minimized = e.Args.Any(a => a.Equals("--minimized", StringComparison.OrdinalIgnoreCase));
#if DEBUG
        // Made-up data instead of the service (see Demo.cs), next to a real tray if one runs.
        int demo = Array.FindIndex(e.Args, a => a.Equals("--demo", StringComparison.OrdinalIgnoreCase));
        if (demo >= 0)
        {
            Demo.Enabled = true;
            Demo.SetStartPage(demo + 1 < e.Args.Length ? e.Args[demo + 1] : null);
            // Screenshots use one fixed format for numbers and dates.
            System.Globalization.CultureInfo.CurrentCulture = System.Globalization.CultureInfo.GetCultureInfo("en-US");
            System.Globalization.CultureInfo.DefaultThreadCurrentCulture = System.Globalization.CultureInfo.GetCultureInfo("en-US");
            MutexName += "Demo";
            ShowEventName += "Demo";
        }
#endif

        if (!TakeInstance())
        {
            if (!minimized) SignalRunning();
            Shutdown();
            return;
        }
        var showEvent = new EventWaitHandle(false, EventResetMode.AutoReset, ShowEventName);
        new Thread(() =>
        {
            while (showEvent.WaitOne())
                Dispatcher.BeginInvoke(ShowWindow);
        }) { IsBackground = true, Name = "show-event" }.Start();

        Settings.ApplyTheme(Settings.Get("theme", "system"));
        tray = new Tray(ShowWindow, Quit);
        AppState.Current.Start();
        if (!minimized) ShowWindow();
    }

    bool TakeInstance()
    {
        try
        {
            mutex = new Mutex(false, MutexName, out bool created);
            return created;
        }
        catch (UnauthorizedAccessException)
        {
            // An elevated tray owns it.
            return false;
        }
    }

    void SignalRunning()
    {
        // The running tray may not have created its event yet.
        for (int i = 0; i < 30; i++)
        {
            try
            {
                if (EventWaitHandle.TryOpenExisting(ShowEventName, out var ev))
                {
                    ev.Set();
                    ev.Dispose();
                    return;
                }
            }
            catch (UnauthorizedAccessException) { return; }
            Thread.Sleep(100);
        }
    }

    public void ShowWindow()
    {
        if (window == null)
        {
            window = new MainWindow();
            window.Closing += (_, args) =>
            {
                if (exiting) return;
                if (Settings.CloseToTray)
                {
                    args.Cancel = true;
                    window.Hide();
                    if (!Settings.Get("trayHintShown", false))
                    {
                        tray?.Notify("No More DPI is still running", "The service keeps working in the background. Open or quit it from the tray icon.");
                        Settings.Set("trayHintShown", true);
                    }
                }
                else Quit();
            };
            MainWindow = window;
        }
        window.Reveal();
    }

    public void Quit()
    {
        exiting = true;
        window?.Close();
        tray?.Dispose();
        Shutdown();
    }

    protected override void OnExit(ExitEventArgs e)
    {
        tray?.Dispose();
        mutex?.Dispose();
        base.OnExit(e);
    }
}
