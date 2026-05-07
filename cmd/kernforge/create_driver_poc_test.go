package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateDriverPOCGeneratesDriverAndTesterSolution(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	rt := &runtimeState{
		cfg:       DefaultConfig(root),
		workspace: Workspace{Root: root, BaseRoot: root},
		writer:    &output,
		ui:        UI{},
	}

	if err := rt.handleCreateDriverPOCCommand("AcmePoc"); err != nil {
		t.Fatalf("handleCreateDriverPOCCommand: %v", err)
	}

	projectRoot := filepath.Join(root, "AcmePoc")
	expectedFiles := []string{
		"AcmePoc.sln",
		"shared/Ioctl.h",
		"AcmePoc/Driver.h",
		"AcmePoc/Driver.cpp",
		"AcmePoc/AcmePoc.vcxproj",
		"AcmePoc/AcmePoc.vcxproj.filters",
		"AcmePoc-tester/main.cpp",
		"AcmePoc-tester/AcmePoc-tester.vcxproj",
		"AcmePoc-tester/AcmePoc-tester.vcxproj.filters",
		"README.md",
	}
	for _, rel := range expectedFiles {
		if _, err := os.Stat(filepath.Join(projectRoot, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("expected generated file %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "AcmePoc", "AcmePoc.inf")); !os.IsNotExist(err) {
		t.Fatalf("expected INF not to be generated, stat err=%v", err)
	}

	solution := readCreateDriverPOCTestFile(t, projectRoot, "AcmePoc.sln")
	if strings.Contains(solution, "%!") {
		t.Fatalf("solution contains malformed fmt output: %s", solution)
	}
	for _, needle := range []string{
		`AcmePoc\AcmePoc.vcxproj`,
		`AcmePoc-tester\AcmePoc-tester.vcxproj`,
		"Debug|x64",
		"Release|x64",
	} {
		if !strings.Contains(solution, needle) {
			t.Fatalf("solution missing %q", needle)
		}
	}
	if strings.Contains(solution, "Win32") {
		t.Fatalf("solution should be x64-only, got Win32 in solution")
	}

	driverProject := readCreateDriverPOCTestFile(t, projectRoot, "AcmePoc/AcmePoc.vcxproj")
	for _, needle := range []string{
		"<ConfigurationType>Driver</ConfigurationType>",
		"<DriverType>WDM</DriverType>",
		"<TargetVersion>Windows10</TargetVersion>",
		"<PlatformToolset>WindowsKernelModeDriver10.0</PlatformToolset>",
		"<LanguageStandard>stdcpp20</LanguageStandard>",
		"<TargetName>AcmePoc</TargetName>",
		"<TargetExt>.sys</TargetExt>",
		"Wdmsec.lib",
		`<ClCompile Include="Driver.cpp" />`,
	} {
		if !strings.Contains(driverProject, needle) {
			t.Fatalf("driver project missing %q", needle)
		}
	}
	for _, forbidden := range []string{`Include="Debug|Win32"`, `Include="Release|Win32"`, "<Platform>Win32</Platform>"} {
		if strings.Contains(driverProject, forbidden) {
			t.Fatalf("driver project should be x64-only, got %q in project", forbidden)
		}
	}

	driverSource := readCreateDriverPOCTestFile(t, projectRoot, "AcmePoc/Driver.cpp")
	if strings.Contains(driverSource, "static ") {
		t.Fatalf("driver source should use namespace/constexpr style instead of static linkage")
	}
	for _, needle := range []string{
		"namespace",
		"constexpr GUID DeviceClassGuid",
		"constexpr CHAR PingReply[]",
		"extern \"C\"",
		"IoCreateDeviceSecure",
		"AcmePocUnload",
		"AcmePocCreateClose",
		"AcmePocDeviceControl",
		"AcmePocContract::DeviceType",
		"AcmePocContract::NtDeviceName",
		"AcmePocContract::DosDeviceName",
		"AcmePocContract::IoctlPing",
		"STATUS_BUFFER_TOO_SMALL",
		`"pong from AcmePoc"`,
	} {
		if !strings.Contains(driverSource, needle) {
			t.Fatalf("driver source missing %q", needle)
		}
	}
	for _, forbidden := range []string{
		"FILE_DEVICE_UNKNOWN",
		"} while (FALSE);",
	} {
		if strings.Contains(driverSource, forbidden) {
			t.Fatalf("driver source should not include %q", forbidden)
		}
	}

	testerProject := readCreateDriverPOCTestFile(t, projectRoot, "AcmePoc-tester/AcmePoc-tester.vcxproj")
	for _, needle := range []string{
		"<ConfigurationType>Application</ConfigurationType>",
		"<PlatformToolset>v143</PlatformToolset>",
		"<LanguageStandard>stdcpp20</LanguageStandard>",
		"<RuntimeLibrary>MultiThreaded</RuntimeLibrary>",
		"<TargetName>AcmePoc-tester</TargetName>",
		`..\AcmePoc\AcmePoc.vcxproj`,
	} {
		if !strings.Contains(testerProject, needle) {
			t.Fatalf("tester project missing %q", needle)
		}
	}
	for _, forbidden := range []string{`Include="Debug|Win32"`, `Include="Release|Win32"`, "<Platform>Win32</Platform>"} {
		if strings.Contains(testerProject, forbidden) {
			t.Fatalf("tester project should be x64-only, got %q in project", forbidden)
		}
	}

	testerSource := readCreateDriverPOCTestFile(t, projectRoot, "AcmePoc-tester/main.cpp")
	if strings.Contains(testerSource, "static ") {
		t.Fatalf("tester source should use namespace/constexpr style instead of static linkage")
	}
	if strings.Contains(testerSource, "std::wstring executableDirectory;\n    std::wstring driverPath;") {
		t.Fatalf("wmain should declare do-while locals inside the do block when they are not used during cleanup")
	}
	for _, needle := range []string{
		"OpenSCManagerW",
		"CreateServiceW",
		"StartServiceW",
		"StopDriverService",
		"WaitForServiceState",
		"if (!StopDriverService(service))",
		"Service is already running; restarting it to load the current driver image.",
		"MaxPathBufferChars",
		"path.assign(capacity, L'\\0')",
		"CreateFileW",
		"DeviceIoControl",
		"StopAndDeleteDriverService",
		"namespace",
		"constexpr DWORD StopPollCount",
		"AcmePocContract::ServiceName",
		"AcmePocContract::Win32DeviceName",
		"AcmePocContract::DriverFileName",
		"AcmePocContract::IoctlPing",
		"device = INVALID_HANDLE_VALUE;",
		"service = nullptr;",
		"scm = nullptr;",
	} {
		if !strings.Contains(testerSource, needle) {
			t.Fatalf("tester source missing %q", needle)
		}
	}
	for _, forbidden := range []string{
		"std::array<wchar_t, MAX_PATH>",
		"} while (false);",
	} {
		if strings.Contains(testerSource, forbidden) {
			t.Fatalf("tester source should not include %q", forbidden)
		}
	}

	header := readCreateDriverPOCTestFile(t, projectRoot, "shared/Ioctl.h")
	for _, needle := range []string{
		"namespace AcmePocContract",
		"inline constexpr wchar_t ServiceName[]",
		"inline constexpr wchar_t NtDeviceName[]",
		"inline constexpr wchar_t DosDeviceName[]",
		"inline constexpr wchar_t Win32DeviceName[]",
		"inline constexpr wchar_t DriverFileName[]",
		"inline constexpr ULONG IoctlPing",
	} {
		if !strings.Contains(header, needle) {
			t.Fatalf("shared header missing %q", needle)
		}
	}
	readme := readCreateDriverPOCTestFile(t, projectRoot, "README.md")
	for _, needle := range []string{
		"DeviceType",
		"fixed MAX_PATH",
		"already running so the current driver image is tested",
	} {
		if !strings.Contains(readme, needle) {
			t.Fatalf("generated README missing %q", needle)
		}
	}
	if !strings.Contains(output.String(), "AcmePoc-tester.exe") {
		t.Fatalf("command output did not mention tester binary: %s", output.String())
	}
}

func TestCreateDriverPOCRejectsInvalidNames(t *testing.T) {
	cases := []string{
		"",
		"bad-name",
		"1Bad",
		"Bad.Name",
		strings.Repeat("A", 65),
		"Two Names",
	}

	for _, tc := range cases {
		if _, err := parseCreateDriverPOCSpec(tc); err == nil {
			t.Fatalf("expected invalid driver name %q to fail", tc)
		}
	}
}

func TestCreateDriverPOCFunctionPrefixStartsUppercase(t *testing.T) {
	spec, err := parseCreateDriverPOCSpec("kndriver")
	if err != nil {
		t.Fatalf("parseCreateDriverPOCSpec: %v", err)
	}
	if spec.FunctionPrefix != "Kndriver" {
		t.Fatalf("unexpected function prefix: %q", spec.FunctionPrefix)
	}
	source := renderCreateDriverPOCTemplate(createDriverPOCDriverSourceTemplate, spec)
	for _, needle := range []string{
		"KndriverUnload",
		"KndriverCreateClose",
		"KndriverDeviceControl",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("expected source to include %q", needle)
		}
	}
	if strings.Contains(source, "kndriverUnload") {
		t.Fatalf("source should not use lowercase-start generated function names")
	}
}

func TestCreateDriverPOCRefusesNonEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	projectRoot := filepath.Join(root, "ExistingPoc")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "keep.txt"), []byte("user file"), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	rt := &runtimeState{
		cfg:       DefaultConfig(root),
		workspace: Workspace{Root: root, BaseRoot: root},
		writer:    &bytes.Buffer{},
		ui:        UI{},
	}
	err := rt.handleCreateDriverPOCCommand("ExistingPoc")
	if err == nil {
		t.Fatalf("expected existing non-empty target to fail")
	}
	if !strings.Contains(err.Error(), "already exists and is not empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func readCreateDriverPOCTestFile(t *testing.T, root string, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}
