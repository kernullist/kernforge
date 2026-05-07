package main

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var createDriverPOCNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

type createDriverPOCSpec struct {
	DriverName       string
	FunctionPrefix   string
	UpperName        string
	SolutionGUID     string
	DriverGUID       string
	TesterGUID       string
	DeviceClassGUID  string
	DeviceClassCGUID string
}

type createDriverPOCFile struct {
	RelativePath string
	Content      string
}

func (rt *runtimeState) handleCreateDriverPOCCommand(args string) error {
	spec, err := parseCreateDriverPOCSpec(args)
	if err != nil {
		return err
	}

	workspaceRoot := strings.TrimSpace(rt.workspace.Root)
	if workspaceRoot == "" && rt.session != nil {
		workspaceRoot = strings.TrimSpace(rt.session.WorkingDir)
	}
	if workspaceRoot == "" {
		workspaceRoot, err = os.Getwd()
		if err != nil {
			return err
		}
	}

	targetRoot := filepath.Join(workspaceRoot, spec.DriverName)
	empty, err := createDriverPOCDirectoryIsEmpty(targetRoot)
	if err != nil {
		return err
	}
	if !empty {
		return fmt.Errorf("target directory already exists and is not empty: %s", targetRoot)
	}

	files := renderCreateDriverPOCFiles(spec)
	for _, file := range files {
		path := filepath.Join(targetRoot, filepath.FromSlash(file.RelativePath))
		if err := atomicWriteFile(path, []byte(normalizeGeneratedText(file.Content)), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}

	writer := rt.writer
	if writer == nil {
		writer = io.Discard
	}
	ui := rt.ui
	fmt.Fprintln(writer, ui.section("Driver POC"))
	fmt.Fprintln(writer, ui.successLine("Generated x64 C++20 MSVC driver POC solution."))
	fmt.Fprintln(writer, ui.statusKV("solution", filepath.Join(targetRoot, spec.DriverName+".sln")))
	fmt.Fprintln(writer, ui.statusKV("driver_project", filepath.Join(targetRoot, spec.DriverName, spec.DriverName+".vcxproj")))
	fmt.Fprintln(writer, ui.statusKV("tester_project", filepath.Join(targetRoot, spec.DriverName+"-tester", spec.DriverName+"-tester.vcxproj")))
	fmt.Fprintln(writer, ui.statusKV("tester_binary", spec.DriverName+"-tester.exe"))
	fmt.Fprintln(writer, ui.statusKV("platforms", "Debug|x64, Release|x64"))
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, ui.hintLine("Build: msbuild \""+filepath.Join(targetRoot, spec.DriverName+".sln")+"\" /p:Configuration=Debug /p:Platform=x64"))
	fmt.Fprintln(writer, ui.hintLine("Run the tester from the output directory as Administrator. Loading unsigned x64 drivers requires test-signing or an equivalent lab policy."))
	return nil
}

func parseCreateDriverPOCSpec(args string) (createDriverPOCSpec, error) {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) != 1 {
		return createDriverPOCSpec{}, fmt.Errorf("usage: /create-driver-poc <driver-name>")
	}
	name := strings.TrimSpace(fields[0])
	if !createDriverPOCNamePattern.MatchString(name) {
		return createDriverPOCSpec{}, fmt.Errorf("invalid driver name %q: use 1-64 ASCII letters, digits, or underscores, starting with a letter", name)
	}

	driverGUIDBytes := createDriverPOCGUIDBytes("driver-project", name)
	testerGUIDBytes := createDriverPOCGUIDBytes("tester-project", name)
	deviceClassGUIDBytes := createDriverPOCGUIDBytes("device-class", name)
	return createDriverPOCSpec{
		DriverName:       name,
		FunctionPrefix:   createDriverPOCFunctionPrefix(name),
		UpperName:        strings.ToUpper(name),
		SolutionGUID:     formatCreateDriverPOCSolutionGUID(createDriverPOCGUIDBytes("solution", name)),
		DriverGUID:       formatCreateDriverPOCSolutionGUID(driverGUIDBytes),
		TesterGUID:       formatCreateDriverPOCSolutionGUID(testerGUIDBytes),
		DeviceClassGUID:  formatCreateDriverPOCSolutionGUID(deviceClassGUIDBytes),
		DeviceClassCGUID: formatCreateDriverPOCCGUIDInitializer(deviceClassGUIDBytes),
	}, nil
}

func createDriverPOCFunctionPrefix(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Driver"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func createDriverPOCDirectoryIsEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err == nil {
		return len(entries) == 0, nil
	}
	if os.IsNotExist(err) {
		return true, nil
	}
	return false, err
}

func createDriverPOCGUIDBytes(namespace string, name string) [16]byte {
	sum := sha1.Sum([]byte("kernforge:create-driver-poc:" + namespace + ":" + strings.ToLower(name)))
	var out [16]byte
	copy(out[:], sum[:16])
	out[6] = (out[6] & 0x0f) | 0x50
	out[8] = (out[8] & 0x3f) | 0x80
	return out
}

func formatCreateDriverPOCSolutionGUID(guid [16]byte) string {
	return fmt.Sprintf("{%08X-%04X-%04X-%04X-%012X}",
		binary.BigEndian.Uint32(guid[0:4]),
		binary.BigEndian.Uint16(guid[4:6]),
		binary.BigEndian.Uint16(guid[6:8]),
		binary.BigEndian.Uint16(guid[8:10]),
		guid[10:16],
	)
}

