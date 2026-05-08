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
	POCType          string
	POCTypeLabel     string
	DriverType       string
	DriverLibraries  string
	TesterLibraries  string
	ServiceType      string
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
	fmt.Fprintln(writer, ui.statusKV("type", spec.POCTypeLabel))
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
	if len(fields) < 1 {
		return createDriverPOCSpec{}, fmt.Errorf("usage: /create-driver-poc <driver-name> [--type default|objectfilter|minifilter|registryfilter|wfpcallout]")
	}
	name := ""
	pocType := "default"
	for index := 0; index < len(fields); index++ {
		field := strings.TrimSpace(fields[index])
		if field == "--type" {
			index++
			if index >= len(fields) {
				return createDriverPOCSpec{}, fmt.Errorf("--type requires a value")
			}
			pocType = strings.ToLower(strings.TrimSpace(fields[index]))
			continue
		}
		if strings.HasPrefix(field, "--type=") {
			pocType = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(field, "--type=")))
			continue
		}
		if strings.HasPrefix(field, "--") {
			return createDriverPOCSpec{}, fmt.Errorf("unknown option %q", field)
		}
		if name != "" {
			return createDriverPOCSpec{}, fmt.Errorf("usage: /create-driver-poc <driver-name> [--type default|objectfilter|minifilter|registryfilter|wfpcallout]")
		}
		name = field
	}
	if name == "" {
		return createDriverPOCSpec{}, fmt.Errorf("usage: /create-driver-poc <driver-name> [--type default|objectfilter|minifilter|registryfilter|wfpcallout]")
	}
	pocType = normalizeCreateDriverPOCType(pocType)
	if pocType == "" {
		return createDriverPOCSpec{}, fmt.Errorf("invalid --type: use default, objectfilter, minifilter, registryfilter, or wfpcallout")
	}
	if !createDriverPOCNamePattern.MatchString(name) {
		return createDriverPOCSpec{}, fmt.Errorf("invalid driver name %q: use 1-64 ASCII letters, digits, or underscores, starting with a letter", name)
	}

	driverGUIDBytes := createDriverPOCGUIDBytes("driver-project", name)
	testerGUIDBytes := createDriverPOCGUIDBytes("tester-project", name)
	deviceClassGUIDBytes := createDriverPOCGUIDBytes("device-class", name)
	driverType := "WDM"
	driverLibraries := "Wdmsec.lib;%(AdditionalDependencies)"
	testerLibraries := "%(AdditionalDependencies)"
	serviceType := "SERVICE_KERNEL_DRIVER"
	if pocType == "minifilter" {
		driverType = "File System"
		driverLibraries = "FltMgr.lib;Wdmsec.lib;%(AdditionalDependencies)"
		testerLibraries = "FltLib.lib;%(AdditionalDependencies)"
		serviceType = "SERVICE_FILE_SYSTEM_DRIVER"
	}
	if pocType == "wfpcallout" {
		driverLibraries = "Fwpkclnt.lib;Wdmsec.lib;%(AdditionalDependencies)"
	}
	return createDriverPOCSpec{
		DriverName:       name,
		FunctionPrefix:   createDriverPOCFunctionPrefix(name),
		UpperName:        strings.ToUpper(name),
		POCType:          pocType,
		POCTypeLabel:     createDriverPOCTypeLabel(pocType),
		DriverType:       driverType,
		DriverLibraries:  driverLibraries,
		TesterLibraries:  testerLibraries,
		ServiceType:      serviceType,
		SolutionGUID:     formatCreateDriverPOCSolutionGUID(createDriverPOCGUIDBytes("solution", name)),
		DriverGUID:       formatCreateDriverPOCSolutionGUID(driverGUIDBytes),
		TesterGUID:       formatCreateDriverPOCSolutionGUID(testerGUIDBytes),
		DeviceClassGUID:  formatCreateDriverPOCSolutionGUID(deviceClassGUIDBytes),
		DeviceClassCGUID: formatCreateDriverPOCCGUIDInitializer(deviceClassGUIDBytes),
	}, nil
}

func normalizeCreateDriverPOCType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "default", "basic", "wdm":
		return "default"
	case "objectfilter", "object-filter", "obcallback", "obcallbacks":
		return "objectfilter"
	case "minifilter", "mini-filter", "filesystem", "filesystemfilter":
		return "minifilter"
	case "registryfilter", "registry-filter", "cmcallback":
		return "registryfilter"
	case "wfpcallout", "wfp", "wfp-callout":
		return "wfpcallout"
	default:
		return ""
	}
}

func createDriverPOCTypeLabel(value string) string {
	switch value {
	case "objectfilter":
		return "objectfilter"
	case "minifilter":
		return "minifilter"
	case "registryfilter":
		return "registryfilter"
	case "wfpcallout":
		return "wfpcallout"
	default:
		return "default"
	}
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
	ioctlTemplate := createDriverPOCIoctlHeaderTemplate
	driverSourceTemplate := createDriverPOCDriverSourceTemplate
	testerSourceTemplate := createDriverPOCTesterSourceTemplate
	readmeTemplate := createDriverPOCReadmeTemplate
	switch spec.POCType {
	case "objectfilter":
		ioctlTemplate = createDriverPOCObjectFilterIoctlHeaderTemplate
		driverSourceTemplate = createDriverPOCObjectFilterDriverSourceTemplate
		testerSourceTemplate = createDriverPOCObjectFilterTesterSourceTemplate
		readmeTemplate = createDriverPOCObjectFilterReadmeTemplate
	case "minifilter":
		ioctlTemplate = createDriverPOCMinifilterIoctlHeaderTemplate
		driverSourceTemplate = createDriverPOCMinifilterDriverSourceTemplate
		testerSourceTemplate = createDriverPOCMinifilterTesterSourceTemplate
		readmeTemplate = createDriverPOCMinifilterReadmeTemplate
	case "registryfilter":
		ioctlTemplate = createDriverPOCRegistryFilterIoctlHeaderTemplate
		driverSourceTemplate = createDriverPOCRegistryFilterDriverSourceTemplate
		testerSourceTemplate = createDriverPOCRegistryFilterTesterSourceTemplate
		readmeTemplate = createDriverPOCRegistryFilterReadmeTemplate
	case "wfpcallout":
		ioctlTemplate = createDriverPOCWfpCalloutIoctlHeaderTemplate
		driverSourceTemplate = createDriverPOCWfpCalloutDriverSourceTemplate
		testerSourceTemplate = createDriverPOCWfpCalloutTesterSourceTemplate
		readmeTemplate = createDriverPOCWfpCalloutReadmeTemplate
	}
	testerContent := renderCreateDriverPOCTemplate(testerSourceTemplate, spec)
	testerContent = customizeCreateDriverPOCTesterContent(testerContent, spec)
	testerContent = renderCreateDriverPOCTemplate(testerContent, spec)
	return []createDriverPOCFile{
		{RelativePath: spec.DriverName + ".sln", Content: renderCreateDriverPOCTemplate(createDriverPOCSolutionTemplate, spec)},
		{RelativePath: "shared/Ioctl.h", Content: renderCreateDriverPOCTemplate(ioctlTemplate, spec)},
		{RelativePath: spec.DriverName + "/Driver.h", Content: renderCreateDriverPOCTemplate(createDriverPOCDriverHeaderTemplate, spec)},
		{RelativePath: spec.DriverName + "/Driver.cpp", Content: renderCreateDriverPOCTemplate(driverSourceTemplate, spec)},
		{RelativePath: spec.DriverName + "/" + spec.DriverName + ".vcxproj", Content: renderCreateDriverPOCTemplate(createDriverPOCDriverProjectTemplate, spec)},
		{RelativePath: spec.DriverName + "/" + spec.DriverName + ".vcxproj.filters", Content: renderCreateDriverPOCTemplate(createDriverPOCDriverFiltersTemplate, spec)},
		{RelativePath: spec.DriverName + "-tester/main.cpp", Content: testerContent},
		{RelativePath: spec.DriverName + "-tester/" + spec.DriverName + "-tester.vcxproj", Content: renderCreateDriverPOCTemplate(createDriverPOCTesterProjectTemplate, spec)},
		{RelativePath: spec.DriverName + "-tester/" + spec.DriverName + "-tester.vcxproj.filters", Content: renderCreateDriverPOCTemplate(createDriverPOCTesterFiltersTemplate, spec)},
		{RelativePath: "README.md", Content: renderCreateDriverPOCTemplate(readmeTemplate, spec)},
	}
}

