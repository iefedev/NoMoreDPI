using System;
using System.Linq;
using System.Text.Json.Nodes;
using System.Threading.Tasks;
using System.Windows.Threading;

namespace NoMoreDPI;

/// <summary>Everything the tray and the window show: the last status, the editable config
/// copy and whether it has unsaved edits. Lives on the UI thread.</summary>
public class AppState : Observable
{
    public static AppState Current { get; } = new();

    readonly DispatcherTimer timer = new() { Interval = TimeSpan.FromSeconds(2) };
    bool polling;

    AppState() { timer.Tick += async (_, _) => await PollAsync(); }

    public void Start()
    {
        timer.Start();
        _ = LoadAsync().ContinueWith(_ => PollAsync(), TaskScheduler.FromCurrentSynchronizationContext());
    }

    Status? status;
    public Status? Status { get => status; private set => Set(ref status, value); }

    string? serviceError;
    /// <summary>Set when the pipe call itself failed (service not running).</summary>
    public string? ServiceError { get => serviceError; private set => Set(ref serviceError, value); }

    ServiceRunState serviceState = ServiceRunState.Unknown;
    /// <summary>What the service manager says, independent of the pipe.</summary>
    public ServiceRunState ServiceState { get => serviceState; private set => Set(ref serviceState, value); }

    StartupType? serviceStartup;
    public StartupType? ServiceStartup { get => serviceStartup; private set => Set(ref serviceStartup, value); }

    ConfigModel? config;
    public ConfigModel? Config { get => config; private set => Set(ref config, value); }

    string? configError;
    public string? ConfigError { get => configError; private set => Set(ref configError, value); }

    bool dirty;
    public bool Dirty { get => dirty; private set => Set(ref dirty, value); }

    bool anyFinding;
    public bool AnyFinding { get => anyFinding; private set => Set(ref anyFinding, value); }

    public event Action? StatusChanged;

    public async Task LoadAsync()
    {
        try
        {
            var raw = await Pipe.CallAsync("getConfig") as JsonObject ?? throw new InvalidOperationException("The service returned an empty configuration.");
            var keep = Config?.Profiles.ToDictionary(p => p.Id);
            var c = new ConfigModel(raw, () => Dirty = true);
            // Auto find results belong to the profile, not to one loaded copy of the config.
            if (keep != null)
                foreach (var p in c.Profiles)
                    if (keep.TryGetValue(p.Id, out var old)) { p.Find = old.Find; p.FindError = old.FindError; }
            Config = c;
            Dirty = false;
            ConfigError = null;
            ApplyStatus();
        }
        catch (Exception e)
        {
            ConfigError = e.Message;
        }
    }

    public async Task PollAsync()
    {
        if (polling) return;
        polling = true;
        try
        {
            var (state, startup) = await Task.Run(Service.Query);
            ServiceState = state;
            ServiceStartup = startup;
            Status = await Pipe.CallAsync<Status>("status", timeout: TimeSpan.FromSeconds(3));
            ServiceError = null;
            if (Config == null) await LoadAsync();
        }
        catch (Exception e)
        {
            Status = null;
            ServiceError = e.Message;
        }
        finally { polling = false; }
        ApplyStatus();
        StatusChanged?.Invoke();
    }

    void ApplyStatus()
    {
        if (Config == null) return;
        var s = Status;
        foreach (var p in Config.Profiles)
        {
            var ps = s?.Profiles?.FirstOrDefault(x => x.Id == p.Id);
            if (s == null || ps == null) { p.State = StateKind.Off; p.StatsText = s == null ? "" : "Not saved yet"; continue; }
            p.State = ps.Running ? StateKind.Ok : ps.Enabled && s.Up ? StateKind.Bad : StateKind.Off;
            var t = !ps.Enabled ? "Off" : !s.Up ? "Paused" : ps.Running ? "Running" : "Not running";
            if (s.Up && ps.Enabled) t += $" · {ps.ActiveConns} open · {ps.TotalConns} total · {ps.FailedConns} failed · {Format.Bytes(ps.Bytes)}";
            if (ps.Restarts > 0) t += $" · {ps.Restarts} restarts";
            if (!string.IsNullOrEmpty(ps.LastErr)) t += $" · last error: {ps.LastErr}";
            p.StatsText = t;
        }
    }

    public async Task SetActiveAsync(bool on)
    {
        await Pipe.CallAsync("setActive", on, TimeSpan.FromSeconds(60));
        await PollAsync();
    }

    /// <summary>Runs a service-manager action, then refreshes everything (the pipe comes and
    /// goes with the service).</summary>
    public async Task ServiceActionAsync(Func<Task> action)
    {
        try { await action(); }
        finally
        {
            await PollAsync();
            if (Status != null && Config == null) await LoadAsync();
        }
    }

    public async Task SaveAsync()
    {
        if (Config == null) return;
        // DNS servers, the DNS lock and the fake range affect every user of the PC, so the
        // service takes them only from an elevated caller: change them through UAC first,
        // after which the rest of the config matches and needs no elevation.
        if (Config.SystemChanged)
            await Service.SetDnsAsync(Config.LockDns, Config.FakeRange.Trim(), Config.Upstreams);
        await Pipe.CallAsync("setConfig", Config.ToJson(), TimeSpan.FromSeconds(60));
        await LoadAsync();
        await PollAsync();
    }

    public async Task AutoFindAsync(ProfileModel p)
    {
        if (Dirty) throw new InvalidOperationException("Save your changes first.");
        p.Finding = true;
        AnyFinding = true;
        try
        {
            p.Find = await Pipe.CallAsync<FindResult>("autoFind", p.Id, TimeSpan.FromMinutes(5));
            p.FindError = null;
        }
        catch (Exception e)
        {
            p.Find = null;
            p.FindError = e.Message;
        }
        finally
        {
            p.Finding = false;
            AnyFinding = Config?.Profiles.Any(x => x.Finding) == true;
        }
        if (!Dirty) await LoadAsync();
    }
}

public static class Format
{
    public static string Bytes(ulong n) =>
        n < 1024 ? $"{n} B" :
        n < 1048576 ? $"{n / 1024.0:0.0} KB" :
        n < 1073741824 ? $"{n / 1048576.0:0.0} MB" :
        $"{n / 1073741824.0:0.00} GB";
}