func formatCreateDriverPOCCGUIDInitializer(guid [16]byte) string {
	return fmt.Sprintf("{ 0x%08X, 0x%04X, 0x%04X, { 0x%02X, 0x%02X, 0x%02X, 0x%02X, 0x%02X, 0x%02X, 0x%02X, 0x%02X } }",
		binary.BigEndian.Uint32(guid[0:4]),
		binary.BigEndian.Uint16(guid[4:6]),
		binary.BigEndian.Uint16(guid[6:8]),
		guid[8],
		guid[9],
		guid[10],
		guid[11],
		guid[12],
		guid[13],
		guid[14],
		guid[15],
	)
}

func renderCreateDriverPOCFiles(spec createDriverPOCSpec) []createDriverPOCFile {
	return []createDriverPOCFile{
		{RelativePath: spec.DriverName + ".sln", Content: renderCreateDriverPOCTemplate(createDriverPOCSolutionTemplate, spec)},
		{RelativePath: "shared/Ioctl.h", Content: renderCreateDriverPOCTemplate(createDriverPOCIoctlHeaderTemplate, spec)},
		{RelativePath: spec.DriverName + "/Driver.h", Content: renderCreateDriverPOCTemplate(createDriverPOCDriverHeaderTemplate, spec)},
		{RelativePath: spec.DriverName + "/Driver.cpp", Content: renderCreateDriverPOCTemplate(createDriverPOCDriverSourceTemplate, spec)},
		{RelativePath: spec.DriverName + "/" + spec.DriverName + ".vcxproj", Content: renderCreateDriverPOCTemplate(createDriverPOCDriverProjectTemplate, spec)},
		{RelativePath: spec.DriverName + "/" + spec.DriverName + ".vcxproj.filters", Content: renderCreateDriverPOCTemplate(createDriverPOCDriverFiltersTemplate, spec)},
		{RelativePath: spec.DriverName + "-tester/main.cpp", Content: renderCreateDriverPOCTemplate(createDriverPOCTesterSourceTemplate, spec)},
		{RelativePath: spec.DriverName + "-tester/" + spec.DriverName + "-tester.vcxproj", Content: renderCreateDriverPOCTemplate(createDriverPOCTesterProjectTemplate, spec)},
		{RelativePath: spec.DriverName + "-tester/" + spec.DriverName + "-tester.vcxproj.filters", Content: renderCreateDriverPOCTemplate(createDriverPOCTesterFiltersTemplate, spec)},
		{RelativePath: "README.md", Content: renderCreateDriverPOCTemplate(createDriverPOCReadmeTemplate, spec)},
	}
}

func renderCreateDriverPOCTemplate(template string, spec createDriverPOCSpec) string {
	replacer := strings.NewReplacer(
		"{{DRIVER_NAME}}", spec.DriverName,
		"{{FUNCTION_PREFIX}}", spec.FunctionPrefix,
		"{{UPPER_NAME}}", spec.UpperName,
		"{{SOLUTION_GUID}}", spec.SolutionGUID,
		"{{DRIVER_GUID}}", spec.DriverGUID,
		"{{TESTER_GUID}}", spec.TesterGUID,
		"{{DEVICE_CLASS_GUID}}", spec.DeviceClassGUID,
		"{{DEVICE_CLASS_C_GUID}}", spec.DeviceClassCGUID,
	)
	return replacer.Replace(template)
}

func normalizeGeneratedText(text string) string {
	return strings.ReplaceAll(strings.TrimLeft(text, "\r\n"), "\r\n", "\n")
}

const createDriverPOCSolutionTemplate = `
Microsoft Visual Studio Solution File, Format Version 12.00
# Visual Studio Version 17
VisualStudioVersion = 17.0.31903.59
MinimumVisualStudioVersion = 10.0.40219.1
Project("{BC8A1FFA-BEE3-4634-8014-F334798102B3}") = "{{DRIVER_NAME}}", "{{DRIVER_NAME}}\{{DRIVER_NAME}}.vcxproj", "{{DRIVER_GUID}}"
EndProject
Project("{BC8A1FFA-BEE3-4634-8014-F334798102B3}") = "{{DRIVER_NAME}}-tester", "{{DRIVER_NAME}}-tester\{{DRIVER_NAME}}-tester.vcxproj", "{{TESTER_GUID}}"
	ProjectSection(ProjectDependencies) = postProject
		{{DRIVER_GUID}} = {{DRIVER_GUID}}
	EndProjectSection
EndProject
Global
	GlobalSection(SolutionConfigurationPlatforms) = preSolution
		Debug|x64 = Debug|x64
		Release|x64 = Release|x64
	EndGlobalSection
	GlobalSection(ProjectConfigurationPlatforms) = postSolution
		{{DRIVER_GUID}}.Debug|x64.ActiveCfg = Debug|x64
		{{DRIVER_GUID}}.Debug|x64.Build.0 = Debug|x64
		{{DRIVER_GUID}}.Release|x64.ActiveCfg = Release|x64
		{{DRIVER_GUID}}.Release|x64.Build.0 = Release|x64
		{{TESTER_GUID}}.Debug|x64.ActiveCfg = Debug|x64
		{{TESTER_GUID}}.Debug|x64.Build.0 = Debug|x64
		{{TESTER_GUID}}.Release|x64.ActiveCfg = Release|x64
		{{TESTER_GUID}}.Release|x64.Build.0 = Release|x64
	EndGlobalSection
	GlobalSection(SolutionProperties) = preSolution
		HideSolutionNode = FALSE
	EndGlobalSection
EndGlobal
`

