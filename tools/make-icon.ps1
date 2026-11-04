# Writes assets\icon.ico and icon.png: the tray icon's ring (ui\Icons.cs) in the "active" green at the
# sizes Explorer and setup use. Run again only if the shape or colour changes.
Add-Type -AssemblyName System.Drawing
$out = Join-Path $PSScriptRoot '..\assets\icon.ico'
New-Item -ItemType Directory -Force (Split-Path $out) | Out-Null
$sizes = 16, 20, 24, 32, 40, 48, 64, 256
$pngs = foreach ($n in $sizes) {
    $bmp = New-Object System.Drawing.Bitmap $n, $n, ([System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $scale = $n / 32.0; $c = ($n - 1) / 2.0
    for ($y = 0; $y -lt $n; $y++) {
        for ($x = 0; $x -lt $n; $x++) {
            $d = [Math]::Sqrt(($x - $c) * ($x - $c) + ($y - $c) * ($y - $c)) / $scale
            $a = if ($d -le 6.5) { 1 } elseif ($d -ge 10.5 -and $d -le 15) { 1 } elseif ($d -gt 15 -and $d -lt 16) { 16 - $d } `
                 elseif ($d -gt 6.5 -and $d -lt 7.5) { 7.5 - $d } elseif ($d -gt 9.5 -and $d -lt 10.5) { $d - 9.5 } else { 0 }
            $bmp.SetPixel($x, $y, [System.Drawing.Color]::FromArgb([int](255 * $a), 0x22, 0xa5, 0x5a))
        }
    }
    $ms = New-Object System.IO.MemoryStream
    $bmp.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
    $bmp.Dispose()
    , $ms.ToArray()
}
$fs = [System.IO.File]::Create($out)
$w = New-Object System.IO.BinaryWriter $fs
$w.Write([UInt16]0); $w.Write([UInt16]1); $w.Write([UInt16]$sizes.Count)
$offset = 6 + 16 * $sizes.Count
for ($i = 0; $i -lt $sizes.Count; $i++) {
    $n = $sizes[$i]; $dim = if ($n -ge 256) { 0 } else { $n }
    $w.Write([byte]$dim); $w.Write([byte]$dim); $w.Write([byte]0); $w.Write([byte]0)
    $w.Write([UInt16]1); $w.Write([UInt16]32)
    $w.Write([UInt32]$pngs[$i].Length); $w.Write([UInt32]$offset)
    $offset += $pngs[$i].Length
}
foreach ($p in $pngs) { $w.Write($p) }
$w.Close()
# The 256 px frame also as a PNG, for the README.
$png = [IO.Path]::ChangeExtension($out, '.png')
[IO.File]::WriteAllBytes($png, $pngs[-1])
Get-Item $out, $png | Select-Object Name, Length
