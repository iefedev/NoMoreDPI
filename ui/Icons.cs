using System;
using System.Collections.Generic;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using Drawing = System.Drawing;

namespace NoMoreDPI;

/// <summary>The tray icons, drawn at startup: a ring in the state colour with a solid core
/// (assets/icon.ico is the same shape in green).</summary>
public static class Icons
{
    public static readonly Color Active = Color.FromRgb(0x22, 0xa5, 0x5a);
    public static readonly Color Paused = Color.FromRgb(0x8a, 0x8f, 0x98);
    public static readonly Color Problem = Color.FromRgb(0xd9, 0x48, 0x3b);
    public static readonly Color Missing = Color.FromRgb(0xe0, 0xa1, 0x1b);

    static readonly Dictionary<Color, Drawing.Icon> trayCache = new();

    public static Color For(StateKind k) => k switch
    {
        StateKind.Ok => Active,
        StateKind.Bad => Problem,
        StateKind.Warn => Missing,
        _ => Paused,
    };

    static byte[] Pixels(Color c, int n)
    {
        var px = new byte[n * n * 4];
        double scale = n / 32.0, center = (n - 1) / 2.0;
        for (int y = 0; y < n; y++)
            for (int x = 0; x < n; x++)
            {
                double d = Math.Sqrt((x - center) * (x - center) + (y - center) * (y - center)) / scale;
                double a = d <= 6.5 ? 1
                    : d >= 10.5 && d <= 15 ? 1
                    : d > 15 && d < 16 ? 16 - d
                    : d > 6.5 && d < 7.5 ? 7.5 - d
                    : d > 9.5 && d < 10.5 ? d - 9.5
                    : 0;
                int i = (y * n + x) * 4;
                // Premultiplied BGRA.
                px[i] = (byte)(c.B * a);
                px[i + 1] = (byte)(c.G * a);
                px[i + 2] = (byte)(c.R * a);
                px[i + 3] = (byte)(255 * a);
            }
        return px;
    }

    public static BitmapSource Image(Color c, int n = 64)
    {
        var bmp = BitmapSource.Create(n, n, 96, 96, PixelFormats.Pbgra32, null, Pixels(c, n), n * 4);
        bmp.Freeze();
        return bmp;
    }

    public static Drawing.Icon Tray(Color c)
    {
        if (trayCache.TryGetValue(c, out var icon)) return icon;
        int n = System.Windows.Forms.SystemInformation.SmallIconSize.Width;
        var src = Pixels(c, n);
        using var bmp = new Drawing.Bitmap(n, n, Drawing.Imaging.PixelFormat.Format32bppPArgb);
        var data = bmp.LockBits(new Drawing.Rectangle(0, 0, n, n), Drawing.Imaging.ImageLockMode.WriteOnly, bmp.PixelFormat);
        System.Runtime.InteropServices.Marshal.Copy(src, 0, data.Scan0, src.Length);
        bmp.UnlockBits(data);
        icon = Drawing.Icon.FromHandle(bmp.GetHicon());
        trayCache[c] = icon;
        return icon;
    }
}
