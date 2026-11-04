#if DEBUG
using System;
using System.Text.Json.Nodes;
using System.Threading.Tasks;

namespace NoMoreDPI;

/// <summary>Debug builds only: `NoMoreDPI.exe --demo [status|profiles|advanced|settings]`
/// answers every pipe and service-manager call with made-up data, so the window can be shown
/// (and screenshotted) without the service, opened on the given page. Release builds do not
/// contain this class or the switch.</summary>
public static class Demo
{
    public static bool Enabled { get; set; }

    /// <summary>The page the window opens on, from the argument after --demo.</summary>
    public static Type? StartPage { get; private set; }

    public static void SetStartPage(string? name) => StartPage = name?.ToLowerInvariant() switch
    {
        "profiles" => typeof(Pages.ProfilesPage),
        "advanced" => typeof(Pages.AdvancedPage),
        "settings" => typeof(Pages.SettingsPage),
        _ => null,
    };

    static bool active = true;
    static readonly DateTimeOffset since = DateTimeOffset.Now.AddHours(-2).AddMinutes(-17);

    static JsonObject config = (JsonObject)JsonNode.Parse("""
    {
      "version": 1, "active": true, "fakeRange": "100.79.0.0/16", "lockDns": true,
      "upstreams": [
        "sdns://AQMAAAAAAAAADDkuOS45Ljk6ODQ0MyBnyEe4yHWM0SAkVUO-dWdG3zTfHYTAC4xHA2jfgh2GPhkyLmRuc2NyeXB0LWNlcnQucXVhZDkubmV0",
        "sdns://AQMAAAAAAAAAEjE0OS4xMTIuMTEyLjk6ODQ0MyBnyEe4yHWM0SAkVUO-dWdG3zTfHYTAC4xHA2jfgh2GPhkyLmRuc2NyeXB0LWNlcnQucXVhZDkubmV0",
        "sdns://AQcAAAAAAAAAEjk0LjE0MC4xNC4xNDA6NTQ0MyC16ETWuDo-PhJo62gfvqcN48X6aNvWiBQdvy7AZrLa-iUyLmRuc2NyeXB0LnVuZmlsdGVyZWQubnMxLmFkZ3VhcmQuY29t",
        "https://1.1.1.1/dns-query"
      ],
      "profiles": [
        { "id": "discord", "name": "Discord", "enabled": true, "args": "--tlsrec 1+s", "port": 19091, "probe": "discord.com",
          "domains": ["discord.com", "discord.gg", "discord.media", "discordapp.com", "discordapp.net", "discordstatus.com",
                      "discord.co", "discord.dev", "discord.gift", "discord.new", "discordcdn.com", "dis.gd"] },
        { "id": "roblox", "name": "Roblox", "enabled": true, "args": "--split 1 --tlsrec 1+s --disorder 1", "port": 19092, "probe": "www.roblox.com",
          "domains": ["roblox.com", "robloxlabs.com", "rbxcdn.com", "rbxinfra.com", "rbxtrk.com", "rbx.com"] }
      ]
    }
    """)!;

    public static (ServiceRunState, StartupType?) Query() => (ServiceRunState.Running, StartupType.Automatic);

    public static async Task<JsonNode?> CallAsync(string method, JsonNode? param)
    {
        switch (method)
        {
            case "getConfig":
                return config.DeepClone();
            case "setConfig":
                if (param is JsonObject c) config = (JsonObject)c.DeepClone();
                return null;
            case "setActive":
                active = param?.GetValue<bool>() ?? active;
                return null;
            case "status":
                return Status();
            case "autoFind":
                await Task.Delay(2500);
                return JsonNode.Parse("""
                {
                  "profile": "roblox", "found": "--split 1 --tlsrec 1+s --disorder 1", "applied": true,
                  "attempts": [
                    { "args": "(direct, no byedpi)", "ok": false, "ms": 6004, "err": "TLS: connection reset" },
                    { "args": "--tlsrec 1+s", "ok": false, "ms": 6011, "err": "TLS: connection reset" },
                    { "args": "--split 1 --tlsrec 1+s --disorder 1", "ok": true, "ms": 212 }
                  ]
                }
                """);
            default:
                return null;
        }
    }

    static JsonNode Status()
    {
        var profiles = new JsonArray();
        var n = 0;
        foreach (var p in (JsonArray)config["profiles"]!)
        {
            var o = (JsonObject)p!.DeepClone();
            var on = active && o["enabled"]?.GetValue<bool>() == true;
            o["running"] = on;
            o["restarts"] = 0;
            o["activeConns"] = on ? (n == 0 ? 7 : 3) : 0;
            o["totalConns"] = on ? (n == 0 ? 1482 : 356) : 0;
            o["failedConns"] = on ? (n == 0 ? 2 : 0) : 0;
            o["bytes"] = on ? (n == 0 ? 186_413_002 : 41_207_331) : 0;
            profiles.Add(o);
            n++;
        }
        return new JsonObject
        {
            ["version"] = "1.0.0",
            ["active"] = active,
            ["up"] = active,
            ["since"] = active ? since.ToString("o") : null,
            ["locked"] = active,
            ["dns"] = new JsonObject { ["queries"] = 9214, ["faked"] = 1873, ["upstream"] = 7341, ["failures"] = 4, ["blockedDoh"] = 36 },
            ["profiles"] = profiles,
        };
    }
}
#endif
