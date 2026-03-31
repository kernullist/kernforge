param(
    [string]$SourcePng = (Join-Path $PSScriptRoot "kernforge-icon-preview.png"),
    [string]$OutDir = (Join-Path (Split-Path -Parent $PSScriptRoot) "branding")
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

Add-Type -AssemblyName System.Drawing

if (-not (Test-Path $SourcePng)) {
    throw "Source preview not found: $SourcePng"
}

New-Item -ItemType Directory -Force $OutDir | Out-Null

$source = [System.Drawing.Image]::FromFile($SourcePng)

function New-BitmapGraphics {
    param([int]$Width, [int]$Height)
    $bmp = New-Object System.Drawing.Bitmap($Width, $Height, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
    $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
    $g.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
    return @{ Bitmap = $bmp; Graphics = $g }
}

function New-RoundedRectPath {
    param([float]$X, [float]$Y, [float]$Width, [float]$Height, [float]$Radius)
    $path = New-Object System.Drawing.Drawing2D.GraphicsPath
    $d = [Math]::Max(1.0, $Radius * 2.0)
    [void]$path.AddArc($X, $Y, $d, $d, 180, 90)
    [void]$path.AddArc($X + $Width - $d, $Y, $d, $d, 270, 90)
    [void]$path.AddArc($X + $Width - $d, $Y + $Height - $d, $d, $d, 0, 90)
    [void]$path.AddArc($X, $Y + $Height - $d, $d, $d, 90, 90)
    [void]$path.CloseFigure()
    return $path
}

function Save-AppIcon {
    param([System.Drawing.Image]$Image, [string]$Path)
    $Image.Save($Path, [System.Drawing.Imaging.ImageFormat]::Png)
}

function Save-InstallerTile {
    param([System.Drawing.Image]$Image, [string]$Path)

    $ctx = New-BitmapGraphics -Width 512 -Height 512
    $bmp = $ctx.Bitmap
    $g = $ctx.Graphics
    try {
        $g.Clear([System.Drawing.Color]::FromArgb(255, 9, 13, 22))

        $bgBrush = New-Object System.Drawing.Drawing2D.LinearGradientBrush(
            (New-Object System.Drawing.RectangleF(0, 0, 512, 512)),
            ([System.Drawing.Color]::FromArgb(255, 15, 20, 34)),
            ([System.Drawing.Color]::FromArgb(255, 34, 27, 58)),
            55.0
        )
        $g.FillRectangle($bgBrush, 0, 0, 512, 512)

        $panel = New-RoundedRectPath 40 32 432 448 64
        $panelBrush = New-Object System.Drawing.Drawing2D.LinearGradientBrush(
            (New-Object System.Drawing.RectangleF(40, 32, 432, 448)),
            ([System.Drawing.Color]::FromArgb(235, 22, 30, 48)),
            ([System.Drawing.Color]::FromArgb(235, 18, 24, 39)),
            90.0
        )
        $g.FillPath($panelBrush, $panel)

        $glowBrush = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::FromArgb(38, 255, 136, 64))
        $g.FillEllipse($glowBrush, 44, 248, 210, 160)
        $g.FillEllipse($glowBrush, 272, 52, 120, 120)

        $g.DrawImage($Image, 96, 54, 320, 320)

        $titleFont = New-Object System.Drawing.Font("Segoe UI Semibold", 28, [System.Drawing.FontStyle]::Bold, [System.Drawing.GraphicsUnit]::Pixel)
        $subFont = New-Object System.Drawing.Font("Segoe UI", 15, [System.Drawing.FontStyle]::Regular, [System.Drawing.GraphicsUnit]::Pixel)
        $titleBrush = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::FromArgb(255, 247, 247, 250))
        $subBrush = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::FromArgb(210, 194, 201, 220))
        $g.DrawString("Kernforge", $titleFont, $titleBrush, 96, 388)
        $g.DrawString("Windows coding forge", $subFont, $subBrush, 98, 428)

        $bmp.Save($Path, [System.Drawing.Imaging.ImageFormat]::Png)

        $bgBrush.Dispose(); $panel.Dispose(); $panelBrush.Dispose(); $glowBrush.Dispose(); $titleFont.Dispose(); $subFont.Dispose(); $titleBrush.Dispose(); $subBrush.Dispose()
    }
    finally {
        $g.Dispose()
        $bmp.Dispose()
    }
}