func customizeCreateDriverPOCTesterContent(content string, spec createDriverPOCSpec) string {
	if spec.POCType == "minifilter" {
		content = strings.Replace(content, "#include <winioctl.h>", "#include <winioctl.h>\n#include <fltuser.h>\n#include <strsafe.h>", 1)
	}
	if spec.POCType == "registryfilter" || spec.POCType == "wfpcallout" {
		content = strings.Replace(content, "#include <winioctl.h>", "#include <winioctl.h>\n#include <strsafe.h>", 1)
	}
	helperMarker := "\n}\n\nint\nwmain()"
	helper := ""
	marker := `        if (!SendPingIoctl())
        {
            break;
        }`
	replacement := marker
	switch spec.POCType {
	case "objectfilter":
		helper = `

bool
RegisterProtectedIds()
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

        {{FUNCTION_PREFIX}}Contract::ProtectedIds request = {};
        request.ProcessId = GetCurrentProcessId();
        request.ThreadId = GetCurrentThreadId();
        DWORD bytesReturned = 0;

        if (!DeviceIoControl(
                device,
                {{FUNCTION_PREFIX}}Contract::IoctlSetProtectedIds,
                &request,
                sizeof(request),
                nullptr,
                0,
                &bytesReturned,
                nullptr))
        {
            PrintLastError(L"IoctlSetProtectedIds", GetLastError());
            break;
        }

        std::wcout << L"Protected current PID " << request.ProcessId << L" and TID " << request.ThreadId << std::endl;
        result = true;

    } while (false);

    if (device != INVALID_HANDLE_VALUE)
    {
        CloseHandle(device);
        device = INVALID_HANDLE_VALUE;
    }

    return result;
}
`
		replacement = `        if (!SendPingIoctl())
        {
            break;
        }

        if (!RegisterProtectedIds())
        {
            break;
        }`
	case "minifilter":
		helper = `

bool
SetRegistryStringValue(
    _In_ HKEY key,
    _In_opt_ const wchar_t* valueName,
    _In_ const wchar_t* value
    )
{
    DWORD byteCount = static_cast<DWORD>((wcslen(value) + 1) * sizeof(wchar_t));
    LSTATUS status = RegSetValueExW(
        key,
        valueName,
        0,
        REG_SZ,
        reinterpret_cast<const BYTE*>(value),
        byteCount);
    if (status != ERROR_SUCCESS)
    {
        PrintLastError(L"RegSetValueExW", status);
        return false;
    }

    return true;
}

bool
SetRegistryDwordValue(
    _In_ HKEY key,
    _In_ const wchar_t* valueName,
    _In_ DWORD value
    )
{
    LSTATUS status = RegSetValueExW(
        key,
        valueName,
        0,
        REG_DWORD,
        reinterpret_cast<const BYTE*>(&value),
        sizeof(value));
    if (status != ERROR_SUCCESS)
    {
        PrintLastError(L"RegSetValueExW", status);
        return false;
    }

    return true;
}

bool
ConfigureMinifilterInstance()
{
    bool result = false;
    HKEY serviceKey = nullptr;
    HKEY instancesKey = nullptr;
    HKEY instanceKey = nullptr;

    do
    {
        std::wstring servicePath = L"SYSTEM\\CurrentControlSet\\Services\\";
        servicePath += {{FUNCTION_PREFIX}}Contract::ServiceName;
        LSTATUS status = RegCreateKeyExW(
            HKEY_LOCAL_MACHINE,
            servicePath.c_str(),
            0,
            nullptr,
            REG_OPTION_NON_VOLATILE,
            KEY_SET_VALUE | KEY_CREATE_SUB_KEY,
            nullptr,
            &serviceKey,
            nullptr);
        if (status != ERROR_SUCCESS)
        {
            PrintLastError(L"RegCreateKeyExW Services", status);
            break;
        }

        status = RegCreateKeyExW(
            serviceKey,
            L"Instances",
            0,
            nullptr,
            REG_OPTION_NON_VOLATILE,
            KEY_SET_VALUE | KEY_CREATE_SUB_KEY,
            nullptr,
            &instancesKey,
            nullptr);
        if (status != ERROR_SUCCESS)
        {
            PrintLastError(L"RegCreateKeyExW Instances", status);
            break;
        }

        if (!SetRegistryStringValue(instancesKey, L"DefaultInstance", L"{{DRIVER_NAME}} Instance"))
        {
            break;
        }

        status = RegCreateKeyExW(
            instancesKey,
            L"{{DRIVER_NAME}} Instance",
            0,
            nullptr,
            REG_OPTION_NON_VOLATILE,
            KEY_SET_VALUE,
            nullptr,
            &instanceKey,
            nullptr);
        if (status != ERROR_SUCCESS)
        {
            PrintLastError(L"RegCreateKeyExW Instance", status);
            break;
        }

        if (!SetRegistryStringValue(instanceKey, L"Altitude", L"370050"))
        {
            break;
        }

        if (!SetRegistryDwordValue(instanceKey, L"Flags", 0))
        {
            break;
        }

        result = true;

    } while (false);

    if (instanceKey != nullptr)
    {
        RegCloseKey(instanceKey);
        instanceKey = nullptr;
    }

    if (instancesKey != nullptr)
    {
        RegCloseKey(instancesKey);
        instancesKey = nullptr;
    }

    if (serviceKey != nullptr)
    {
        RegCloseKey(serviceKey);
        serviceKey = nullptr;
    }

    return result;
}

struct PortMessage
{
    FILTER_MESSAGE_HEADER Header;
    {{FUNCTION_PREFIX}}Contract::AccessQuestion Question;
};

bool
RegisterBlockedPath()
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

        {{FUNCTION_PREFIX}}Contract::PathRule rule = {};
        StringCchCopyW(rule.Path, ARRAYSIZE(rule.Path), L"\\blocked-by-{{DRIVER_NAME}}");
        DWORD bytesReturned = 0;

        if (!DeviceIoControl(
                device,
                {{FUNCTION_PREFIX}}Contract::IoctlRegisterPath,
                &rule,
                sizeof(rule),
                nullptr,
                0,
                &bytesReturned,
                nullptr))
        {
            PrintLastError(L"IoctlRegisterPath", GetLastError());
            break;
        }

        std::wcout << L"Registered blocked file path substring: " << rule.Path << std::endl;
        result = true;

    } while (false);

    if (device != INVALID_HANDLE_VALUE)
    {
        CloseHandle(device);
        device = INVALID_HANDLE_VALUE;
    }

    return result;
}

bool
RunMinifilterDecisionPort()
{
    bool result = false;
    HANDLE port = INVALID_HANDLE_VALUE;
    HANDLE iocp = nullptr;
    OVERLAPPED overlapped = {};
    PortMessage message = {};

    do
    {
        HRESULT hr = FilterConnectCommunicationPort(
            {{FUNCTION_PREFIX}}Contract::PortName,
            0,
            nullptr,
            0,
            nullptr,
            &port);
        if (FAILED(hr))
        {
            std::wcerr << L"FilterConnectCommunicationPort failed: 0x" << std::hex << hr << std::dec << std::endl;
            break;
        }

        iocp = CreateIoCompletionPort(port, nullptr, 0, 1);
        if (iocp == nullptr)
        {
            PrintLastError(L"CreateIoCompletionPort", GetLastError());
            break;
        }

        overlapped.hEvent = CreateEventW(nullptr, TRUE, FALSE, nullptr);
        if (overlapped.hEvent == nullptr)
        {
            PrintLastError(L"CreateEventW", GetLastError());
            break;
        }

        hr = FilterGetMessage(port, &message.Header, sizeof(message), &overlapped);
        if (hr != HRESULT_FROM_WIN32(ERROR_IO_PENDING) && FAILED(hr))
        {
            std::wcerr << L"FilterGetMessage failed: 0x" << std::hex << hr << std::dec << std::endl;
            break;
        }

        std::wcout << L"Minifilter IOCP decision worker is armed. Try opening a path containing \\blocked-by-{{DRIVER_NAME}}." << std::endl;

        DWORD bytesTransferred = 0;
        ULONG_PTR completionKey = 0;
        LPOVERLAPPED completed = nullptr;
        if (GetQueuedCompletionStatus(iocp, &bytesTransferred, &completionKey, &completed, 15000) && completed == &overlapped)
        {
            struct Reply
            {
                FILTER_REPLY_HEADER Header;
                {{FUNCTION_PREFIX}}Contract::AccessDecision Decision;
            };

            Reply reply = {};
            reply.Header.Status = 0;
            reply.Header.MessageId = message.Header.MessageId;
            reply.Decision.Allow = message.Question.ProcessId == GetCurrentProcessId() ? 1u : 0u;

            hr = FilterReplyMessage(port, &reply.Header, sizeof(reply));
            if (FAILED(hr))
            {
                std::wcerr << L"FilterReplyMessage failed: 0x" << std::hex << hr << std::dec << std::endl;
                break;
            }

            std::wcout << L"Decision sent for PID " << message.Question.ProcessId << L": " << (reply.Decision.Allow ? L"allow" : L"block") << std::endl;
        }
        result = true;

    } while (false);

    if (overlapped.hEvent != nullptr)
    {
        CloseHandle(overlapped.hEvent);
        overlapped.hEvent = nullptr;
    }

    if (iocp != nullptr)
    {
        CloseHandle(iocp);
        iocp = nullptr;
    }

    if (port != INVALID_HANDLE_VALUE)
    {
        CloseHandle(port);
        port = INVALID_HANDLE_VALUE;
    }

    return result;
}
`
		replacement = `        if (!SendPingIoctl())
        {
            break;
        }

        if (!RegisterBlockedPath())
        {
            break;
        }

        if (!RunMinifilterDecisionPort())
        {
            break;
        }`
	case "registryfilter":
		helper = `

bool
RegisterRegistryPath()
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

        {{FUNCTION_PREFIX}}Contract::RegistryRule rule = {};
        StringCchCopyW(rule.Path, ARRAYSIZE(rule.Path), L"\\Registry\\Machine\\Software\\{{DRIVER_NAME}}Blocked");
        DWORD bytesReturned = 0;

        if (!DeviceIoControl(device, {{FUNCTION_PREFIX}}Contract::IoctlRegisterRegistryPath, &rule, sizeof(rule), nullptr, 0, &bytesReturned, nullptr))
        {
            PrintLastError(L"IoctlRegisterRegistryPath", GetLastError());
            break;
        }

        std::wcout << L"Registered blocked registry path: " << rule.Path << std::endl;
        result = true;

    } while (false);

    if (device != INVALID_HANDLE_VALUE)
    {
        CloseHandle(device);
        device = INVALID_HANDLE_VALUE;
    }

    return result;
}
`
		replacement = `        if (!SendPingIoctl())
        {
            break;
        }

        if (!RegisterRegistryPath())
        {
            break;
        }`
	case "wfpcallout":
		helper = `

bool
RegisterNetworkRule()
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

        {{FUNCTION_PREFIX}}Contract::NetworkRule rule = {};
        rule.Ipv4AddressNetworkOrder = 0x7F000001;
        rule.PortNetworkOrder = 0;
        StringCchCopyW(rule.Target, ARRAYSIZE(rule.Target), L"127.0.0.1");
        DWORD bytesReturned = 0;

        if (!DeviceIoControl(device, {{FUNCTION_PREFIX}}Contract::IoctlRegisterNetworkRule, &rule, sizeof(rule), nullptr, 0, &bytesReturned, nullptr))
        {
            PrintLastError(L"IoctlRegisterNetworkRule", GetLastError());
            break;
        }

        std::wcout << L"Registered outbound block target: " << rule.Target << std::endl;
        result = true;

    } while (false);

    if (device != INVALID_HANDLE_VALUE)
    {
        CloseHandle(device);
        device = INVALID_HANDLE_VALUE;
    }

    return result;
}
`
		replacement = `        if (!SendPingIoctl())
        {
            break;
        }

        if (!RegisterNetworkRule())
        {
            break;
        }`
	}
	if helper != "" {
		content = strings.Replace(content, helperMarker, helper+helperMarker, 1)
	}
	return strings.Replace(content, marker, replacement, 1)
}