const createDriverPOCIoctlHeaderTemplate = `
#pragma once

#ifndef CTL_CODE
#error Include ntddk.h or winioctl.h before including Ioctl.h.
#endif

namespace {{FUNCTION_PREFIX}}Contract
{
inline constexpr wchar_t ServiceName[] = L"{{DRIVER_NAME}}";
inline constexpr wchar_t DisplayName[] = L"{{DRIVER_NAME}} POC Driver";
inline constexpr wchar_t DriverFileName[] = L"{{DRIVER_NAME}}.sys";
inline constexpr wchar_t NtDeviceName[] = L"\\Device\\{{DRIVER_NAME}}";
inline constexpr wchar_t DosDeviceName[] = L"\\DosDevices\\{{DRIVER_NAME}}";
inline constexpr wchar_t Win32DeviceName[] = L"\\\\.\\{{DRIVER_NAME}}";
inline constexpr ULONG DeviceType = 0x8000;
inline constexpr ULONG IoctlPing = CTL_CODE(DeviceType, 0x800, METHOD_BUFFERED, FILE_ANY_ACCESS);
}
`

const createDriverPOCDriverHeaderTemplate = `
#pragma once

#include <ntddk.h>
#include <wdmsec.h>

#include "..\shared\Ioctl.h"

extern "C"
DRIVER_INITIALIZE DriverEntry;

DRIVER_UNLOAD {{FUNCTION_PREFIX}}Unload;

_Dispatch_type_(IRP_MJ_CREATE)
_Dispatch_type_(IRP_MJ_CLOSE)
DRIVER_DISPATCH {{FUNCTION_PREFIX}}CreateClose;

_Dispatch_type_(IRP_MJ_DEVICE_CONTROL)
DRIVER_DISPATCH {{FUNCTION_PREFIX}}DeviceControl;
`

const createDriverPOCDriverSourceTemplate = `
#include "Driver.h"

namespace
{
constexpr wchar_t DriverSddl[] = L"D:P(A;;GA;;;SY)(A;;GA;;;BA)";
constexpr GUID DeviceClassGuid = {{DEVICE_CLASS_C_GUID}};
constexpr CHAR PingReply[] = "pong from {{DRIVER_NAME}}";
}

VOID
{{FUNCTION_PREFIX}}Unload(
    _In_ PDRIVER_OBJECT DriverObject
    )
{
    UNICODE_STRING symbolicLinkName = {};

    RtlInitUnicodeString(&symbolicLinkName, {{FUNCTION_PREFIX}}Contract::DosDeviceName);
    IoDeleteSymbolicLink(&symbolicLinkName);

    if (DriverObject->DeviceObject != nullptr)
    {
        IoDeleteDevice(DriverObject->DeviceObject);
    }
}

NTSTATUS
{{FUNCTION_PREFIX}}CreateClose(
    _In_ PDEVICE_OBJECT DeviceObject,
    _Inout_ PIRP Irp
    )
{
    UNREFERENCED_PARAMETER(DeviceObject);

    Irp->IoStatus.Status = STATUS_SUCCESS;
    Irp->IoStatus.Information = 0;
    IoCompleteRequest(Irp, IO_NO_INCREMENT);

    return STATUS_SUCCESS;
}

NTSTATUS
{{FUNCTION_PREFIX}}DeviceControl(
    _In_ PDEVICE_OBJECT DeviceObject,
    _Inout_ PIRP Irp
    )
{
    PIO_STACK_LOCATION stack = NULL;
    NTSTATUS status = STATUS_INVALID_DEVICE_REQUEST;
    ULONG_PTR information = 0;

    UNREFERENCED_PARAMETER(DeviceObject);

    stack = IoGetCurrentIrpStackLocation(Irp);

    switch (stack->Parameters.DeviceIoControl.IoControlCode)
    {
    case {{FUNCTION_PREFIX}}Contract::IoctlPing:
    {
        ULONG outputBufferLength = stack->Parameters.DeviceIoControl.OutputBufferLength;

        if (Irp->AssociatedIrp.SystemBuffer == NULL)
        {
            status = STATUS_INVALID_USER_BUFFER;
            break;
        }

        if (outputBufferLength < sizeof(PingReply))
        {
            status = STATUS_BUFFER_TOO_SMALL;
            information = sizeof(PingReply);
            break;
        }

        RtlCopyMemory(Irp->AssociatedIrp.SystemBuffer, PingReply, sizeof(PingReply));
        information = sizeof(PingReply);
        status = STATUS_SUCCESS;
        break;
    }
    default:
    {
        status = STATUS_INVALID_DEVICE_REQUEST;
        break;
    }
    }

    Irp->IoStatus.Status = status;
    Irp->IoStatus.Information = information;
    IoCompleteRequest(Irp, IO_NO_INCREMENT);

    return status;
}

extern "C"
NTSTATUS
DriverEntry(
    _In_ PDRIVER_OBJECT DriverObject,
    _In_ PUNICODE_STRING RegistryPath
    )
{
    NTSTATUS status = STATUS_UNSUCCESSFUL;

    UNREFERENCED_PARAMETER(RegistryPath);

    do
    {
        PDEVICE_OBJECT deviceObject = nullptr;
        UNICODE_STRING deviceName = {};
        UNICODE_STRING symbolicLinkName = {};
        UNICODE_STRING sddl = {};

        RtlInitUnicodeString(&deviceName, {{FUNCTION_PREFIX}}Contract::NtDeviceName);
        RtlInitUnicodeString(&symbolicLinkName, {{FUNCTION_PREFIX}}Contract::DosDeviceName);
        RtlInitUnicodeString(&sddl, DriverSddl);

        status = IoCreateDeviceSecure(
            DriverObject,
            0,
            &deviceName,
            {{FUNCTION_PREFIX}}Contract::DeviceType,
            FILE_DEVICE_SECURE_OPEN,
            FALSE,
            &sddl,
            &DeviceClassGuid,
            &deviceObject);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        deviceObject->Flags |= DO_BUFFERED_IO;

        status = IoCreateSymbolicLink(&symbolicLinkName, &deviceName);
        if (!NT_SUCCESS(status))
        {
            IoDeleteDevice(deviceObject);
            deviceObject = nullptr;
            break;
        }

        DriverObject->MajorFunction[IRP_MJ_CREATE] = {{FUNCTION_PREFIX}}CreateClose;
        DriverObject->MajorFunction[IRP_MJ_CLOSE] = {{FUNCTION_PREFIX}}CreateClose;
        DriverObject->MajorFunction[IRP_MJ_DEVICE_CONTROL] = {{FUNCTION_PREFIX}}DeviceControl;
        DriverObject->DriverUnload = {{FUNCTION_PREFIX}}Unload;

        deviceObject->Flags &= ~DO_DEVICE_INITIALIZING;
    }
    while (FALSE);

    return status;
}
`

