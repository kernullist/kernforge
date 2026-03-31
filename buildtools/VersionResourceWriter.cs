using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.IO;
using System.Runtime.InteropServices;
using System.Text;

public static class VersionResourceWriter
{
    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr BeginUpdateResource(string pFileName, bool bDeleteExistingResources);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool UpdateResource(IntPtr hUpdate, IntPtr lpType, IntPtr lpName, ushort wLanguage, byte[] lpData, uint cbData);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool EndUpdateResource(IntPtr hUpdate, bool fDiscard);

    private const int RT_ICON = 3;
    private const int RT_GROUP_ICON = 14;
    private const int RT_VERSION = 16;
    private const int GROUP_ICON_RESOURCE_ID = 1;
    private const int VERSION_RESOURCE_ID = 1;
    private const ushort LANG_EN_US = 1033;

    public static void Apply(string exePath, ushort major, ushort minor, ushort patch, ushort build, string iconPath)
    {
        byte[] versionData = BuildVersionResource(major, minor, patch, build);
        IntPtr updateHandle = BeginUpdateResource(exePath, false);
        if (updateHandle == IntPtr.Zero)
        {
            throw new Win32Exception(Marshal.GetLastWin32Error(), "BeginUpdateResource failed");
        }

        try
        {
            if (!UpdateResource(updateHandle, new IntPtr(RT_VERSION), new IntPtr(VERSION_RESOURCE_ID), LANG_EN_US, versionData, (uint)versionData.Length))
            {
                throw new Win32Exception(Marshal.GetLastWin32Error(), "UpdateResource for version failed");
            }

            ApplyIconResource(updateHandle, iconPath);
        }
        catch
        {
            EndUpdateResource(updateHandle, true);
            throw;
        }

        if (!EndUpdateResource(updateHandle, false))
        {
            throw new Win32Exception(Marshal.GetLastWin32Error(), "EndUpdateResource failed");
        }
    }

    private static byte[] BuildVersionResource(ushort major, ushort minor, ushort patch, ushort build)
    {
        string versionString = string.Format("{0}.{1}.{2}.{3}", major, minor, patch, build);
        byte[] fixedFileInfo = BuildFixedFileInfo(major, minor, patch, build);

        byte[] stringTable = BuildBlock(
            "040904B0",
            1,
            Array.Empty<byte>(),
            0,
            new List<byte[]>
            {
                BuildString("CompanyName", "kernullist"),
                BuildString("FileDescription", "Kernforge terminal coding forge"),
                BuildString("FileVersion", versionString),
                BuildString("InternalName", "kernforge.exe"),
                BuildString("LegalCopyright", "kernullist"),
                BuildString("OriginalFilename", "kernforge.exe"),
                BuildString("ProductName", "Kernforge"),
                BuildString("ProductVersion", versionString)
            });

        byte[] stringFileInfo = BuildBlock(
            "StringFileInfo",
            1,
            Array.Empty<byte>(),
            0,
            new List<byte[]> { stringTable });

        byte[] translationValue = new byte[4];
        BitConverter.GetBytes((ushort)0x0409).CopyTo(translationValue, 0);
        BitConverter.GetBytes((ushort)1200).CopyTo(translationValue, 2);

        byte[] varInfo = BuildBlock(
            "Translation",
            0,
            translationValue,
            translationValue.Length,
            new List<byte[]>());

        byte[] varFileInfo = BuildBlock(
            "VarFileInfo",
            1,
            Array.Empty<byte>(),
            0,
            new List<byte[]> { varInfo });

        return BuildBlock(
            "VS_VERSION_INFO",
            0,
            fixedFileInfo,
            fixedFileInfo.Length,
            new List<byte[]> { stringFileInfo, varFileInfo });
    }

    private static byte[] BuildString(string key, string value)
    {
        byte[] valueBytes = Encoding.Unicode.GetBytes(value + "\0");
        return BuildBlock(key, 1, valueBytes, value.Length + 1, new List<byte[]>());
    }

    private static byte[] BuildFixedFileInfo(ushort major, ushort minor, ushort patch, ushort build)
    {
        using (var ms = new MemoryStream())
        using (var writer = new BinaryWriter(ms, Encoding.Unicode))
        {
            writer.Write(0xFEEF04BDu);
            writer.Write(0x00010000u);
            writer.Write(((uint)major << 16) | minor);
            writer.Write(((uint)patch << 16) | build);
            writer.Write(((uint)major << 16) | minor);
            writer.Write(((uint)patch << 16) | build);
            writer.Write(0x0000003Fu);
            writer.Write(0u);
            writer.Write(0x00040004u);
            writer.Write(0x00000001u);
            writer.Write(0u);
            writer.Write(0u);
            writer.Write(0u);
            writer.Flush();
            return ms.ToArray();
        }
    }