func renderCreateDriverPOCTemplate(template string, spec createDriverPOCSpec) string {
	replacer := strings.NewReplacer(
		"{{DRIVER_NAME}}", spec.DriverName,
		"{{FUNCTION_PREFIX}}", spec.FunctionPrefix,
		"{{UPPER_NAME}}", spec.UpperName,
		"{{POC_TYPE}}", spec.POCTypeLabel,
		"{{DRIVER_TYPE}}", spec.DriverType,
		"{{DRIVER_LIBRARIES}}", spec.DriverLibraries,
		"{{TESTER_LIBRARIES}}", spec.TesterLibraries,
		"{{SERVICE_TYPE}}", spec.ServiceType,
		"\n{{MINIFILTER_INSTANCE_CONFIG_CALL}}\n", createDriverPOCMinifilterConfigCall(spec),
		"{{MINIFILTER_INSTANCE_CONFIG_CALL}}", strings.Trim(createDriverPOCMinifilterConfigCall(spec), "\n"),
		"{{SOLUTION_GUID}}", spec.SolutionGUID,
		"{{DRIVER_GUID}}", spec.DriverGUID,
		"{{TESTER_GUID}}", spec.TesterGUID,
		"{{DEVICE_CLASS_GUID}}", spec.DeviceClassGUID,
		"{{DEVICE_CLASS_C_GUID}}", spec.DeviceClassCGUID,
	)
	return replacer.Replace(template)
}

func createDriverPOCMinifilterConfigCall(spec createDriverPOCSpec) string {
	if spec.POCType != "minifilter" {
		return ""
	}
	return `
        if (!ConfigureMinifilterInstance())
        {
            break;
        }
`
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
        DriverObject->DeviceObject = nullptr;
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

    } while (false);

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
    <DriverType>{{DRIVER_TYPE}}</DriverType>
    <DriverTargetPlatform>Desktop</DriverTargetPlatform>
    <PlatformToolset>WindowsKernelModeDriver10.0</PlatformToolset>
    <UseDebugLibraries>true</UseDebugLibraries>
    <CharacterSet>Unicode</CharacterSet>
  </PropertyGroup>
  <PropertyGroup Condition="'$(Configuration)|$(Platform)'=='Release|x64'" Label="Configuration">
    <TargetVersion>Windows10</TargetVersion>
    <ConfigurationType>Driver</ConfigurationType>
    <DriverType>{{DRIVER_TYPE}}</DriverType>
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
      <AdditionalDependencies>{{DRIVER_LIBRARIES}}</AdditionalDependencies>
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
      <AdditionalDependencies>{{DRIVER_LIBRARIES}}</AdditionalDependencies>
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

    } while (false);

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

    } while (false);

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
            {{SERVICE_TYPE}},
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
                {{SERVICE_TYPE}},
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

    } while (false);

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

    } while (false);

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

    } while (false);

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

    } while (false);

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

{{MINIFILTER_INSTANCE_CONFIG_CALL}}

        if (!StartDriverService(service))
        {
            break;
        }

        if (!SendPingIoctl())
        {
            break;
        }

        exitCode = 0;

    } while (false);

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
    <Link>
      <AdditionalDependencies>{{TESTER_LIBRARIES}}</AdditionalDependencies>
    </Link>
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
    <Link>
      <AdditionalDependencies>{{TESTER_LIBRARIES}}</AdditionalDependencies>
    </Link>
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

const createDriverPOCObjectFilterIoctlHeaderTemplate = `
#pragma once

#ifndef CTL_CODE
#error Include ntddk.h or winioctl.h before including Ioctl.h.
#endif

namespace {{FUNCTION_PREFIX}}Contract
{
inline constexpr wchar_t ServiceName[] = L"{{DRIVER_NAME}}";
inline constexpr wchar_t DisplayName[] = L"{{DRIVER_NAME}} Object Filter POC Driver";
inline constexpr wchar_t DriverFileName[] = L"{{DRIVER_NAME}}.sys";
inline constexpr wchar_t NtDeviceName[] = L"\\Device\\{{DRIVER_NAME}}";
inline constexpr wchar_t DosDeviceName[] = L"\\DosDevices\\{{DRIVER_NAME}}";
inline constexpr wchar_t Win32DeviceName[] = L"\\\\.\\{{DRIVER_NAME}}";
inline constexpr ULONG DeviceType = 0x8000;
inline constexpr ULONG IoctlPing = CTL_CODE(DeviceType, 0x800, METHOD_BUFFERED, FILE_ANY_ACCESS);
inline constexpr ULONG IoctlSetProtectedIds = CTL_CODE(DeviceType, 0x801, METHOD_BUFFERED, FILE_WRITE_DATA);

struct ProtectedIds
{
    ULONG ProcessId;
    ULONG ThreadId;
};
}
`