const createDriverPOCDriverProjectTemplate = `
<?xml version="1.0" encoding="utf-8"?>
<Project DefaultTargets="Build" ToolsVersion="Current" xmlns="http://schemas.microsoft.com/developer/msbuild/2003">
  <ItemGroup Label="ProjectConfigurations">
    <ProjectConfiguration Include="Debug|x64">
      <Configuration>Debug</Configuration>
      <Platform>x64</Platform>
    </ProjectConfiguration>
    <ProjectConfiguration Include="Release|x64">
      <Configuration>Release</Configuration>
      <Platform>x64</Platform>
    </ProjectConfiguration>
  </ItemGroup>
  <PropertyGroup Label="Globals">
    <ProjectGuid>{{DRIVER_GUID}}</ProjectGuid>
    <Keyword>Win32Proj</Keyword>
    <RootNamespace>{{DRIVER_NAME}}</RootNamespace>
    <ProjectName>{{DRIVER_NAME}}</ProjectName>
    <WindowsTargetPlatformVersion>10.0</WindowsTargetPlatformVersion>
  </PropertyGroup>
  <Import Project="$(VCTargetsPath)\Microsoft.Cpp.Default.props" />
  <PropertyGroup Condition="'$(Configuration)|$(Platform)'=='Debug|x64'" Label="Configuration">
    <TargetVersion>Windows10</TargetVersion>
    <ConfigurationType>Driver</ConfigurationType>
    <DriverType>WDM</DriverType>
    <DriverTargetPlatform>Desktop</DriverTargetPlatform>
    <PlatformToolset>WindowsKernelModeDriver10.0</PlatformToolset>
    <UseDebugLibraries>true</UseDebugLibraries>
    <CharacterSet>Unicode</CharacterSet>
  </PropertyGroup>
  <PropertyGroup Condition="'$(Configuration)|$(Platform)'=='Release|x64'" Label="Configuration">
    <TargetVersion>Windows10</TargetVersion>
    <ConfigurationType>Driver</ConfigurationType>
    <DriverType>WDM</DriverType>
    <DriverTargetPlatform>Desktop</DriverTargetPlatform>
    <PlatformToolset>WindowsKernelModeDriver10.0</PlatformToolset>
    <UseDebugLibraries>false</UseDebugLibraries>
    <WholeProgramOptimization>true</WholeProgramOptimization>
    <CharacterSet>Unicode</CharacterSet>
  </PropertyGroup>
  <Import Project="$(VCTargetsPath)\Microsoft.Cpp.props" />
  <ImportGroup Label="PropertySheets" Condition="'$(Configuration)|$(Platform)'=='Debug|x64'">
    <Import Project="$(UserRootDir)\Microsoft.Cpp.$(Platform).user.props" Condition="exists('$(UserRootDir)\Microsoft.Cpp.$(Platform).user.props')" Label="LocalAppDataPlatform" />
  </ImportGroup>
  <ImportGroup Label="PropertySheets" Condition="'$(Configuration)|$(Platform)'=='Release|x64'">
    <Import Project="$(UserRootDir)\Microsoft.Cpp.$(Platform).user.props" Condition="exists('$(UserRootDir)\Microsoft.Cpp.$(Platform).user.props')" Label="LocalAppDataPlatform" />
  </ImportGroup>
  <PropertyGroup Condition="'$(Configuration)|$(Platform)'=='Debug|x64'">
    <OutDir>$(SolutionDir)bin\$(Configuration)\$(Platform)\</OutDir>
    <IntDir>$(SolutionDir)obj\$(ProjectName)\$(Configuration)\$(Platform)\</IntDir>
    <TargetName>{{DRIVER_NAME}}</TargetName>
    <TargetExt>.sys</TargetExt>
  </PropertyGroup>
  <PropertyGroup Condition="'$(Configuration)|$(Platform)'=='Release|x64'">
    <OutDir>$(SolutionDir)bin\$(Configuration)\$(Platform)\</OutDir>
    <IntDir>$(SolutionDir)obj\$(ProjectName)\$(Configuration)\$(Platform)\</IntDir>
    <TargetName>{{DRIVER_NAME}}</TargetName>
    <TargetExt>.sys</TargetExt>
  </PropertyGroup>
  <ItemDefinitionGroup Condition="'$(Configuration)|$(Platform)'=='Debug|x64'">
    <ClCompile>
      <WarningLevel>Level4</WarningLevel>
      <TreatWarningAsError>true</TreatWarningAsError>
      <LanguageStandard>stdcpp20</LanguageStandard>
      <AdditionalIncludeDirectories>$(SolutionDir)shared;%(AdditionalIncludeDirectories)</AdditionalIncludeDirectories>
      <PreprocessorDefinitions>DBG=1;%(PreprocessorDefinitions)</PreprocessorDefinitions>
    </ClCompile>
    <Link>
      <AdditionalDependencies>Wdmsec.lib;%(AdditionalDependencies)</AdditionalDependencies>
    </Link>
    <DriverSign>
      <FileDigestAlgorithm>sha256</FileDigestAlgorithm>
      <SignMode>Off</SignMode>
    </DriverSign>
  </ItemDefinitionGroup>
  <ItemDefinitionGroup Condition="'$(Configuration)|$(Platform)'=='Release|x64'">
    <ClCompile>
      <WarningLevel>Level4</WarningLevel>
      <TreatWarningAsError>true</TreatWarningAsError>
      <LanguageStandard>stdcpp20</LanguageStandard>
      <AdditionalIncludeDirectories>$(SolutionDir)shared;%(AdditionalIncludeDirectories)</AdditionalIncludeDirectories>
      <PreprocessorDefinitions>NDEBUG;%(PreprocessorDefinitions)</PreprocessorDefinitions>
    </ClCompile>
    <Link>
      <AdditionalDependencies>Wdmsec.lib;%(AdditionalDependencies)</AdditionalDependencies>
    </Link>
    <DriverSign>
      <FileDigestAlgorithm>sha256</FileDigestAlgorithm>
      <SignMode>Off</SignMode>
    </DriverSign>
  </ItemDefinitionGroup>
  <ItemGroup>
    <ClCompile Include="Driver.cpp" />
  </ItemGroup>
  <ItemGroup>
    <ClInclude Include="Driver.h" />
    <ClInclude Include="..\shared\Ioctl.h" />
  </ItemGroup>
  <Import Project="$(VCTargetsPath)\Microsoft.Cpp.targets" />
  <ImportGroup Label="ExtensionTargets">
  </ImportGroup>
</Project>
`

