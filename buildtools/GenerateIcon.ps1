param(
    [string]$OutIco = (Join-Path (Split-Path -Parent $PSScriptRoot) "kernforge.ico"),
    [string]$PreviewPng = (Join-Path $PSScriptRoot "kernforge-icon-preview.png")
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$source = @"
using System;
using System.Collections.Generic;
using System.Drawing;
using System.Drawing.Drawing2D;
using System.Drawing.Imaging;
using System.IO;
using System.Runtime.InteropServices;

public static class KernforgeIconGenerator
{
    public static void Generate(string outIco, string previewPng)
    {
        int[] sizes = new[] { 16, 24, 32, 48, 64, 128, 256 };
        var frames = new List<byte[]>();
        foreach (int size in sizes)
        {
            using (var bmp = Render(size))
            {
                frames.Add(EncodeIconFrame(bmp));
            }
        }

        SaveIco(outIco, sizes, frames);

        Directory.CreateDirectory(Path.GetDirectoryName(previewPng) ?? ".");
        using (var preview = Render(512))
        {
            preview.Save(previewPng, ImageFormat.Png);
        }
    }

    private static Bitmap Render(int size)
    {
        var bmp = new Bitmap(size, size, PixelFormat.Format32bppArgb);
        using (var g = Graphics.FromImage(bmp))
        {
            g.SmoothingMode = SmoothingMode.AntiAlias;
            g.InterpolationMode = InterpolationMode.HighQualityBicubic;
            g.PixelOffsetMode = PixelOffsetMode.HighQuality;
            g.Clear(Color.Transparent);

            float pad = size * 0.06f;
            float radius = size * 0.23f;
            float outerSize = size - pad * 2f;

            using (var outerPath = RoundedRect(pad, pad, outerSize, outerSize, radius))
            using (var outerBrush = new LinearGradientBrush(new RectangleF(pad, pad, outerSize, outerSize), Color.FromArgb(255, 255, 175, 75), Color.FromArgb(255, 123, 58, 237), 45f))
            {
                g.FillPath(outerBrush, outerPath);
            }

            float innerPad = pad + size * 0.035f;
            float innerSize = size - innerPad * 2f;
            using (var innerPath = RoundedRect(innerPad, innerPad, innerSize, innerSize, radius * 0.82f))
            using (var innerBrush = new LinearGradientBrush(new RectangleF(innerPad, innerPad, innerSize, innerSize), Color.FromArgb(255, 14, 21, 33), Color.FromArgb(255, 31, 42, 68), 90f))
            {
                g.FillPath(innerBrush, innerPath);
            }

            RectangleF glowRect = new RectangleF(size * 0.14f, size * 0.50f, size * 0.55f, size * 0.36f);
            using (var glowPath = RoundedRect(glowRect.X, glowRect.Y, glowRect.Width, glowRect.Height, size * 0.15f))
            using (var glowBrush = new PathGradientBrush(glowPath))
            {
                glowBrush.CenterColor = Color.FromArgb(160, 255, 122, 32);
                glowBrush.SurroundColors = new[] { Color.FromArgb(0, 255, 122, 32) };
                g.FillPath(glowBrush, glowPath);
            }

            using (var ringPen = new Pen(Color.FromArgb(55, 255, 255, 255), Math.Max(2f, size * 0.018f)))
            {
                g.DrawArc(ringPen, new RectangleF(size * 0.18f, size * 0.18f, size * 0.64f, size * 0.64f), 215f, 235f);
            }

            float stroke = Math.Max(12f, size * 0.12f);
            PointF vTop = new PointF(size * 0.31f, size * 0.23f);
            PointF vBottom = new PointF(size * 0.31f, size * 0.79f);
            PointF mid = new PointF(size * 0.31f + stroke * 0.25f, size * 0.52f);
            PointF upper = new PointF(size * 0.72f, size * 0.28f);
            PointF lower = new PointF(size * 0.72f, size * 0.76f);

            using (var kBrush = new LinearGradientBrush(new RectangleF(size * 0.20f, size * 0.20f, size * 0.58f, size * 0.60f), Color.FromArgb(255, 255, 211, 118), Color.FromArgb(255, 255, 105, 38), 90f))
            using (var shadowPen = new Pen(Color.FromArgb(80, 0, 0, 0), stroke + Math.Max(2f, size * 0.025f)))
            using (var kPen = new Pen(kBrush, stroke))
            using (var highlightPen = new Pen(Color.FromArgb(90, 255, 245, 210), Math.Max(2f, size * 0.022f)))
            {
                shadowPen.StartCap = shadowPen.EndCap = LineCap.Round;
                shadowPen.LineJoin = LineJoin.Round;
                kPen.StartCap = kPen.EndCap = LineCap.Round;
                kPen.LineJoin = LineJoin.Round;
                highlightPen.StartCap = highlightPen.EndCap = LineCap.Round;

                PointF shadowOffset = new PointF(size * 0.014f, size * 0.018f);
                g.DrawLine(shadowPen, Offset(vTop, shadowOffset), Offset(vBottom, shadowOffset));
                g.DrawLine(shadowPen, Offset(mid, shadowOffset), Offset(upper, shadowOffset));
                g.DrawLine(shadowPen, Offset(mid, shadowOffset), Offset(lower, shadowOffset));

                g.DrawLine(kPen, vTop, vBottom);
                g.DrawLine(kPen, mid, upper);
                g.DrawLine(kPen, mid, lower);

                g.DrawLine(highlightPen,
                    new PointF(vTop.X - size * 0.012f, vTop.Y + size * 0.055f),
                    new PointF(vTop.X - size * 0.012f, vBottom.Y - size * 0.22f));
            }

            float emberX = size * 0.77f;
            float emberY = size * 0.25f;
            using (var emberGlowBrush = new SolidBrush(Color.FromArgb(110, 255, 120, 44)))
            using (var emberBrush = new SolidBrush(Color.FromArgb(255, 255, 216, 140)))
            using (var emberPen = new Pen(Color.FromArgb(210, 255, 153, 60), Math.Max(2f, size * 0.014f)))
            {
                g.FillEllipse(emberGlowBrush, emberX - size * 0.06f, emberY - size * 0.06f, size * 0.12f, size * 0.12f);
                g.FillEllipse(emberBrush, emberX - size * 0.022f, emberY - size * 0.022f, size * 0.044f, size * 0.044f);
                g.DrawLine(emberPen, emberX, emberY - size * 0.075f, emberX, emberY - size * 0.03f);
                g.DrawLine(emberPen, emberX - size * 0.048f, emberY, emberX - size * 0.012f, emberY);
                g.DrawLine(emberPen, emberX + size * 0.012f, emberY, emberX + size * 0.048f, emberY);
            }
        }

        return bmp;
    }

    private static byte[] EncodeIconFrame(Bitmap bmp)
    {
        int width = bmp.Width;
        int height = bmp.Height;
        int xorStride = width * 4;
        int maskStride = ((width + 31) / 32) * 4;
        int xorSize = xorStride * height;
        int maskSize = maskStride * height;

        var rect = new Rectangle(0, 0, width, height);
        BitmapData data = bmp.LockBits(rect, ImageLockMode.ReadOnly, PixelFormat.Format32bppArgb);
        try
        {
            byte[] pixels = new byte[Math.Abs(data.Stride) * height];
            Marshal.Copy(data.Scan0, pixels, 0, pixels.Length);

            using (var ms = new MemoryStream())
            using (var bw = new BinaryWriter(ms))
            {
                bw.Write(40);
                bw.Write(width);
                bw.Write(height * 2);
                bw.Write((short)1);
                bw.Write((short)32);
                bw.Write(0);
                bw.Write(xorSize + maskSize);
                bw.Write(0);
                bw.Write(0);
                bw.Write(0);
                bw.Write(0);

                for (int y = height - 1; y >= 0; y--)
                {
                    int src = y * data.Stride;
                    bw.Write(pixels, src, xorStride);
                }

                bw.Write(new byte[maskSize]);
                bw.Flush();
                return ms.ToArray();
            }
        }
        finally
        {
            bmp.UnlockBits(data);
        }
    }

    private static PointF Offset(PointF p, PointF delta)
    {
        return new PointF(p.X + delta.X, p.Y + delta.Y);
    }

    private static GraphicsPath RoundedRect(float x, float y, float width, float height, float radius)
    {
        var path = new GraphicsPath();
        float d = Math.Max(1f, radius * 2f);
        path.AddArc(x, y, d, d, 180f, 90f);
        path.AddArc(x + width - d, y, d, d, 270f, 90f);
        path.AddArc(x + width - d, y + height - d, d, d, 0f, 90f);
        path.AddArc(x, y + height - d, d, d, 90f, 90f);
        path.CloseFigure();
        return path;
    }

    private static void SaveIco(string path, int[] sizes, List<byte[]> frames)
    {
        Directory.CreateDirectory(Path.GetDirectoryName(path) ?? ".");
        using (var fs = new FileStream(path, FileMode.Create, FileAccess.Write))
        using (var bw = new BinaryWriter(fs))
        {
            bw.Write((ushort)0);
            bw.Write((ushort)1);
            bw.Write((ushort)frames.Count);

            int offset = 6 + (16 * frames.Count);
            for (int i = 0; i < frames.Count; i++)
            {
                int size = sizes[i];
                byte[] frame = frames[i];
                bw.Write((byte)(size >= 256 ? 0 : size));
                bw.Write((byte)(size >= 256 ? 0 : size));
                bw.Write((byte)0);
                bw.Write((byte)0);
                bw.Write((ushort)1);
                bw.Write((ushort)32);
                bw.Write(frame.Length);
                bw.Write(offset);
                offset += frame.Length;
            }

            foreach (byte[] frame in frames)
            {
                bw.Write(frame);
            }
        }
    }
}
"@

Add-Type -ReferencedAssemblies System.Drawing -TypeDefinition $source
[KernforgeIconGenerator]::Generate($OutIco, $PreviewPng)
Write-Host "Generated icon: $OutIco"
Write-Host "Generated preview: $PreviewPng"