const createDriverPOCObjectFilterDriverSourceTemplate = `
#include "Driver.h"

namespace
{
constexpr wchar_t DriverSddl[] = L"D:P(A;;GA;;;SY)(A;;GA;;;BA)";
constexpr GUID DeviceClassGuid = {{DEVICE_CLASS_C_GUID}};
constexpr CHAR PingReply[] = "objectfilter pong from {{DRIVER_NAME}}";
PVOID RegistrationHandle = nullptr;
volatile ULONG ProtectedProcessId = 0;
volatile ULONG ProtectedThreadId = 0;

ACCESS_MASK
StripProcessAccess(
    _In_ ACCESS_MASK desiredAccess
    )
{
    constexpr ACCESS_MASK DangerousProcessAccess =
        PROCESS_CREATE_THREAD |
        PROCESS_DUP_HANDLE |
        PROCESS_SET_INFORMATION |
        PROCESS_SET_QUOTA |
        PROCESS_SUSPEND_RESUME |
        PROCESS_TERMINATE |
        PROCESS_VM_OPERATION |
        PROCESS_VM_WRITE;

    return desiredAccess & ~DangerousProcessAccess;
}

ACCESS_MASK
StripThreadAccess(
    _In_ ACCESS_MASK desiredAccess
    )
{
    constexpr ACCESS_MASK DangerousThreadAccess =
        THREAD_DIRECT_IMPERSONATION |
        THREAD_IMPERSONATE |
        THREAD_SET_CONTEXT |
        THREAD_SET_INFORMATION |
        THREAD_SET_LIMITED_INFORMATION |
        THREAD_SUSPEND_RESUME |
        THREAD_TERMINATE;

    return desiredAccess & ~DangerousThreadAccess;
}

OB_PREOP_CALLBACK_STATUS
ObjectPreOperationCallback(
    _In_ PVOID RegistrationContext,
    _Inout_ POB_PRE_OPERATION_INFORMATION OperationInformation
    )
{
    UNREFERENCED_PARAMETER(RegistrationContext);

    if (OperationInformation->KernelHandle)
    {
        return OB_PREOP_SUCCESS;
    }

    if (OperationInformation->ObjectType == *PsProcessType)
    {
        HANDLE processId = PsGetProcessId(reinterpret_cast<PEPROCESS>(OperationInformation->Object));
        if (HandleToULong(processId) == ProtectedProcessId)
        {
            if (OperationInformation->Operation == OB_OPERATION_HANDLE_CREATE)
            {
                OperationInformation->Parameters->CreateHandleInformation.DesiredAccess =
                    StripProcessAccess(OperationInformation->Parameters->CreateHandleInformation.DesiredAccess);
            }
            else
            {
                if (OperationInformation->Operation == OB_OPERATION_HANDLE_DUPLICATE)
                {
                    OperationInformation->Parameters->DuplicateHandleInformation.DesiredAccess =
                        StripProcessAccess(OperationInformation->Parameters->DuplicateHandleInformation.DesiredAccess);
                }
            }
        }
    }
    else
    {
        if (OperationInformation->ObjectType == *PsThreadType)
        {
            HANDLE threadId = PsGetThreadId(reinterpret_cast<PETHREAD>(OperationInformation->Object));
            if (HandleToULong(threadId) == ProtectedThreadId)
            {
                if (OperationInformation->Operation == OB_OPERATION_HANDLE_CREATE)
                {
                    OperationInformation->Parameters->CreateHandleInformation.DesiredAccess =
                        StripThreadAccess(OperationInformation->Parameters->CreateHandleInformation.DesiredAccess);
                }
                else
                {
                    if (OperationInformation->Operation == OB_OPERATION_HANDLE_DUPLICATE)
                    {
                        OperationInformation->Parameters->DuplicateHandleInformation.DesiredAccess =
                            StripThreadAccess(OperationInformation->Parameters->DuplicateHandleInformation.DesiredAccess);
                    }
                }
            }
        }
    }

    return OB_PREOP_SUCCESS;
}
}

VOID
{{FUNCTION_PREFIX}}Unload(
    _In_ PDRIVER_OBJECT DriverObject
    )
{
    UNICODE_STRING symbolicLinkName = {};

    if (RegistrationHandle != nullptr)
    {
        ObUnRegisterCallbacks(RegistrationHandle);
        RegistrationHandle = nullptr;
    }

    RtlInitUnicodeString(&symbolicLinkName, {{FUNCTION_PREFIX}}Contract::DosDeviceName);
    IoDeleteSymbolicLink(&symbolicLinkName);

    if (DriverObject->DeviceObject != nullptr)
    {
        IoDeleteDevice(DriverObject->DeviceObject);
        DriverObject->DeviceObject = nullptr;
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
    PIO_STACK_LOCATION stack = IoGetCurrentIrpStackLocation(Irp);
    NTSTATUS status = STATUS_INVALID_DEVICE_REQUEST;
    ULONG_PTR information = 0;

    UNREFERENCED_PARAMETER(DeviceObject);

    switch (stack->Parameters.DeviceIoControl.IoControlCode)
    {
    case {{FUNCTION_PREFIX}}Contract::IoctlPing:
    {
        if (Irp->AssociatedIrp.SystemBuffer == NULL || stack->Parameters.DeviceIoControl.OutputBufferLength < sizeof(PingReply))
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
    case {{FUNCTION_PREFIX}}Contract::IoctlSetProtectedIds:
    {
        if (Irp->AssociatedIrp.SystemBuffer == NULL || stack->Parameters.DeviceIoControl.InputBufferLength < sizeof({{FUNCTION_PREFIX}}Contract::ProtectedIds))
        {
            status = STATUS_INVALID_PARAMETER;
            break;
        }

        auto request = static_cast<{{FUNCTION_PREFIX}}Contract::ProtectedIds*>(Irp->AssociatedIrp.SystemBuffer);
        InterlockedExchange(reinterpret_cast<volatile LONG*>(&ProtectedProcessId), static_cast<LONG>(request->ProcessId));
        InterlockedExchange(reinterpret_cast<volatile LONG*>(&ProtectedThreadId), static_cast<LONG>(request->ThreadId));
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
    PDEVICE_OBJECT deviceObject = nullptr;
    UNICODE_STRING deviceName = {};
    UNICODE_STRING symbolicLinkName = {};
    UNICODE_STRING sddl = {};

    UNREFERENCED_PARAMETER(RegistryPath);

    do
    {
        OB_OPERATION_REGISTRATION operations[2] = {};
        OB_CALLBACK_REGISTRATION registration = {};
        UNICODE_STRING altitude = {};

        RtlInitUnicodeString(&deviceName, {{FUNCTION_PREFIX}}Contract::NtDeviceName);
        RtlInitUnicodeString(&symbolicLinkName, {{FUNCTION_PREFIX}}Contract::DosDeviceName);
        RtlInitUnicodeString(&sddl, DriverSddl);

        status = IoCreateDeviceSecure(DriverObject, 0, &deviceName, {{FUNCTION_PREFIX}}Contract::DeviceType, FILE_DEVICE_SECURE_OPEN, FALSE, &sddl, &DeviceClassGuid, &deviceObject);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        status = IoCreateSymbolicLink(&symbolicLinkName, &deviceName);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        RtlInitUnicodeString(&altitude, L"370030");
        operations[0].ObjectType = PsProcessType;
        operations[0].Operations = OB_OPERATION_HANDLE_CREATE | OB_OPERATION_HANDLE_DUPLICATE;
        operations[0].PreOperation = ObjectPreOperationCallback;
        operations[1].ObjectType = PsThreadType;
        operations[1].Operations = OB_OPERATION_HANDLE_CREATE | OB_OPERATION_HANDLE_DUPLICATE;
        operations[1].PreOperation = ObjectPreOperationCallback;
        registration.Version = OB_FLT_REGISTRATION_VERSION;
        registration.OperationRegistrationCount = RTL_NUMBER_OF(operations);
        registration.Altitude = altitude;
        registration.OperationRegistration = operations;

        status = ObRegisterCallbacks(&registration, &RegistrationHandle);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        DriverObject->MajorFunction[IRP_MJ_CREATE] = {{FUNCTION_PREFIX}}CreateClose;
        DriverObject->MajorFunction[IRP_MJ_CLOSE] = {{FUNCTION_PREFIX}}CreateClose;
        DriverObject->MajorFunction[IRP_MJ_DEVICE_CONTROL] = {{FUNCTION_PREFIX}}DeviceControl;
        DriverObject->DriverUnload = {{FUNCTION_PREFIX}}Unload;
        deviceObject->Flags &= ~DO_DEVICE_INITIALIZING;

    } while (false);

    if (!NT_SUCCESS(status))
    {
        {{FUNCTION_PREFIX}}Unload(DriverObject);
    }

    return status;
}
`

const createDriverPOCObjectFilterTesterSourceTemplate = createDriverPOCTesterSourceTemplate + `
`

const createDriverPOCObjectFilterReadmeTemplate = `
# {{DRIVER_NAME}} Object Filter Driver POC

Generated by Kernforge /create-driver-poc {{DRIVER_NAME}} --type objectfilter.

This POC registers ObRegisterCallbacks for process and thread handle create/duplicate operations. The tester loads the driver through SCM and sends {{FUNCTION_PREFIX}}Contract::IoctlSetProtectedIds for its own process id and thread id. The driver strips dangerous requested access such as PROCESS_VM_WRITE, PROCESS_TERMINATE, PROCESS_SUSPEND_RESUME, THREAD_SUSPEND_RESUME, THREAD_SET_CONTEXT, and THREAD_TERMINATE for the protected objects.

Build with:

    msbuild "{{DRIVER_NAME}}.sln" /p:Configuration=Debug /p:Platform=x64

Run the tester as Administrator from bin\Debug\x64. Unsigned x64 drivers require test-signing or an equivalent lab policy.
`

const createDriverPOCMinifilterIoctlHeaderTemplate = `
#pragma once

#ifndef CTL_CODE
#error Include ntddk.h or winioctl.h before including Ioctl.h.
#endif

namespace {{FUNCTION_PREFIX}}Contract
{
inline constexpr wchar_t ServiceName[] = L"{{DRIVER_NAME}}";
inline constexpr wchar_t DisplayName[] = L"{{DRIVER_NAME}} Minifilter POC Driver";
inline constexpr wchar_t DriverFileName[] = L"{{DRIVER_NAME}}.sys";
inline constexpr wchar_t NtDeviceName[] = L"\\Device\\{{DRIVER_NAME}}";
inline constexpr wchar_t DosDeviceName[] = L"\\DosDevices\\{{DRIVER_NAME}}";
inline constexpr wchar_t Win32DeviceName[] = L"\\\\.\\{{DRIVER_NAME}}";
inline constexpr wchar_t PortName[] = L"\\{{DRIVER_NAME}}Port";
inline constexpr ULONG DeviceType = 0x8000;
inline constexpr ULONG IoctlPing = CTL_CODE(DeviceType, 0x800, METHOD_BUFFERED, FILE_ANY_ACCESS);
inline constexpr ULONG IoctlRegisterPath = CTL_CODE(DeviceType, 0x810, METHOD_BUFFERED, FILE_WRITE_DATA);
inline constexpr ULONG MaxPathChars = 512;

struct PathRule
{
    wchar_t Path[MaxPathChars];
};

struct AccessQuestion
{
    ULONG ProcessId;
    wchar_t Path[MaxPathChars];
};

struct AccessDecision
{
    ULONG Allow;
};
}
`