const createDriverPOCDriverFiltersTemplate = `
<?xml version="1.0" encoding="utf-8"?>
<Project ToolsVersion="4.0" xmlns="http://schemas.microsoft.com/developer/msbuild/2003">
  <ItemGroup>
    <Filter Include="Source Files">
      <UniqueIdentifier>{B0D63B53-2C5D-4EA5-AE56-96350910F51D}</UniqueIdentifier>
      <Extensions>c;cpp;cxx</Extensions>
    </Filter>
    <Filter Include="Header Files">
      <UniqueIdentifier>{8F4900F0-20B1-4B6D-82CE-2E76673B0603}</UniqueIdentifier>
      <Extensions>h;hpp;hxx</Extensions>
    </Filter>
  </ItemGroup>
  <ItemGroup>
    <ClCompile Include="Driver.cpp">
      <Filter>Source Files</Filter>
    </ClCompile>
  </ItemGroup>
  <ItemGroup>
    <ClInclude Include="Driver.h">
      <Filter>Header Files</Filter>
    </ClInclude>
    <ClInclude Include="..\shared\Ioctl.h">
      <Filter>Header Files</Filter>
    </ClInclude>
  </ItemGroup>
</Project>
`

const createDriverPOCTesterSourceTemplate = `
#include <windows.h>
#include <winioctl.h>

#include <array>
#include <iostream>
#include <string>

#include "..\shared\Ioctl.h"

namespace
{
constexpr DWORD StopPollCount = 50;
constexpr DWORD StopPollDelayMs = 100;
constexpr DWORD MaxPathBufferChars = 32768;
constexpr DWORD PingOutputBufferSize = 128;

std::wstring
GetLastErrorText(
    _In_ DWORD error
    )
{
    std::wstring result;
    wchar_t* buffer = nullptr;

    do
    {
        DWORD length = FormatMessageW(
            FORMAT_MESSAGE_ALLOCATE_BUFFER | FORMAT_MESSAGE_FROM_SYSTEM | FORMAT_MESSAGE_IGNORE_INSERTS,
            nullptr,
            error,
            0,
            reinterpret_cast<wchar_t*>(&buffer),
            0,
            nullptr);
        if (length == 0 || buffer == nullptr)
        {
            result = L"error " + std::to_wstring(error);
            break;
        }

        result.assign(buffer, length);
        while (!result.empty() && (result.back() == L'\r' || result.back() == L'\n' || result.back() == L' '))
        {
            result.pop_back();
        }
    }
    while (false);

    if (buffer != nullptr)
    {
        LocalFree(buffer);
        buffer = nullptr;
    }

    return result;
}

void
PrintLastError(
    _In_ const wchar_t* action,
    _In_ DWORD error
    )
{
    std::wcerr << action << L" failed: " << error << L" (" << GetLastErrorText(error) << L")" << std::endl;
}

bool
GetExecutableDirectory(
    _Out_ std::wstring& directory
    )
{
    bool result = false;

    do
    {
        std::wstring path;

        for (DWORD capacity = MAX_PATH; capacity <= MaxPathBufferChars; capacity *= 2)
        {
            path.assign(capacity, L'\0');

            DWORD length = GetModuleFileNameW(nullptr, path.data(), capacity);
            if (length == 0)
            {
                PrintLastError(L"GetModuleFileNameW", GetLastError());
                break;
            }

            if (length < capacity)
            {
                path.resize(length);
                break;
            }

            if (capacity > MaxPathBufferChars / 2)
            {
                std::wcerr << L"Executable path is too long." << std::endl;
                path.clear();
                break;
            }
        }

        if (path.empty())
        {
            break;
        }

        directory = path;
        size_t slash = directory.find_last_of(L"\\/");
        if (slash == std::wstring::npos)
        {
            std::wcerr << L"Could not resolve executable directory." << std::endl;
            break;
        }

        directory.resize(slash);
        result = true;
    }
    while (false);

    return result;
}

std::wstring
JoinPath(
    _In_ const std::wstring& directory,
    _In_ const std::wstring& leaf
    )
{
    std::wstring result = directory;

    if (!result.empty() && result.back() != L'\\' && result.back() != L'/')
    {
        result += L'\\';
    }

    result += leaf;
    return result;
}

SC_HANDLE
OpenOrCreateDriverService(
    _In_ SC_HANDLE scm,
    _In_ const std::wstring& driverPath
    )
{
    SC_HANDLE service = nullptr;

    do
    {
        service = CreateServiceW(
            scm,
            {{FUNCTION_PREFIX}}Contract::ServiceName,
            {{FUNCTION_PREFIX}}Contract::DisplayName,
            SERVICE_START | SERVICE_STOP | SERVICE_QUERY_STATUS | DELETE | SERVICE_CHANGE_CONFIG,
            SERVICE_KERNEL_DRIVER,
            SERVICE_DEMAND_START,
            SERVICE_ERROR_NORMAL,
            driverPath.c_str(),
            nullptr,
            nullptr,
            nullptr,
            nullptr,
            nullptr);
        if (service != nullptr)
        {
            break;
        }

        DWORD error = GetLastError();
        if (error != ERROR_SERVICE_EXISTS)
        {
            PrintLastError(L"CreateServiceW", error);
            break;
        }

        service = OpenServiceW(
            scm,
            {{FUNCTION_PREFIX}}Contract::ServiceName,
            SERVICE_START | SERVICE_STOP | SERVICE_QUERY_STATUS | DELETE | SERVICE_CHANGE_CONFIG);
        if (service == nullptr)
        {
            PrintLastError(L"OpenServiceW", GetLastError());
            break;
        }

        if (!ChangeServiceConfigW(
                service,
                SERVICE_KERNEL_DRIVER,
                SERVICE_DEMAND_START,
                SERVICE_ERROR_NORMAL,
                driverPath.c_str(),
                nullptr,
                nullptr,
                nullptr,
                nullptr,
                nullptr,
                {{FUNCTION_PREFIX}}Contract::DisplayName))
        {
            PrintLastError(L"ChangeServiceConfigW", GetLastError());
            CloseServiceHandle(service);
            service = nullptr;
            break;
        }
    }
    while (false);

    return service;
}

bool
WaitForServiceState(
    _In_ SC_HANDLE service,
    _In_ DWORD desiredState
    )
{
    bool result = false;

    for (DWORD index = 0; index < StopPollCount; index++)
    {
        SERVICE_STATUS_PROCESS processStatus = {};
        DWORD bytesNeeded = 0;
        if (!QueryServiceStatusEx(
                service,
                SC_STATUS_PROCESS_INFO,
                reinterpret_cast<LPBYTE>(&processStatus),
                sizeof(processStatus),
                &bytesNeeded))
        {
            PrintLastError(L"QueryServiceStatusEx", GetLastError());
            break;
        }

        if (processStatus.dwCurrentState == desiredState)
        {
            result = true;
            break;
        }

        Sleep(StopPollDelayMs);
    }

    return result;
}

bool
StopDriverService(
    _In_ SC_HANDLE service
    )
{
    bool result = false;
    SERVICE_STATUS status = {};

    do
    {
        if (ControlService(service, SERVICE_CONTROL_STOP, &status))
        {
            result = WaitForServiceState(service, SERVICE_STOPPED);
            break;
        }

        DWORD error = GetLastError();
        if (error == ERROR_SERVICE_NOT_ACTIVE)
        {
            result = true;
            break;
        }

        PrintLastError(L"ControlService", error);
    }
    while (false);

    return result;
}

bool
StartDriverService(
    _In_ SC_HANDLE service
    )
{
    bool result = false;

    do
    {
        if (StartServiceW(service, 0, nullptr))
        {
            result = true;
            break;
        }

        DWORD error = GetLastError();
        if (error == ERROR_SERVICE_ALREADY_RUNNING)
        {
            std::wcerr << L"Service is already running; restarting it to load the current driver image." << std::endl;
            if (!StopDriverService(service))
            {
                break;
            }

            if (StartServiceW(service, 0, nullptr))
            {
                result = true;
                break;
            }

            error = GetLastError();
        }

        PrintLastError(L"StartServiceW", error);
    }
    while (false);

    return result;
}

void
StopAndDeleteDriverService(
    _In_ SC_HANDLE service
    )
{
    (void)StopDriverService(service);

    if (!DeleteService(service))
    {
        DWORD error = GetLastError();
        if (error != ERROR_SERVICE_MARKED_FOR_DELETE)
        {
            PrintLastError(L"DeleteService", error);
        }
    }
}

bool
SendPingIoctl()
{
    bool result = false;
    HANDLE device = INVALID_HANDLE_VALUE;

    do
    {
        device = CreateFileW(
            {{FUNCTION_PREFIX}}Contract::Win32DeviceName,
            GENERIC_READ | GENERIC_WRITE,
            0,
            nullptr,
            OPEN_EXISTING,
            FILE_ATTRIBUTE_NORMAL,
            nullptr);
        if (device == INVALID_HANDLE_VALUE)
        {
            PrintLastError(L"CreateFileW", GetLastError());
            break;
        }

        std::array<char, PingOutputBufferSize> output = {};
        DWORD bytesReturned = 0;

        if (!DeviceIoControl(
                device,
                {{FUNCTION_PREFIX}}Contract::IoctlPing,
                nullptr,
                0,
                output.data(),
                static_cast<DWORD>(output.size()),
                &bytesReturned,
                nullptr))
        {
            PrintLastError(L"DeviceIoControl", GetLastError());
            break;
        }

        std::cout << "IOCTL reply: " << output.data() << " (" << bytesReturned << " bytes)" << std::endl;
        result = true;
    }
    while (false);

    if (device != INVALID_HANDLE_VALUE)
    {
        CloseHandle(device);
        device = INVALID_HANDLE_VALUE;
    }

    return result;
}
}

int
wmain()
{
    int exitCode = 1;
    SC_HANDLE scm = nullptr;
    SC_HANDLE service = nullptr;

    do
    {
        std::wstring executableDirectory;
        if (!GetExecutableDirectory(executableDirectory))
        {
            break;
        }

        std::wstring driverPath = JoinPath(executableDirectory, {{FUNCTION_PREFIX}}Contract::DriverFileName);
        DWORD attributes = GetFileAttributesW(driverPath.c_str());
        if (attributes == INVALID_FILE_ATTRIBUTES || (attributes & FILE_ATTRIBUTE_DIRECTORY) != 0)
        {
            std::wcerr << L"Driver file not found next to tester: " << driverPath << std::endl;
            break;
        }

        scm = OpenSCManagerW(nullptr, nullptr, SC_MANAGER_CREATE_SERVICE | SC_MANAGER_CONNECT);
        if (scm == nullptr)
        {
            PrintLastError(L"OpenSCManagerW", GetLastError());
            break;
        }

        service = OpenOrCreateDriverService(scm, driverPath);
        if (service == nullptr)
        {
            break;
        }

        if (!StartDriverService(service))
        {
            break;
        }

        if (!SendPingIoctl())
        {
            break;
        }

        exitCode = 0;
    }
    while (false);

    if (service != nullptr)
    {
        StopAndDeleteDriverService(service);
        CloseServiceHandle(service);
        service = nullptr;
    }

    if (scm != nullptr)
    {
        CloseServiceHandle(scm);
        scm = nullptr;
    }

    return exitCode;
}
`

