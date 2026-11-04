using System;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.ServiceProcess;
using System.Threading.Tasks;

namespace NoMoreDPI;

public enum ServiceRunState { NotInstalled, Stopped, Starting, Running, Stopping, Unknown }

public enum StartupType { Automatic, Manual, Disabled }

/// <summary>The Windows service seen through the service manager (not the pipe). Setup grants
/// interactive users start/stop/query on it, so these calls need no UAC; changing the startup
/// type and uninstalling go through nomoredpi-svc.exe or the uninstaller elevated.</summary>
public static class Service
{
    public const string Name = "NoMoreDPI";
    public const string SvcExe = "nomoredpi-svc.exe";

    public static string InstallDir => Path.GetDirectoryName(Environment.ProcessPath) ?? AppContext.BaseDirectory;

    public static string LogDir => Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData), "No More DPI");

    public static (ServiceRunState State, StartupType? Startup) Query()
    {
#if DEBUG
        if (Demo.Enabled) return Demo.Query();
#endif
        try
        {
            using var sc = new ServiceController(Name);
            var state = sc.Status switch
            {
                ServiceControllerStatus.Running => ServiceRunState.Running,
                ServiceControllerStatus.Stopped => ServiceRunState.Stopped,
                ServiceControllerStatus.StartPending => ServiceRunState.Starting,
                ServiceControllerStatus.StopPending => ServiceRunState.Stopping,
                _ => ServiceRunState.Unknown,
            };
            StartupType? startup = sc.StartType switch
            {
                ServiceStartMode.Automatic or ServiceStartMode.Boot or ServiceStartMode.System => StartupType.Automatic,
                ServiceStartMode.Manual => StartupType.Manual,
                ServiceStartMode.Disabled => StartupType.Disabled,
                _ => null,
            };
            return (state, startup);
        }
        catch (InvalidOperationException)
        {
            return (ServiceRunState.NotInstalled, null);
        }
    }

    public static Task StartAsync() => Task.Run(() =>
    {
#if DEBUG
        if (Demo.Enabled) return;
#endif
        using var sc = new ServiceController(Name);
        if (sc.StartType == ServiceStartMode.Disabled)
            throw new InvalidOperationException("The service is disabled. Set its startup type to Automatic or Manual first.");
        if (sc.Status == ServiceControllerStatus.Running) return;
        Wrap(() => sc.Start());
        sc.WaitForStatus(ServiceControllerStatus.Running, TimeSpan.FromSeconds(20));
    });

    public static Task StopAsync() => Task.Run(() =>
    {
#if DEBUG
        if (Demo.Enabled) return;
#endif
        using var sc = new ServiceController(Name);
        if (sc.Status == ServiceControllerStatus.Stopped) return;
        Wrap(() => sc.Stop());
        sc.WaitForStatus(ServiceControllerStatus.Stopped, TimeSpan.FromSeconds(30));
    });

    public static async Task RestartAsync()
    {
        await StopAsync();
        await StartAsync();
    }

    /// <summary>Runs `nomoredpi-svc.exe set-startup …` through UAC.</summary>
    public static Task SetStartupAsync(StartupType type) =>
        RunElevatedAsync(Path.Combine(InstallDir, SvcExe), "set-startup", type.ToString().ToLowerInvariant());

    /// <summary>Runs `nomoredpi-svc.exe set-dns …` through UAC: the service takes the
    /// machine-wide DNS settings only from an elevated caller.</summary>
    public static Task SetDnsAsync(bool lockDns, string fakeRange, string[] upstreams)
    {
        var args = new System.Collections.Generic.List<string> { "set-dns", "--lock", lockDns ? "on" : "off", "--fake-range", fakeRange };
        foreach (var u in upstreams) { args.Add("--upstream"); args.Add(u); }
        return RunElevatedAsync(Path.Combine(InstallDir, SvcExe), args.ToArray());
    }

    /// <summary>Starts the setup uninstaller (or `nomoredpi-svc.exe uninstall` for a build that
    /// was installed without setup). Returns false when the user cancelled UAC.</summary>
    public static bool Uninstall()
    {
        var uninstaller = Path.Combine(InstallDir, "unins000.exe");
        try
        {
            if (File.Exists(uninstaller))
                Process.Start(new ProcessStartInfo(uninstaller) { UseShellExecute = true });
            else
                Process.Start(new ProcessStartInfo(Path.Combine(InstallDir, SvcExe), "uninstall") { UseShellExecute = true, Verb = "runas" });
            return true;
        }
        catch (Win32Exception e) when (e.NativeErrorCode == 1223) // ERROR_CANCELLED: UAC declined
        {
            return false;
        }
    }

    /// <summary>Where nomoredpi-svc.exe started with --child leaves its error message (its
    /// %TEMP%, which the elevated process shares with us: same user).</summary>
    static string ChildErrorFile => Path.Combine(Path.GetTempPath(), "nomoredpi-elevated-error.txt");

    /// <summary>Runs nomoredpi-svc.exe elevated with --child, so it reports errors to us
    /// instead of showing its own message box.</summary>
    static async Task RunElevatedAsync(string exe, params string[] args)
    {
#if DEBUG
        if (Demo.Enabled) return;
#endif
        foreach (var a in args)
            if (a.Length == 0 || a.Contains('"') || a.Any(char.IsWhiteSpace))
                throw new ArgumentException($"Invalid value: \"{a}\"");
        var line = string.Join(" ", args.Select(a => "\"" + a + "\"")) + " --child";
        try { File.Delete(ChildErrorFile); } catch (IOException) { }
        Process p;
        try
        {
            p = Process.Start(new ProcessStartInfo(exe, line) { UseShellExecute = true, Verb = "runas", WindowStyle = ProcessWindowStyle.Hidden })
                ?? throw new InvalidOperationException("Could not start " + Path.GetFileName(exe));
        }
        catch (Win32Exception e) when (e.NativeErrorCode == 1223)
        {
            throw new OperationCanceledException("Administrator permission was not granted.");
        }
        using (p)
        {
            await p.WaitForExitAsync();
            if (p.ExitCode == 0) return;
            string? message = null;
            try { message = File.ReadAllText(ChildErrorFile).Trim(); } catch (IOException) { }
            throw new InvalidOperationException(string.IsNullOrEmpty(message) ? $"{Path.GetFileName(exe)} {args[0]} failed (exit code {p.ExitCode})." : message);
        }
    }

    static void Wrap(Action a)
    {
        try { a(); }
        catch (InvalidOperationException e) when (e.InnerException is Win32Exception { NativeErrorCode: 5 })
        {
            throw new InvalidOperationException("Access denied. Reinstall No More DPI to grant service control to users.");
        }
    }
}