const createDriverPOCMinifilterDriverSourceTemplate = `
#include <fltKernel.h>
#include <ntstrsafe.h>
#include "Driver.h"

namespace
{
constexpr wchar_t DriverSddl[] = L"D:P(A;;GA;;;SY)(A;;GA;;;BA)";
constexpr GUID DeviceClassGuid = {{DEVICE_CLASS_C_GUID}};
PFLT_FILTER FilterHandle = nullptr;
PFLT_PORT ServerPort = nullptr;
PFLT_PORT ClientPort = nullptr;
wchar_t BlockedPath[{{FUNCTION_PREFIX}}Contract::MaxPathChars] = {};

void
CopyPathToQuestion(
    _Out_ wchar_t* Destination,
    _In_reads_(SourceChars) const wchar_t* Source,
    _In_ size_t SourceChars
    )
{
    size_t chars = min(SourceChars, {{FUNCTION_PREFIX}}Contract::MaxPathChars - 1);

    RtlZeroMemory(Destination, {{FUNCTION_PREFIX}}Contract::MaxPathChars * sizeof(wchar_t));
    if (Source != nullptr && chars > 0)
    {
        RtlCopyMemory(Destination, Source, chars * sizeof(wchar_t));
    }
    Destination[chars] = L'\0';
}

bool
PathMatchesBlockedRule(
    _In_reads_(PathChars) const wchar_t* Path,
    _In_ size_t PathChars
    )
{
    bool matched = false;
    wchar_t localPath[{{FUNCTION_PREFIX}}Contract::MaxPathChars] = {};

    do
    {
        if (BlockedPath[0] == L'\0' || Path == nullptr || PathChars == 0)
        {
            break;
        }

        CopyPathToQuestion(localPath, Path, PathChars);
        matched = (wcsstr(localPath, BlockedPath) != nullptr);

    } while (false);

    return matched;
}

bool
AskUserModeToAllow(
    _In_z_ const wchar_t* Path
    )
{
    bool allow = true;
    {{FUNCTION_PREFIX}}Contract::AccessQuestion question = {};
    {{FUNCTION_PREFIX}}Contract::AccessDecision decision = {};
    ULONG replyLength = sizeof(decision);
    NTSTATUS status = STATUS_SUCCESS;
    LARGE_INTEGER timeout = {};

    do
    {
        if (FilterHandle == nullptr || ClientPort == nullptr || Path == nullptr)
        {
            break;
        }

        CopyPathToQuestion(question.Path, Path, wcslen(Path));
        question.ProcessId = HandleToULong(PsGetCurrentProcessId());

        timeout.QuadPart = -10 * 1000 * 1000;
        status = FltSendMessage(FilterHandle, &ClientPort, &question, sizeof(question), &decision, &replyLength, &timeout);
        if (NT_SUCCESS(status) && decision.Allow == 0)
        {
            allow = false;
        }

    } while (false);

    return allow;
}

bool
ShouldInspectPreCreate(
    _In_ PFLT_CALLBACK_DATA Data,
    _In_ PCFLT_RELATED_OBJECTS FltObjects
    )
{
    bool inspect = false;

    do
    {
        if (Data == nullptr || FltObjects == nullptr || FltObjects->FileObject == nullptr)
        {
            break;
        }

        if (BlockedPath[0] == L'\0' || ClientPort == nullptr)
        {
            break;
        }

        if (FLT_IS_FASTIO_OPERATION(Data))
        {
            break;
        }

        if (IoGetTopLevelIrp() != nullptr)
        {
            break;
        }

        if (FlagOn(Data->Iopb->OperationFlags, SL_OPEN_TARGET_DIRECTORY))
        {
            break;
        }

        if (FlagOn(Data->Iopb->Parameters.Create.Options, FILE_OPEN_BY_FILE_ID))
        {
            break;
        }

        if (FlagOn(Data->Iopb->Parameters.Create.Options, FILE_OPEN_REPARSE_POINT))
        {
            break;
        }

        if (FlagOn(FltObjects->FileObject->Flags, FO_VOLUME_OPEN))
        {
            break;
        }

        inspect = true;

    } while (false);

    return inspect;
}

bool
IsDeleteDispositionRequest(
    _In_ PFLT_CALLBACK_DATA Data
    )
{
    bool deleting = false;

    do
    {
        if (Data == nullptr || Data->Iopb->Parameters.SetFileInformation.InfoBuffer == nullptr)
        {
            break;
        }

        switch (Data->Iopb->Parameters.SetFileInformation.FileInformationClass)
        {
        case FileDispositionInformation:
        {
            auto dispositionInfo =
                static_cast<PFILE_DISPOSITION_INFORMATION>(Data->Iopb->Parameters.SetFileInformation.InfoBuffer);
            deleting = (dispositionInfo->DeleteFile != FALSE);
            break;
        }
        case FileDispositionInformationEx:
        {
            auto dispositionInfo =
                static_cast<PFILE_DISPOSITION_INFORMATION_EX>(Data->Iopb->Parameters.SetFileInformation.InfoBuffer);
            deleting = FlagOn(dispositionInfo->Flags, FILE_DISPOSITION_DELETE);
            break;
        }
        default:
            break;
        }

    } while (false);

    return deleting;
}

bool
ShouldInspectSetInformation(
    _In_ PFLT_CALLBACK_DATA Data,
    _In_ PCFLT_RELATED_OBJECTS FltObjects
    )
{
    bool inspect = false;

    do
    {
        if (Data == nullptr || FltObjects == nullptr || FltObjects->FileObject == nullptr)
        {
            break;
        }

        if (BlockedPath[0] == L'\0' || ClientPort == nullptr)
        {
            break;
        }

        if (FLT_IS_FASTIO_OPERATION(Data))
        {
            break;
        }

        if (IoGetTopLevelIrp() != nullptr)
        {
            break;
        }

        if (FlagOn(FltObjects->FileObject->Flags, FO_VOLUME_OPEN))
        {
            break;
        }

        switch (Data->Iopb->Parameters.SetFileInformation.FileInformationClass)
        {
        case FileRenameInformation:
        case FileLinkInformation:
            inspect = true;
            break;
        case FileDispositionInformation:
        case FileDispositionInformationEx:
            inspect = IsDeleteDispositionRequest(Data);
            break;
        default:
            break;
        }

    } while (false);

    return inspect;
}

FLT_PREOP_CALLBACK_STATUS
PreCreate(
    _Inout_ PFLT_CALLBACK_DATA Data,
    _In_ PCFLT_RELATED_OBJECTS FltObjects,
    _Outptr_result_maybenull_ PVOID* CompletionContext
    )
{
    UNREFERENCED_PARAMETER(CompletionContext);

    if (ShouldInspectPreCreate(Data, FltObjects))
    {
        NTSTATUS status = STATUS_SUCCESS;
        PFLT_FILE_NAME_INFORMATION nameInfo = nullptr;
        wchar_t questionPath[{{FUNCTION_PREFIX}}Contract::MaxPathChars] = {};

        status = FltGetFileNameInformation(Data, FLT_FILE_NAME_NORMALIZED | FLT_FILE_NAME_QUERY_DEFAULT, &nameInfo);
        if (NT_SUCCESS(status))
        {
            status = FltParseFileNameInformation(nameInfo);
        }

        if (NT_SUCCESS(status) && nameInfo->Name.Buffer != nullptr)
        {
            if (PathMatchesBlockedRule(nameInfo->Name.Buffer, nameInfo->Name.Length / sizeof(wchar_t)))
            {
                CopyPathToQuestion(questionPath, nameInfo->Name.Buffer, nameInfo->Name.Length / sizeof(wchar_t));
                if (!AskUserModeToAllow(questionPath))
                {
                    Data->IoStatus.Status = STATUS_ACCESS_DENIED;
                    Data->IoStatus.Information = 0;
                    FltReleaseFileNameInformation(nameInfo);
                    nameInfo = nullptr;
                    return FLT_PREOP_COMPLETE;
                }
            }
        }

        if (nameInfo != nullptr)
        {
            FltReleaseFileNameInformation(nameInfo);
            nameInfo = nullptr;
        }
    }

    return FLT_PREOP_SUCCESS_NO_CALLBACK;
}

FLT_PREOP_CALLBACK_STATUS
PreSetInformation(
    _Inout_ PFLT_CALLBACK_DATA Data,
    _In_ PCFLT_RELATED_OBJECTS FltObjects,
    _Outptr_result_maybenull_ PVOID* CompletionContext
    )
{
    UNREFERENCED_PARAMETER(CompletionContext);

    if (ShouldInspectSetInformation(Data, FltObjects))
    {
        NTSTATUS status = STATUS_SUCCESS;
        PFLT_FILE_NAME_INFORMATION nameInfo = nullptr;
        wchar_t questionPath[{{FUNCTION_PREFIX}}Contract::MaxPathChars] = {};
        bool matched = false;

        status = FltGetFileNameInformation(Data, FLT_FILE_NAME_NORMALIZED | FLT_FILE_NAME_QUERY_DEFAULT, &nameInfo);
        if (NT_SUCCESS(status))
        {
            status = FltParseFileNameInformation(nameInfo);
        }

        if (NT_SUCCESS(status) && nameInfo->Name.Buffer != nullptr)
        {
            matched = PathMatchesBlockedRule(nameInfo->Name.Buffer, nameInfo->Name.Length / sizeof(wchar_t));
            if (matched)
            {
                CopyPathToQuestion(questionPath, nameInfo->Name.Buffer, nameInfo->Name.Length / sizeof(wchar_t));
            }
        }

        if (!matched && Data->Iopb->Parameters.SetFileInformation.FileInformationClass == FileRenameInformation)
        {
            PFILE_RENAME_INFORMATION renameInfo =
                static_cast<PFILE_RENAME_INFORMATION>(Data->Iopb->Parameters.SetFileInformation.InfoBuffer);

            if (renameInfo != nullptr && renameInfo->FileNameLength > 0)
            {
                matched = PathMatchesBlockedRule(renameInfo->FileName, renameInfo->FileNameLength / sizeof(wchar_t));
                if (matched)
                {
                    CopyPathToQuestion(questionPath, renameInfo->FileName, renameInfo->FileNameLength / sizeof(wchar_t));
                }
            }
        }

        if (matched && !AskUserModeToAllow(questionPath))
        {
            Data->IoStatus.Status = STATUS_ACCESS_DENIED;
            Data->IoStatus.Information = 0;
            if (nameInfo != nullptr)
            {
                FltReleaseFileNameInformation(nameInfo);
                nameInfo = nullptr;
            }
            return FLT_PREOP_COMPLETE;
        }

        if (nameInfo != nullptr)
        {
            FltReleaseFileNameInformation(nameInfo);
            nameInfo = nullptr;
        }
    }

    return FLT_PREOP_SUCCESS_NO_CALLBACK;
}

NTSTATUS
PortConnect(
    _In_ PFLT_PORT ClientPortHandle,
    _In_opt_ PVOID ServerPortCookie,
    _In_reads_bytes_opt_(SizeOfContext) PVOID ConnectionContext,
    _In_ ULONG SizeOfContext,
    _Outptr_result_maybenull_ PVOID* ConnectionCookie
    )
{
    UNREFERENCED_PARAMETER(ServerPortCookie);
    UNREFERENCED_PARAMETER(ConnectionContext);
    UNREFERENCED_PARAMETER(SizeOfContext);
    UNREFERENCED_PARAMETER(ConnectionCookie);
    ClientPort = ClientPortHandle;
    return STATUS_SUCCESS;
}

VOID
PortDisconnect(
    _In_opt_ PVOID ConnectionCookie
    )
{
    UNREFERENCED_PARAMETER(ConnectionCookie);
    FltCloseClientPort(FilterHandle, &ClientPort);
}

NTSTATUS
InstanceSetup(
    _In_ PCFLT_RELATED_OBJECTS FltObjects,
    _In_ FLT_INSTANCE_SETUP_FLAGS Flags,
    _In_ DEVICE_TYPE VolumeDeviceType,
    _In_ FLT_FILESYSTEM_TYPE VolumeFilesystemType
    )
{
    UNREFERENCED_PARAMETER(FltObjects);
    UNREFERENCED_PARAMETER(Flags);

    switch (VolumeDeviceType)
    {
    case FILE_DEVICE_DISK_FILE_SYSTEM:
    case FILE_DEVICE_NETWORK_FILE_SYSTEM:
        break;
    default:
        return STATUS_FLT_DO_NOT_ATTACH;
    }

    switch (VolumeFilesystemType)
    {
    case FLT_FSTYPE_NTFS:
    case FLT_FSTYPE_REFS:
    case FLT_FSTYPE_FAT:
    case FLT_FSTYPE_EXFAT:
    case FLT_FSTYPE_NETWORK:
        return STATUS_SUCCESS;
    default:
        return STATUS_FLT_DO_NOT_ATTACH;
    }
}

NTSTATUS
FilterUnload(
    _In_ FLT_FILTER_UNLOAD_FLAGS Flags
    )
{
    UNREFERENCED_PARAMETER(Flags);
    if (ServerPort != nullptr)
    {
        FltCloseCommunicationPort(ServerPort);
        ServerPort = nullptr;
    }

    if (FilterHandle != nullptr)
    {
        FltUnregisterFilter(FilterHandle);
        FilterHandle = nullptr;
    }
    return STATUS_SUCCESS;
}

CONST FLT_OPERATION_REGISTRATION Operations[] =
{
    { IRP_MJ_CREATE, 0, PreCreate, nullptr },
    { IRP_MJ_SET_INFORMATION, 0, PreSetInformation, nullptr },
    { IRP_MJ_OPERATION_END }
};

CONST FLT_REGISTRATION Registration =
{
    sizeof(FLT_REGISTRATION),
    FLT_REGISTRATION_VERSION,
    0,
    nullptr,
    Operations,
    FilterUnload,
    InstanceSetup,
    nullptr,
    nullptr,
    nullptr,
    nullptr,
    nullptr,
    nullptr,
    nullptr,
    nullptr
};
}

VOID
{{FUNCTION_PREFIX}}Unload(
    _In_ PDRIVER_OBJECT DriverObject
    )
{
    UNICODE_STRING symbolicLinkName = {};

    (void)FilterUnload(0);

    RtlInitUnicodeString(&symbolicLinkName, {{FUNCTION_PREFIX}}Contract::DosDeviceName);
    IoDeleteSymbolicLink(&symbolicLinkName);

    if (DriverObject->DeviceObject != nullptr)
    {
        IoDeleteDevice(DriverObject->DeviceObject);
        DriverObject->DeviceObject = nullptr;
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
    IoCompleteRequest(Irp, IO_NO_INCREMENT);
    return STATUS_SUCCESS;
}

NTSTATUS
{{FUNCTION_PREFIX}}DeviceControl(
    _In_ PDEVICE_OBJECT DeviceObject,
    _Inout_ PIRP Irp
    )
{
    PIO_STACK_LOCATION stack = IoGetCurrentIrpStackLocation(Irp);
    NTSTATUS status = STATUS_INVALID_DEVICE_REQUEST;

    UNREFERENCED_PARAMETER(DeviceObject);

    switch (stack->Parameters.DeviceIoControl.IoControlCode)
    {
    case {{FUNCTION_PREFIX}}Contract::IoctlRegisterPath:
    {
        if (Irp->AssociatedIrp.SystemBuffer == NULL ||
            stack->Parameters.DeviceIoControl.InputBufferLength < sizeof({{FUNCTION_PREFIX}}Contract::PathRule))
        {
            status = STATUS_INVALID_PARAMETER;
            break;
        }

        auto rule = static_cast<{{FUNCTION_PREFIX}}Contract::PathRule*>(Irp->AssociatedIrp.SystemBuffer);
        status = RtlStringCchCopyW(BlockedPath, RTL_NUMBER_OF(BlockedPath), rule->Path);
        break;
    }
    default:
    {
        status = STATUS_INVALID_DEVICE_REQUEST;
        break;
    }
    }

    Irp->IoStatus.Status = status;
    Irp->IoStatus.Information = 0;
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
    NTSTATUS status = STATUS_SUCCESS;
    PDEVICE_OBJECT deviceObject = nullptr;
    UNICODE_STRING deviceName = {};
    UNICODE_STRING symbolicLinkName = {};
    UNICODE_STRING portName = {};
    UNICODE_STRING sddl = {};
    PSECURITY_DESCRIPTOR securityDescriptor = nullptr;
    OBJECT_ATTRIBUTES objectAttributes = {};

    UNREFERENCED_PARAMETER(RegistryPath);

    do
    {
        RtlInitUnicodeString(&deviceName, {{FUNCTION_PREFIX}}Contract::NtDeviceName);
        RtlInitUnicodeString(&symbolicLinkName, {{FUNCTION_PREFIX}}Contract::DosDeviceName);
        RtlInitUnicodeString(&sddl, DriverSddl);

        status = IoCreateDeviceSecure(DriverObject, 0, &deviceName, {{FUNCTION_PREFIX}}Contract::DeviceType, FILE_DEVICE_SECURE_OPEN, FALSE, &sddl, &DeviceClassGuid, &deviceObject);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        status = IoCreateSymbolicLink(&symbolicLinkName, &deviceName);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        status = FltRegisterFilter(DriverObject, &Registration, &FilterHandle);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        status = FltBuildDefaultSecurityDescriptor(&securityDescriptor, FLT_PORT_ALL_ACCESS);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        RtlInitUnicodeString(&portName, {{FUNCTION_PREFIX}}Contract::PortName);
        InitializeObjectAttributes(&objectAttributes, &portName, OBJ_KERNEL_HANDLE | OBJ_CASE_INSENSITIVE, nullptr, securityDescriptor);
        status = FltCreateCommunicationPort(FilterHandle, &ServerPort, &objectAttributes, nullptr, PortConnect, PortDisconnect, nullptr, 1);
        FltFreeSecurityDescriptor(securityDescriptor);
        securityDescriptor = nullptr;
        if (!NT_SUCCESS(status))
        {
            break;
        }

        status = FltStartFiltering(FilterHandle);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        DriverObject->MajorFunction[IRP_MJ_CREATE] = {{FUNCTION_PREFIX}}CreateClose;
        DriverObject->MajorFunction[IRP_MJ_CLOSE] = {{FUNCTION_PREFIX}}CreateClose;
        DriverObject->MajorFunction[IRP_MJ_DEVICE_CONTROL] = {{FUNCTION_PREFIX}}DeviceControl;
        DriverObject->DriverUnload = {{FUNCTION_PREFIX}}Unload;
        deviceObject->Flags &= ~DO_DEVICE_INITIALIZING;

    } while (false);

    if (!NT_SUCCESS(status))
    {
        (void)FilterUnload(0);
    }

    return status;
}
`