const createDriverPOCTesterProjectTemplate = `
<?xml version="1.0" encoding="utf-8"?>
<Project DefaultTargets="Build" ToolsVersion="Current" xmlns="http://schemas.microsoft.com/developer/msbuild/2003">
  <ItemGroup Label="ProjectConfigurations">
    <ProjectConfiguration Include="Debug|x64">
      <Configuration>Debug</Configuration>
      <Platform>x64</Platform>
    </ProjectConfiguration>
    <ProjectConfiguration Include="Release|x64">
      <Configuration>Release</Configuration>
      <Platform>x64</Platform>
    </ProjectConfiguration>
  </ItemGroup>
  <PropertyGroup Label="Globals">
    <VCProjectVersion>17.0</VCProjectVersion>
    <ProjectGuid>{{TESTER_GUID}}</ProjectGuid>
    <Keyword>Win32Proj</Keyword>
    <RootNamespace>{{DRIVER_NAME}}Tester</RootNamespace>
    <ProjectName>{{DRIVER_NAME}}-tester</ProjectName>
    <WindowsTargetPlatformVersion>10.0</WindowsTargetPlatformVersion>
  </PropertyGroup>
  <Import Project="$(VCTargetsPath)\Microsoft.Cpp.Default.props" />
  <PropertyGroup Condition="'$(Configuration)|$(Platform)'=='Debug|x64'" Label="Configuration">
    <ConfigurationType>Application</ConfigurationType>
    <UseDebugLibraries>true</UseDebugLibraries>
    <PlatformToolset>v143</PlatformToolset>
    <CharacterSet>Unicode</CharacterSet>
  </PropertyGroup>
  <PropertyGroup Condition="'$(Configuration)|$(Platform)'=='Release|x64'" Label="Configuration">
    <ConfigurationType>Application</ConfigurationType>
    <UseDebugLibraries>false</UseDebugLibraries>
    <PlatformToolset>v143</PlatformToolset>
    <WholeProgramOptimization>true</WholeProgramOptimization>
    <CharacterSet>Unicode</CharacterSet>
  </PropertyGroup>
  <Import Project="$(VCTargetsPath)\Microsoft.Cpp.props" />
  <ImportGroup Label="PropertySheets" Condition="'$(Configuration)|$(Platform)'=='Debug|x64'">
    <Import Project="$(UserRootDir)\Microsoft.Cpp.$(Platform).user.props" Condition="exists('$(UserRootDir)\Microsoft.Cpp.$(Platform).user.props')" Label="LocalAppDataPlatform" />
  </ImportGroup>
  <ImportGroup Label="PropertySheets" Condition="'$(Configuration)|$(Platform)'=='Release|x64'">
    <Import Project="$(UserRootDir)\Microsoft.Cpp.$(Platform).user.props" Condition="exists('$(UserRootDir)\Microsoft.Cpp.$(Platform).user.props')" Label="LocalAppDataPlatform" />
  </ImportGroup>
  <PropertyGroup Condition="'$(Configuration)|$(Platform)'=='Debug|x64'">
    <OutDir>$(SolutionDir)bin\$(Configuration)\$(Platform)\</OutDir>
    <IntDir>$(SolutionDir)obj\$(ProjectName)\$(Configuration)\$(Platform)\</IntDir>
    <TargetName>{{DRIVER_NAME}}-tester</TargetName>
  </PropertyGroup>
  <PropertyGroup Condition="'$(Configuration)|$(Platform)'=='Release|x64'">
    <OutDir>$(SolutionDir)bin\$(Configuration)\$(Platform)\</OutDir>
    <IntDir>$(SolutionDir)obj\$(ProjectName)\$(Configuration)\$(Platform)\</IntDir>
    <TargetName>{{DRIVER_NAME}}-tester</TargetName>
  </PropertyGroup>
  <ItemDefinitionGroup Condition="'$(Configuration)|$(Platform)'=='Debug|x64'">
    <ClCompile>
      <WarningLevel>Level4</WarningLevel>
      <TreatWarningAsError>true</TreatWarningAsError>
      <LanguageStandard>stdcpp20</LanguageStandard>
      <AdditionalIncludeDirectories>$(SolutionDir)shared;%(AdditionalIncludeDirectories)</AdditionalIncludeDirectories>
      <PreprocessorDefinitions>WIN32_LEAN_AND_MEAN;UNICODE;_UNICODE;%(PreprocessorDefinitions)</PreprocessorDefinitions>
    </ClCompile>
  </ItemDefinitionGroup>
  <ItemDefinitionGroup Condition="'$(Configuration)|$(Platform)'=='Release|x64'">
    <ClCompile>
      <WarningLevel>Level4</WarningLevel>
      <TreatWarningAsError>true</TreatWarningAsError>
      <LanguageStandard>stdcpp20</LanguageStandard>
      <RuntimeLibrary>MultiThreaded</RuntimeLibrary>
      <AdditionalIncludeDirectories>$(SolutionDir)shared;%(AdditionalIncludeDirectories)</AdditionalIncludeDirectories>
      <PreprocessorDefinitions>WIN32_LEAN_AND_MEAN;UNICODE;_UNICODE;NDEBUG;%(PreprocessorDefinitions)</PreprocessorDefinitions>
    </ClCompile>
  </ItemDefinitionGroup>
  <ItemGroup>
    <ClCompile Include="main.cpp" />
  </ItemGroup>
  <ItemGroup>
    <ClInclude Include="..\shared\Ioctl.h" />
  </ItemGroup>
  <ItemGroup>
    <ProjectReference Include="..\{{DRIVER_NAME}}\{{DRIVER_NAME}}.vcxproj">
      <Project>{{DRIVER_GUID}}</Project>
      <ReferenceOutputAssembly>false</ReferenceOutputAssembly>
    </ProjectReference>
  </ItemGroup>
  <Import Project="$(VCTargetsPath)\Microsoft.Cpp.targets" />
  <ImportGroup Label="ExtensionTargets">
  </ImportGroup>
</Project>
`