    private static byte[] BuildBlock(string key, ushort type, byte[] valueBytes, int valueLength, IList<byte[]> children)
    {
        using (var ms = new MemoryStream())
        using (var writer = new BinaryWriter(ms, Encoding.Unicode))
        {
            writer.Write((ushort)0);
            writer.Write((ushort)valueLength);
            writer.Write(type);
            writer.Write(Encoding.Unicode.GetBytes(key + "\0"));
            PadToDword(ms);

            if (valueBytes.Length > 0)
            {
                writer.Write(valueBytes);
            }
            PadToDword(ms);

            foreach (byte[] child in children)
            {
                writer.Write(child);
            }

            ushort totalLength = checked((ushort)ms.Length);
            ms.Position = 0;
            writer.Write(totalLength);
            writer.Flush();
            return ms.ToArray();
        }
    }

    private static void ApplyIconResource(IntPtr updateHandle, string iconPath)
    {
        if (string.IsNullOrWhiteSpace(iconPath) || !File.Exists(iconPath))
        {
            throw new FileNotFoundException("Icon file was not found", iconPath);
        }

        byte[] iconBytes = File.ReadAllBytes(iconPath);
        using (var ms = new MemoryStream(iconBytes))
        using (var reader = new BinaryReader(ms))
        {
            ushort reserved = reader.ReadUInt16();
            ushort type = reader.ReadUInt16();
            ushort count = reader.ReadUInt16();

            if (reserved != 0 || type != 1 || count == 0)
            {
                throw new InvalidDataException("Invalid .ico file");
            }

            var entries = new List<IconDirEntry>();
            for (int i = 0; i < count; i++)
            {
                entries.Add(new IconDirEntry
                {
                    Width = reader.ReadByte(),
                    Height = reader.ReadByte(),
                    ColorCount = reader.ReadByte(),
                    Reserved = reader.ReadByte(),
                    Planes = reader.ReadUInt16(),
                    BitCount = reader.ReadUInt16(),
                    BytesInRes = reader.ReadUInt32(),
                    ImageOffset = reader.ReadUInt32()
                });
            }

            for (int i = 0; i < entries.Count; i++)
            {
                IconDirEntry entry = entries[i];
                byte[] imageData = new byte[entry.BytesInRes];
                Buffer.BlockCopy(iconBytes, (int)entry.ImageOffset, imageData, 0, (int)entry.BytesInRes);
                if (!UpdateResource(updateHandle, new IntPtr(RT_ICON), new IntPtr(i + 1), LANG_EN_US, imageData, (uint)imageData.Length))
                {
                    throw new Win32Exception(Marshal.GetLastWin32Error(), "UpdateResource for icon image failed");
                }
            }

            byte[] groupIcon = BuildGroupIcon(entries);
            if (!UpdateResource(updateHandle, new IntPtr(RT_GROUP_ICON), new IntPtr(GROUP_ICON_RESOURCE_ID), LANG_EN_US, groupIcon, (uint)groupIcon.Length))
            {
                throw new Win32Exception(Marshal.GetLastWin32Error(), "UpdateResource for group icon failed");
            }
        }
    }

    private static byte[] BuildGroupIcon(List<IconDirEntry> entries)
    {
        using (var ms = new MemoryStream())
        using (var writer = new BinaryWriter(ms))
        {
            writer.Write((ushort)0);
            writer.Write((ushort)1);
            writer.Write((ushort)entries.Count);

            for (int i = 0; i < entries.Count; i++)
            {
                IconDirEntry entry = entries[i];
                writer.Write(entry.Width);
                writer.Write(entry.Height);
                writer.Write(entry.ColorCount);
                writer.Write(entry.Reserved);
                writer.Write(entry.Planes);
                writer.Write(entry.BitCount);
                writer.Write(entry.BytesInRes);
                writer.Write((ushort)(i + 1));
            }

            writer.Flush();
            return ms.ToArray();
        }
    }

    private static void PadToDword(Stream stream)
    {
        while ((stream.Position % 4) != 0)
        {
            stream.WriteByte(0);
        }
    }

    private struct IconDirEntry
    {
        public byte Width;
        public byte Height;
        public byte ColorCount;
        public byte Reserved;
        public ushort Planes;
        public ushort BitCount;
        public uint BytesInRes;
        public uint ImageOffset;
    }
}