const createDriverPOCMinifilterTesterSourceTemplate = createDriverPOCTesterSourceTemplate + `
`

const createDriverPOCMinifilterReadmeTemplate = `
# {{DRIVER_NAME}} Minifilter Driver POC

Generated by Kernforge /create-driver-poc {{DRIVER_NAME}} --type minifilter.

This POC creates a filesystem minifilter skeleton with IRP_MJ_CREATE open inspection, IRP_MJ_SET_INFORMATION rename/move/delete inspection, an InstanceSetup attach policy, a Filter Manager communication port, FltSendMessage user-mode decision flow, and {{FUNCTION_PREFIX}}Contract::IoctlRegisterPath for a protected file path rule. The tester links FltLib.lib, connects with FilterConnectCommunicationPort, arms FilterGetMessage on an IOCP, and replies with FilterReplyMessage so its own process can be allowed while other opens, renames, and deletes are denied.

Build with:

    msbuild "{{DRIVER_NAME}}.sln" /p:Configuration=Debug /p:Platform=x64
`

const createDriverPOCRegistryFilterIoctlHeaderTemplate = `
#pragma once

#ifndef CTL_CODE
#error Include ntddk.h or winioctl.h before including Ioctl.h.
#endif

namespace {{FUNCTION_PREFIX}}Contract
{
inline constexpr wchar_t ServiceName[] = L"{{DRIVER_NAME}}";
inline constexpr wchar_t DisplayName[] = L"{{DRIVER_NAME}} Registry Filter POC Driver";
inline constexpr wchar_t DriverFileName[] = L"{{DRIVER_NAME}}.sys";
inline constexpr wchar_t NtDeviceName[] = L"\\Device\\{{DRIVER_NAME}}";
inline constexpr wchar_t DosDeviceName[] = L"\\DosDevices\\{{DRIVER_NAME}}";
inline constexpr wchar_t Win32DeviceName[] = L"\\\\.\\{{DRIVER_NAME}}";
inline constexpr ULONG DeviceType = 0x8000;
inline constexpr ULONG IoctlPing = CTL_CODE(DeviceType, 0x800, METHOD_BUFFERED, FILE_ANY_ACCESS);
inline constexpr ULONG IoctlRegisterRegistryPath = CTL_CODE(DeviceType, 0x820, METHOD_BUFFERED, FILE_WRITE_DATA);
inline constexpr ULONG MaxRegistryPathChars = 512;

struct RegistryRule
{
    wchar_t Path[MaxRegistryPathChars];
};
}
`

