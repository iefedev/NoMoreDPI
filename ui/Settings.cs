using System;
using System.IO;
using System.Text.Json.Nodes;
using Microsoft.Win32;
using Wpf.Ui.Appearance;

namespace NoMoreDPI;

/// <summary>Window-app preferences in %APPDATA%\No More DPI\settings.json (theme, close to
/// tray, …). The service's own config lives elsewhere and is reached over the pipe.</summary>
public static class Settings
{
    static readonly string Dir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "No More DPI");
    static readonly string File_ = Path.Combine(Dir, "settings.json");
    static JsonObject? data;

    static JsonObject Data
    {
        get
        {
            if (data != null) return data;
            try { data = JsonNode.Parse(File.ReadAllText(File_)) as JsonObject; } catch { }
            return data ??= new JsonObject();
        }
    }

    public static T Get<T>(string key, T fallback)
    {
        try { return Data[key] is JsonNode n ? n.GetValue<T>() : fallback; }
        catch { return fallback; }
    }

    public static void Set<T>(string key, T value)
    {
        Data[key] = JsonValue.Create(value);
        try
        {
            Directory.CreateDirectory(Dir);
            File.WriteAllText(File_, Data.ToJsonString(new() { WriteIndented = true }));
        }
        catch { }
    }

    public static bool CloseToTray
    {
        get => Get("closeToTray", true);
        set => Set("closeToTray", value);
    }

    public static void ApplyTheme(string theme)
    {
        switch (theme)
        {
            case "light": ApplicationThemeManager.Apply(ApplicationTheme.Light); break;
            case "dark": ApplicationThemeManager.Apply(ApplicationTheme.Dark); break;
            default: ApplicationThemeManager.ApplySystemTheme(); break;
        }
        ApplicationAccentColorManager.ApplySystemAccent();
    }
}

/// <summary>The per-user HKCU Run entry. Setup sets it through `NoMoreDPI.exe --autostart on`
/// run as the signed-in user (not the elevated installer); the Settings toggle edits the same
/// value.</summary>
public static class Autostart
{
    const string Key = @"Software\Microsoft\Windows\CurrentVersion\Run";
    const string Name = "No More DPI";

    public static bool Enabled
    {
        get
        {
            using var k = Registry.CurrentUser.OpenSubKey(Key);
            return k?.GetValue(Name) is string s && s.Length > 0;
        }
        set
        {
            using var k = Registry.CurrentUser.CreateSubKey(Key);
            if (value) k.SetValue(Name, $"\"{Environment.ProcessPath}\" --minimized");
            else k.DeleteValue(Name, false);
        }
    }
}