const createDriverPOCTesterFiltersTemplate = `
<?xml version="1.0" encoding="utf-8"?>
<Project ToolsVersion="4.0" xmlns="http://schemas.microsoft.com/developer/msbuild/2003">
  <ItemGroup>
    <Filter Include="Source Files">
      <UniqueIdentifier>{B5F6C4F2-7DD9-4CDE-9419-50D143E19B2D}</UniqueIdentifier>
      <Extensions>cpp;cxx;c</Extensions>
    </Filter>
    <Filter Include="Header Files">
      <UniqueIdentifier>{A7248747-E18F-442D-8770-55B9CF55DB67}</UniqueIdentifier>
      <Extensions>h;hpp;hxx</Extensions>
    </Filter>
  </ItemGroup>
  <ItemGroup>
    <ClCompile Include="main.cpp">
      <Filter>Source Files</Filter>
    </ClCompile>
  </ItemGroup>
  <ItemGroup>
    <ClInclude Include="..\shared\Ioctl.h">
      <Filter>Header Files</Filter>
    </ClInclude>
  </ItemGroup>
</Project>
`

const createDriverPOCReadmeTemplate = `
# {{DRIVER_NAME}} Driver POC

This folder was generated by Kernforge /create-driver-poc {{DRIVER_NAME}}.

## Layout

- {{DRIVER_NAME}}.sln contains x64-only Debug and Release configurations.
- {{DRIVER_NAME}}\{{DRIVER_NAME}}.vcxproj builds {{DRIVER_NAME}}.sys from Driver.cpp.
- {{DRIVER_NAME}}-tester\{{DRIVER_NAME}}-tester.vcxproj builds {{DRIVER_NAME}}-tester.exe.
- shared\Ioctl.h is included by both projects and defines {{FUNCTION_PREFIX}}Contract service names, device names, DeviceType, and IoctlPing.

Both projects write outputs to bin\<Configuration>\x64\, so the tester and driver land in the same directory.
Both projects use C++20. The tester Release build links the MT runtime.
The driver creates its device object with the shared DeviceType contract, and the tester resolves its executable path with a growing buffer instead of a fixed MAX_PATH assumption.

## Build

    msbuild "{{DRIVER_NAME}}.sln" /p:Configuration=Debug /p:Platform=x64

The driver project requires Visual Studio with the Windows Driver Kit and the WindowsKernelModeDriver10.0 platform toolset.

## Run

Run bin\Debug\x64\{{DRIVER_NAME}}-tester.exe as Administrator. The tester:

1. Resolves {{DRIVER_NAME}}.sys next to the tester executable.
2. Creates or updates a demand-start kernel-driver service through SCM.
3. Starts the service, or stops and restarts it first if it was already running so the current driver image is tested.
4. Opens \\.\{{DRIVER_NAME}}.
5. Sends {{FUNCTION_PREFIX}}Contract::IoctlPing.
6. Closes the device handle.
7. Stops and deletes the service.

Unsigned x64 kernel drivers require test-signing or an equivalent lab policy before Windows will load them.
`