const createDriverPOCRegistryFilterDriverSourceTemplate = `
#include <ntstrsafe.h>
#include "Driver.h"

namespace
{
constexpr wchar_t DriverSddl[] = L"D:P(A;;GA;;;SY)(A;;GA;;;BA)";
constexpr GUID DeviceClassGuid = {{DEVICE_CLASS_C_GUID}};
LARGE_INTEGER CallbackCookie = {};
wchar_t ProtectedRegistryPath[{{FUNCTION_PREFIX}}Contract::MaxRegistryPathChars] = {};

bool
RegistryPathMatchesRule(
    _In_opt_ PCUNICODE_STRING Path
    )
{
    bool matched = false;
    wchar_t localPath[{{FUNCTION_PREFIX}}Contract::MaxRegistryPathChars] = {};

    do
    {
        if (ProtectedRegistryPath[0] == L'\0' || Path == nullptr || Path->Buffer == nullptr || Path->Length == 0)
        {
            break;
        }

        size_t chars = min(Path->Length / sizeof(wchar_t), {{FUNCTION_PREFIX}}Contract::MaxRegistryPathChars - 1);
        RtlCopyMemory(localPath, Path->Buffer, chars * sizeof(wchar_t));
        localPath[chars] = L'\0';
        matched = (wcsstr(localPath, ProtectedRegistryPath) != nullptr);

    } while (false);

    return matched;
}

bool
ShouldBlockRegistryObject(
    _In_opt_ PVOID Object
    )
{
    bool block = false;
    PUNICODE_STRING objectName = nullptr;

    do
    {
        if (ProtectedRegistryPath[0] == L'\0' || Object == nullptr)
        {
            break;
        }

        NTSTATUS status = CmCallbackGetKeyObjectIDEx(&CallbackCookie, Object, nullptr, &objectName, 0);
        if (!NT_SUCCESS(status) || objectName == nullptr || objectName->Buffer == nullptr)
        {
            break;
        }

        if (RegistryPathMatchesRule(objectName))
        {
            block = true;
        }

    } while (false);

    if (objectName != nullptr)
    {
        CmCallbackReleaseKeyObjectIDEx(objectName);
        objectName = nullptr;
    }

    return block;
}

NTSTATUS
RegistryCallback(
    _In_ PVOID CallbackContext,
    _In_opt_ PVOID Argument1,
    _In_opt_ PVOID Argument2
    )
{
    UNREFERENCED_PARAMETER(CallbackContext);

    REG_NOTIFY_CLASS notifyClass = static_cast<REG_NOTIFY_CLASS>(reinterpret_cast<ULONG_PTR>(Argument1));

    switch (notifyClass)
    {
    case RegNtPreCreateKeyEx:
    {
        auto info = static_cast<PREG_CREATE_KEY_INFORMATION>(Argument2);
        if (info != nullptr && RegistryPathMatchesRule(info->CompleteName))
        {
            return STATUS_ACCESS_DENIED;
        }
        break;
    }
    case RegNtPreOpenKeyEx:
    {
        auto info = static_cast<PREG_OPEN_KEY_INFORMATION>(Argument2);
        if (info != nullptr && RegistryPathMatchesRule(info->CompleteName))
        {
            return STATUS_ACCESS_DENIED;
        }
        break;
    }
    case RegNtPreSetValueKey:
    {
        auto info = static_cast<PREG_SET_VALUE_KEY_INFORMATION>(Argument2);
        if (info != nullptr && ShouldBlockRegistryObject(info->Object))
        {
            return STATUS_ACCESS_DENIED;
        }
        break;
    }
    case RegNtPreDeleteValueKey:
    {
        auto info = static_cast<PREG_DELETE_VALUE_KEY_INFORMATION>(Argument2);
        if (info != nullptr && ShouldBlockRegistryObject(info->Object))
        {
            return STATUS_ACCESS_DENIED;
        }
        break;
    }
    case RegNtPreDeleteKey:
    {
        auto info = static_cast<PREG_DELETE_KEY_INFORMATION>(Argument2);
        if (info != nullptr && ShouldBlockRegistryObject(info->Object))
        {
            return STATUS_ACCESS_DENIED;
        }
        break;
    }
    case RegNtPreRenameKey:
    {
        auto info = static_cast<PREG_RENAME_KEY_INFORMATION>(Argument2);
        if (info != nullptr && (ShouldBlockRegistryObject(info->Object) || RegistryPathMatchesRule(info->NewName)))
        {
            return STATUS_ACCESS_DENIED;
        }
        break;
    }
    default:
    {
        break;
    }
    }

    return STATUS_SUCCESS;
}
}

VOID
{{FUNCTION_PREFIX}}Unload(
    _In_ PDRIVER_OBJECT DriverObject
    )
{
    UNICODE_STRING symbolicLinkName = {};

    if (CallbackCookie.QuadPart != 0)
    {
        CmUnRegisterCallback(CallbackCookie);
        CallbackCookie.QuadPart = 0;
    }

    RtlInitUnicodeString(&symbolicLinkName, {{FUNCTION_PREFIX}}Contract::DosDeviceName);
    IoDeleteSymbolicLink(&symbolicLinkName);

    if (DriverObject->DeviceObject != nullptr)
    {
        IoDeleteDevice(DriverObject->DeviceObject);
        DriverObject->DeviceObject = nullptr;
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
    PIO_STACK_LOCATION stack = IoGetCurrentIrpStackLocation(Irp);
    NTSTATUS status = STATUS_INVALID_DEVICE_REQUEST;

    UNREFERENCED_PARAMETER(DeviceObject);

    switch (stack->Parameters.DeviceIoControl.IoControlCode)
    {
    case {{FUNCTION_PREFIX}}Contract::IoctlRegisterRegistryPath:
    {
        if (Irp->AssociatedIrp.SystemBuffer == NULL ||
            stack->Parameters.DeviceIoControl.InputBufferLength < sizeof({{FUNCTION_PREFIX}}Contract::RegistryRule))
        {
            status = STATUS_INVALID_PARAMETER;
            break;
        }

        auto rule = static_cast<{{FUNCTION_PREFIX}}Contract::RegistryRule*>(Irp->AssociatedIrp.SystemBuffer);
        status = RtlStringCchCopyW(ProtectedRegistryPath, RTL_NUMBER_OF(ProtectedRegistryPath), rule->Path);
        break;
    }
    default:
    {
        status = STATUS_INVALID_DEVICE_REQUEST;
        break;
    }
    }

    Irp->IoStatus.Status = status;
    Irp->IoStatus.Information = 0;
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
    PDEVICE_OBJECT deviceObject = nullptr;
    UNICODE_STRING deviceName = {};
    UNICODE_STRING symbolicLinkName = {};
    UNICODE_STRING sddl = {};
    UNICODE_STRING altitude = {};

    UNREFERENCED_PARAMETER(RegistryPath);

    do
    {
        RtlInitUnicodeString(&deviceName, {{FUNCTION_PREFIX}}Contract::NtDeviceName);
        RtlInitUnicodeString(&symbolicLinkName, {{FUNCTION_PREFIX}}Contract::DosDeviceName);
        RtlInitUnicodeString(&sddl, DriverSddl);
        RtlInitUnicodeString(&altitude, L"370040");

        status = IoCreateDeviceSecure(DriverObject, 0, &deviceName, {{FUNCTION_PREFIX}}Contract::DeviceType, FILE_DEVICE_SECURE_OPEN, FALSE, &sddl, &DeviceClassGuid, &deviceObject);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        status = IoCreateSymbolicLink(&symbolicLinkName, &deviceName);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        status = CmRegisterCallbackEx(RegistryCallback, &altitude, DriverObject, nullptr, &CallbackCookie, nullptr);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        DriverObject->MajorFunction[IRP_MJ_CREATE] = {{FUNCTION_PREFIX}}CreateClose;
        DriverObject->MajorFunction[IRP_MJ_CLOSE] = {{FUNCTION_PREFIX}}CreateClose;
        DriverObject->MajorFunction[IRP_MJ_DEVICE_CONTROL] = {{FUNCTION_PREFIX}}DeviceControl;
        DriverObject->DriverUnload = {{FUNCTION_PREFIX}}Unload;
        deviceObject->Flags &= ~DO_DEVICE_INITIALIZING;

    } while (false);

    if (!NT_SUCCESS(status))
    {
        {{FUNCTION_PREFIX}}Unload(DriverObject);
    }

    return status;
}
`

const createDriverPOCRegistryFilterTesterSourceTemplate = createDriverPOCTesterSourceTemplate

const createDriverPOCRegistryFilterReadmeTemplate = `
# {{DRIVER_NAME}} Registry Filter Driver POC

Generated by Kernforge /create-driver-poc {{DRIVER_NAME}} --type registryfilter.

This variant implements a CmRegisterCallbackEx-based registry filter POC. The tester sends {{FUNCTION_PREFIX}}Contract::IoctlRegisterRegistryPath with a RegistryRule, and Driver.cpp compares pre-create, pre-open, pre-set-value, pre-delete-value, pre-delete-key, and pre-rename-key callback names against the registered path before returning STATUS_ACCESS_DENIED for protected keys.
`

const createDriverPOCWfpCalloutIoctlHeaderTemplate = `
#pragma once

#ifndef CTL_CODE
#error Include ntddk.h or winioctl.h before including Ioctl.h.
#endif

namespace {{FUNCTION_PREFIX}}Contract
{
inline constexpr wchar_t ServiceName[] = L"{{DRIVER_NAME}}";
inline constexpr wchar_t DisplayName[] = L"{{DRIVER_NAME}} WFP Callout POC Driver";
inline constexpr wchar_t DriverFileName[] = L"{{DRIVER_NAME}}.sys";
inline constexpr wchar_t NtDeviceName[] = L"\\Device\\{{DRIVER_NAME}}";
inline constexpr wchar_t DosDeviceName[] = L"\\DosDevices\\{{DRIVER_NAME}}";
inline constexpr wchar_t Win32DeviceName[] = L"\\\\.\\{{DRIVER_NAME}}";
inline constexpr ULONG DeviceType = 0x8000;
inline constexpr ULONG IoctlPing = CTL_CODE(DeviceType, 0x800, METHOD_BUFFERED, FILE_ANY_ACCESS);
inline constexpr ULONG IoctlRegisterNetworkRule = CTL_CODE(DeviceType, 0x830, METHOD_BUFFERED, FILE_WRITE_DATA);
inline constexpr ULONG MaxTargetChars = 256;

struct NetworkRule
{
    wchar_t Target[MaxTargetChars];
    ULONG Ipv4AddressNetworkOrder;
    USHORT PortNetworkOrder;
};
}
`