function Save-ReleaseBanner {
    param([System.Drawing.Image]$Image, [string]$Path)

    $ctx = New-BitmapGraphics -Width 1280 -Height 640
    $bmp = $ctx.Bitmap
    $g = $ctx.Graphics
    try {
        $bgBrush = New-Object System.Drawing.Drawing2D.LinearGradientBrush(
            (New-Object System.Drawing.RectangleF(0, 0, 1280, 640)),
            ([System.Drawing.Color]::FromArgb(255, 11, 16, 27)),
            ([System.Drawing.Color]::FromArgb(255, 29, 19, 54)),
            22.0
        )
        $g.FillRectangle($bgBrush, 0, 0, 1280, 640)

        $ringPen = New-Object System.Drawing.Pen([System.Drawing.Color]::FromArgb(28, 255, 255, 255), 16)
        $g.DrawArc($ringPen, 64, 98, 420, 420, 208, 230)
        $g.DrawArc($ringPen, 798, -140, 540, 540, 128, 116)

        $glowBrush = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::FromArgb(42, 255, 127, 58))
        $g.FillEllipse($glowBrush, 72, 340, 300, 180)
        $g.FillEllipse($glowBrush, 988, 68, 180, 180)

        $panel = New-RoundedRectPath 72 72 1136 496 48
        $panelBrush = New-Object System.Drawing.Drawing2D.LinearGradientBrush(
            (New-Object System.Drawing.RectangleF(72, 72, 1136, 496)),
            ([System.Drawing.Color]::FromArgb(190, 15, 22, 37)),
            ([System.Drawing.Color]::FromArgb(160, 20, 17, 35)),
            180.0
        )
        $g.FillPath($panelBrush, $panel)

        $g.DrawImage($Image, 118, 120, 244, 244)

        $titleFont = New-Object System.Drawing.Font("Segoe UI Semibold", 68, [System.Drawing.FontStyle]::Bold, [System.Drawing.GraphicsUnit]::Pixel)
        $tagFont = New-Object System.Drawing.Font("Segoe UI", 24, [System.Drawing.FontStyle]::Regular, [System.Drawing.GraphicsUnit]::Pixel)
        $bodyFont = New-Object System.Drawing.Font("Segoe UI Semibold", 22, [System.Drawing.FontStyle]::Bold, [System.Drawing.GraphicsUnit]::Pixel)
        $smallFont = New-Object System.Drawing.Font("Segoe UI", 18, [System.Drawing.FontStyle]::Regular, [System.Drawing.GraphicsUnit]::Pixel)

        $whiteBrush = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::FromArgb(255, 247, 248, 252))
        $softBrush = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::FromArgb(225, 205, 212, 228))
        $accentBrush = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::FromArgb(255, 255, 181, 100))

        $g.DrawString("Kernforge", $titleFont, $whiteBrush, 420, 128)
        $g.DrawString("Verification-first terminal coding forge", $tagFont, $softBrush, 424, 220)

        $dotBrush = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::FromArgb(255, 255, 128, 48))
        $items = @(
            @{ Title = "Persistent memory"; Body = "Cross-session recall with citations, importance tiers, and dashboards." },
            @{ Title = "Selection-first editing"; Body = "Review and patch flows built around focused code selections." },
            @{ Title = "MCP + skills"; Body = "Local workflows and remote tools in a Windows-native CLI experience." }
        )

        $y = 304
        foreach ($item in $items) {
            $g.FillEllipse($dotBrush, 432, $y + 8, 12, 12)
            $g.DrawString([string]$item.Title, $bodyFont, $whiteBrush, 460, $y)
            $g.DrawString([string]$item.Body, $smallFont, $softBrush, 460, $y + 30)
            $y += 92
        }

        $accentFont = New-Object System.Drawing.Font("Segoe UI Semibold", 18, [System.Drawing.FontStyle]::Bold, [System.Drawing.GraphicsUnit]::Pixel)
        $g.DrawString("Windows-native  |  Checkpoints  |  Adaptive verification", $accentFont, $accentBrush, 120, 540)

        $bmp.Save($Path, [System.Drawing.Imaging.ImageFormat]::Png)

        $bgBrush.Dispose(); $ringPen.Dispose(); $glowBrush.Dispose(); $panel.Dispose(); $panelBrush.Dispose(); $titleFont.Dispose(); $tagFont.Dispose(); $bodyFont.Dispose(); $smallFont.Dispose(); $whiteBrush.Dispose(); $softBrush.Dispose(); $accentBrush.Dispose(); $dotBrush.Dispose(); $accentFont.Dispose()
    }
    finally {
        $g.Dispose()
        $bmp.Dispose()
    }
}

$appIconPath = Join-Path $OutDir 'kernforge-app-icon-512.png'
$installerPath = Join-Path $OutDir 'kernforge-installer-tile-512.png'
$bannerPath = Join-Path $OutDir 'kernforge-release-banner-1280x640.png'

Save-AppIcon -Image $source -Path $appIconPath
Save-InstallerTile -Image $source -Path $installerPath
Save-ReleaseBanner -Image $source -Path $bannerPath

$source.Dispose()

Write-Host "Generated branding assets:"
Write-Host " - $appIconPath"
Write-Host " - $installerPath"
Write-Host " - $bannerPath"
