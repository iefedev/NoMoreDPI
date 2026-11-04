using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.ComponentModel;
using System.Linq;
using System.Runtime.CompilerServices;
using System.Text.Json.Nodes;
using System.Text.Json.Serialization;
using System.Text.RegularExpressions;

namespace NoMoreDPI;

// Read-only shapes of engine.Status and engine.FindResult.

public class ProfileStatus
{
    [JsonPropertyName("id")] public string Id { get; set; } = "";
    [JsonPropertyName("name")] public string Name { get; set; } = "";
    [JsonPropertyName("enabled")] public bool Enabled { get; set; }
    [JsonPropertyName("running")] public bool Running { get; set; }
    [JsonPropertyName("restarts")] public int Restarts { get; set; }
    [JsonPropertyName("lastErr")] public string? LastErr { get; set; }
    [JsonPropertyName("activeConns")] public long ActiveConns { get; set; }
    [JsonPropertyName("totalConns")] public ulong TotalConns { get; set; }
    [JsonPropertyName("failedConns")] public ulong FailedConns { get; set; }
    [JsonPropertyName("bytes")] public ulong Bytes { get; set; }
}

public class Status
{
    [JsonPropertyName("version")] public string Version { get; set; } = "";
    [JsonPropertyName("active")] public bool Active { get; set; }
    [JsonPropertyName("up")] public bool Up { get; set; }
    [JsonPropertyName("since")] public string? Since { get; set; }
    [JsonPropertyName("error")] public string? Error { get; set; }
    [JsonPropertyName("locked")] public bool Locked { get; set; }
    [JsonPropertyName("dns")] public Dictionary<string, ulong>? Dns { get; set; }
    [JsonPropertyName("profiles")] public List<ProfileStatus>? Profiles { get; set; }

    public ulong DnsCount(string key) => Dns != null && Dns.TryGetValue(key, out var v) ? v : 0;
}

public class Attempt
{
    [JsonPropertyName("args")] public string Args { get; set; } = "";
    [JsonPropertyName("ok")] public bool Ok { get; set; }
    [JsonPropertyName("ms")] public long Ms { get; set; }
    [JsonPropertyName("err")] public string? Err { get; set; }
    public string Mark => Ok ? "✔" : "✘";
}

public class FindResult
{
    [JsonPropertyName("profile")] public string Profile { get; set; } = "";
    [JsonPropertyName("found")] public string? Found { get; set; }
    [JsonPropertyName("applied")] public bool Applied { get; set; }
    [JsonPropertyName("attempts")] public List<Attempt>? Attempts { get; set; }
}

public abstract class Observable : INotifyPropertyChanged
{
    public event PropertyChangedEventHandler? PropertyChanged;

    protected void Raise([CallerMemberName] string? name = null) =>
        PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(name));

    protected bool Set<T>(ref T field, T value, [CallerMemberName] string? name = null)
    {
        if (EqualityComparer<T>.Default.Equals(field, value)) return false;
        field = value;
        Raise(name);
        return true;
    }
}

/// <summary>An editable profile. Edits go into the original JSON object so fields this
/// app does not know about (probe, …) survive a save.</summary>
public class ProfileModel : Observable
{
    readonly JsonObject raw;
    readonly Action changed;

    public ProfileModel(JsonObject raw, Action changed)
    {
        this.raw = raw;
        this.changed = changed;
        name = raw["name"]?.GetValue<string>() ?? "";
        enabled = raw["enabled"]?.GetValue<bool>() ?? false;
        domainsText = string.Join(Environment.NewLine, (raw["domains"] as JsonArray ?? new JsonArray()).Select(d => d?.GetValue<string>() ?? ""));
        args = raw["args"]?.GetValue<string>() ?? "";
    }

    public string Id => raw["id"]?.GetValue<string>() ?? "";
    public int Port => raw["port"]?.GetValue<int>() ?? 0;
    public string PortLabel => Port > 0 ? $"byedpi arguments — port {Port}" : "byedpi arguments — port is assigned on save";

    string name;
    public string Name { get => name; set { if (Set(ref name, value)) changed(); } }

    bool enabled;
    public bool Enabled { get => enabled; set { if (Set(ref enabled, value)) changed(); } }

    string domainsText;
    public string DomainsText { get => domainsText; set { if (Set(ref domainsText, value)) changed(); } }

    string args;
    public string Args { get => args; set { if (Set(ref args, value)) changed(); } }