const createDriverPOCWfpCalloutDriverSourceTemplate = `
#include <fwpsk.h>
#include <fwpmk.h>
#include "Driver.h"

namespace
{
constexpr wchar_t DriverSddl[] = L"D:P(A;;GA;;;SY)(A;;GA;;;BA)";
constexpr GUID DeviceClassGuid = {{DEVICE_CLASS_C_GUID}};
constexpr GUID CalloutKey = {{DEVICE_CLASS_C_GUID}};
HANDLE EngineHandle = nullptr;
UINT32 CalloutId = 0;
ULONG BlockedIpv4Address = 0;
USHORT BlockedPort = 0;

void NTAPI
ClassifyOutbound(
    _In_ const FWPS_INCOMING_VALUES0* FixedValues,
    _In_ const FWPS_INCOMING_METADATA_VALUES0* MetaValues,
    _Inout_opt_ void* LayerData,
    _In_opt_ const void* ClassifyContext,
    _In_ const FWPS_FILTER0* Filter,
    _In_ UINT64 FlowContext,
    _Inout_ FWPS_CLASSIFY_OUT0* ClassifyOut
    )
{
    UNREFERENCED_PARAMETER(MetaValues);
    UNREFERENCED_PARAMETER(LayerData);
    UNREFERENCED_PARAMETER(ClassifyContext);
    UNREFERENCED_PARAMETER(Filter);
    UNREFERENCED_PARAMETER(FlowContext);

    UINT32 remoteAddress = FixedValues->incomingValue[FWPS_FIELD_ALE_AUTH_CONNECT_V4_IP_REMOTE_ADDRESS].value.uint32;
    UINT16 remotePort = FixedValues->incomingValue[FWPS_FIELD_ALE_AUTH_CONNECT_V4_IP_REMOTE_PORT].value.uint16;

    if (BlockedIpv4Address != 0 && remoteAddress == BlockedIpv4Address && (BlockedPort == 0 || remotePort == BlockedPort))
    {
        ClassifyOut->actionType = FWP_ACTION_BLOCK;
        ClassifyOut->rights &= ~FWPS_RIGHT_ACTION_WRITE;
    }
    else
    {
        ClassifyOut->actionType = FWP_ACTION_PERMIT;
    }
}

NTSTATUS NTAPI
NotifyCallout(
    _In_ FWPS_CALLOUT_NOTIFY_TYPE NotifyType,
    _In_ const GUID* FilterKey,
    _Inout_ FWPS_FILTER0* Filter
    )
{
    UNREFERENCED_PARAMETER(NotifyType);
    UNREFERENCED_PARAMETER(FilterKey);
    UNREFERENCED_PARAMETER(Filter);
    return STATUS_SUCCESS;
}

VOID NTAPI
FlowDelete(
    _In_ UINT16 LayerId,
    _In_ UINT32 CalloutIdValue,
    _In_ UINT64 FlowContext
    )
{
    UNREFERENCED_PARAMETER(LayerId);
    UNREFERENCED_PARAMETER(CalloutIdValue);
    UNREFERENCED_PARAMETER(FlowContext);
}

NTSTATUS
RegisterWfpObjects(
    _In_ PDEVICE_OBJECT DeviceObject
    )
{
    NTSTATUS status = STATUS_SUCCESS;
    FWPS_CALLOUT0 callout = {};
    FWPM_CALLOUT0 managementCallout = {};
    FWPM_FILTER0 filter = {};
    FWPM_FILTER_CONDITION0 condition = {};
    FWPM_SESSION0 session = {};

    do
    {
        session.flags = FWPM_SESSION_FLAG_DYNAMIC;
        status = FwpmEngineOpen0(nullptr, RPC_C_AUTHN_WINNT, nullptr, &session, &EngineHandle);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        callout.calloutKey = CalloutKey;
        callout.classifyFn = ClassifyOutbound;
        callout.notifyFn = NotifyCallout;
        callout.flowDeleteFn = FlowDelete;
        status = FwpsCalloutRegister0(DeviceObject, &callout, &CalloutId);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        managementCallout.calloutKey = CalloutKey;
        managementCallout.displayData.name = const_cast<wchar_t*>(L"{{DRIVER_NAME}} outbound block callout");
        managementCallout.applicableLayer = FWPM_LAYER_ALE_AUTH_CONNECT_V4;
        status = FwpmCalloutAdd0(EngineHandle, &managementCallout, nullptr, nullptr);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        condition.fieldKey = FWPM_CONDITION_IP_REMOTE_ADDRESS;
        condition.matchType = FWP_MATCH_GREATER_OR_EQUAL;
        condition.conditionValue.type = FWP_UINT32;
        condition.conditionValue.uint32 = 0;

        filter.displayData.name = const_cast<wchar_t*>(L"{{DRIVER_NAME}} outbound block filter");
        filter.layerKey = FWPM_LAYER_ALE_AUTH_CONNECT_V4;
        filter.action.type = FWP_ACTION_CALLOUT_TERMINATING;
        filter.action.calloutKey = CalloutKey;
        filter.subLayerKey = FWPM_SUBLAYER_UNIVERSAL;
        filter.weight.type = FWP_EMPTY;
        filter.numFilterConditions = 1;
        filter.filterCondition = &condition;
        status = FwpmFilterAdd0(EngineHandle, &filter, nullptr, nullptr);

    } while (false);

    return status;
}

VOID
UnregisterWfpObjects()
{
    if (CalloutId != 0)
    {
        FwpsCalloutUnregisterById0(CalloutId);
        CalloutId = 0;
    }

    if (EngineHandle != nullptr)
    {
        FwpmEngineClose0(EngineHandle);
        EngineHandle = nullptr;
    }
}
}

VOID
{{FUNCTION_PREFIX}}Unload(
    _In_ PDRIVER_OBJECT DriverObject
    )
{
    UNICODE_STRING symbolicLinkName = {};

    UnregisterWfpObjects();
    RtlInitUnicodeString(&symbolicLinkName, {{FUNCTION_PREFIX}}Contract::DosDeviceName);
    IoDeleteSymbolicLink(&symbolicLinkName);

    if (DriverObject->DeviceObject != nullptr)
    {
        IoDeleteDevice(DriverObject->DeviceObject);
        DriverObject->DeviceObject = nullptr;
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
    PIO_STACK_LOCATION stack = IoGetCurrentIrpStackLocation(Irp);
    NTSTATUS status = STATUS_INVALID_DEVICE_REQUEST;

    UNREFERENCED_PARAMETER(DeviceObject);

    switch (stack->Parameters.DeviceIoControl.IoControlCode)
    {
    case {{FUNCTION_PREFIX}}Contract::IoctlRegisterNetworkRule:
    {
        if (Irp->AssociatedIrp.SystemBuffer == NULL ||
            stack->Parameters.DeviceIoControl.InputBufferLength < sizeof({{FUNCTION_PREFIX}}Contract::NetworkRule))
        {
            status = STATUS_INVALID_PARAMETER;
            break;
        }

        auto rule = static_cast<{{FUNCTION_PREFIX}}Contract::NetworkRule*>(Irp->AssociatedIrp.SystemBuffer);
        BlockedIpv4Address = rule->Ipv4AddressNetworkOrder;
        BlockedPort = rule->PortNetworkOrder;
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
    Irp->IoStatus.Information = 0;
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
    PDEVICE_OBJECT deviceObject = nullptr;
    UNICODE_STRING deviceName = {};
    UNICODE_STRING symbolicLinkName = {};
    UNICODE_STRING sddl = {};

    UNREFERENCED_PARAMETER(RegistryPath);

    do
    {
        RtlInitUnicodeString(&deviceName, {{FUNCTION_PREFIX}}Contract::NtDeviceName);
        RtlInitUnicodeString(&symbolicLinkName, {{FUNCTION_PREFIX}}Contract::DosDeviceName);
        RtlInitUnicodeString(&sddl, DriverSddl);

        status = IoCreateDeviceSecure(DriverObject, 0, &deviceName, {{FUNCTION_PREFIX}}Contract::DeviceType, FILE_DEVICE_SECURE_OPEN, FALSE, &sddl, &DeviceClassGuid, &deviceObject);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        status = IoCreateSymbolicLink(&symbolicLinkName, &deviceName);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        status = RegisterWfpObjects(deviceObject);
        if (!NT_SUCCESS(status))
        {
            break;
        }

        DriverObject->MajorFunction[IRP_MJ_CREATE] = {{FUNCTION_PREFIX}}CreateClose;
        DriverObject->MajorFunction[IRP_MJ_CLOSE] = {{FUNCTION_PREFIX}}CreateClose;
        DriverObject->MajorFunction[IRP_MJ_DEVICE_CONTROL] = {{FUNCTION_PREFIX}}DeviceControl;
        DriverObject->DriverUnload = {{FUNCTION_PREFIX}}Unload;
        deviceObject->Flags &= ~DO_DEVICE_INITIALIZING;

    } while (false);

    if (!NT_SUCCESS(status))
    {
        {{FUNCTION_PREFIX}}Unload(DriverObject);
    }

    return status;
}
`

const createDriverPOCWfpCalloutTesterSourceTemplate = createDriverPOCTesterSourceTemplate

const createDriverPOCWfpCalloutReadmeTemplate = `
# {{DRIVER_NAME}} WFP Callout Driver POC

Generated by Kernforge /create-driver-poc {{DRIVER_NAME}} --type wfpcallout.

This variant implements a WFP outbound callout POC and links Fwpkclnt.lib. The tester sends {{FUNCTION_PREFIX}}Contract::IoctlRegisterNetworkRule with a NetworkRule, and Driver.cpp registers FwpsCalloutRegister/FwpmEngineOpen/FwpmFilterAdd plumbing with a classifyFn that blocks outbound IPv4 traffic matching the registered target.
`