    // Live state from status / auto find; not part of the config.
    string statsText = "";
    public string StatsText { get => statsText; set => Set(ref statsText, value); }

    StateKind state = StateKind.Off;
    public StateKind State { get => state; set => Set(ref state, value); }

    bool finding;
    public bool Finding { get => finding; set => Set(ref finding, value); }

    FindResult? find;
    string? findError;
    public FindResult? Find { get => find; set { Set(ref find, value); Raise(nameof(FindHeadline)); Raise(nameof(HasFind)); } }
    public string? FindError { get => findError; set { Set(ref findError, value); Raise(nameof(FindHeadline)); Raise(nameof(HasFind)); } }
    public bool HasFind => find != null || findError != null;
    public string FindHeadline => findError != null ? findError
        : find?.Found is { Length: > 0 } f ? "Found a working setting and applied it: " + f
        : find != null ? "No working setting was found." : "";

    public JsonObject ToJson()
    {
        raw["name"] = name;
        raw["enabled"] = enabled;
        raw["domains"] = new JsonArray(Regex.Split(domainsText, @"[\s,]+").Where(s => s.Length > 0).Select(s => (JsonNode)s).ToArray());
        raw["args"] = args;
        return raw;
    }
}

public enum StateKind { Off, Ok, Bad, Warn }

/// <summary>The editable copy of config.Config shared by the Profiles and Advanced pages.</summary>
public class ConfigModel : Observable
{
    readonly JsonObject raw;
    readonly Action changed;

    public ConfigModel(JsonObject raw, Action changed)
    {
        this.raw = raw;
        this.changed = changed;
        upstreamsText = string.Join(Environment.NewLine, (raw["upstreams"] as JsonArray ?? new JsonArray()).Select(u => u?.GetValue<string>() ?? ""));
        fakeRange = raw["fakeRange"]?.GetValue<string>() ?? "";
        lockDns = raw["lockDns"]?.GetValue<bool>() ?? false;
        foreach (var p in (raw["profiles"] as JsonArray ?? new JsonArray()).OfType<JsonObject>())
            Profiles.Add(new ProfileModel(p, changed));
    }

    public ObservableCollection<ProfileModel> Profiles { get; } = new();

    string upstreamsText;
    public string UpstreamsText { get => upstreamsText; set { if (Set(ref upstreamsText, value)) changed(); } }

    string fakeRange;
    public string FakeRange { get => fakeRange; set { if (Set(ref fakeRange, value)) changed(); } }

    bool lockDns;
    public bool LockDns { get => lockDns; set { if (Set(ref lockDns, value)) changed(); } }

    public ProfileModel AddProfile()
    {
        var p = new JsonObject
        {
            ["id"] = "p" + DateTimeOffset.UtcNow.ToUnixTimeMilliseconds().ToString("x"),
            ["name"] = "New profile",
            ["enabled"] = true,
            ["domains"] = new JsonArray(),
            ["args"] = "--tlsrec 1+s",
            ["port"] = 0,
        };
        var m = new ProfileModel(p, changed);
        Profiles.Add(m);
        changed();
        return m;
    }

    public void Remove(ProfileModel p)
    {
        Profiles.Remove(p);
        changed();
    }

    public string[] Upstreams => Regex.Split(upstreamsText, @"\s+").Where(s => s.Length > 0).ToArray();

    /// <summary>Whether the machine-wide DNS settings (servers, lock, fake range) differ from
    /// what was loaded. The service accepts those only from an elevated caller.</summary>
    public bool SystemChanged =>
        lockDns != (raw["lockDns"]?.GetValue<bool>() ?? false)
        || fakeRange.Trim() != (raw["fakeRange"]?.GetValue<string>() ?? "")
        || !Upstreams.SequenceEqual((raw["upstreams"] as JsonArray ?? new JsonArray()).Select(u => u?.GetValue<string>() ?? ""));

    public JsonObject ToJson()
    {
        var copy = raw.DeepClone().AsObject();
        copy["upstreams"] = new JsonArray(Upstreams.Select(s => (JsonNode)s).ToArray());
        copy["fakeRange"] = fakeRange.Trim();
        copy["lockDns"] = lockDns;
        copy["profiles"] = new JsonArray(Profiles.Select(p => (JsonNode)p.ToJson().DeepClone()).ToArray());
        return copy;
    }
}
