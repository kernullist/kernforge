package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectKernforgeDoctorReportIncludesCoreChecks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := t.TempDir()
	if err := os.MkdirAll(userConfigDir(), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	config := `{
  "provider": "openai",
  "model": "gpt-test",
  "api_key": "super-secret-key",
  "mcp_servers": [
    {
      "name": "docs",
      "command": "node",
      "args": ["server.js"],
      "env_vars": [
        {
          "name": "DOCS_TOKEN"
        }
      ]
    }
  ]
}`
	if err := os.WriteFile(userConfigPath(), []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	report := collectKernforgeDoctorReport(workspace, kernforgeDoctorOptions{})
	for _, id := range []string{
		"runtime.provenance",
		"environment.workspace",
		"configuration.config",
		"configuration.auth",
		"configuration.mcp",
		"runtime.tool_registry",
		"background.daemon",
	} {
		if _, ok := report.Checks[id]; !ok {
			t.Fatalf("expected doctor check %q, got %#v", id, report.Checks)
		}
	}
	if got := report.Checks["configuration.config"].Status; got != kernforgeDoctorStatusOK {
		t.Fatalf("expected config check ok, got %q", got)
	}
	if got := report.Checks["configuration.mcp"].Status; got != kernforgeDoctorStatusWarn {
		t.Fatalf("expected MCP missing env warning, got %q", got)
	}
	if got := report.Checks["runtime.tool_registry"].Status; got != kernforgeDoctorStatusOK {
		t.Fatalf("expected tool registry check ok, got %q", got)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if strings.Contains(string(data), "super-secret-key") {
		t.Fatalf("doctor report leaked raw API key: %s", data)
	}
}

func TestCollectKernforgeDoctorReportCapturesConfigLoadFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := t.TempDir()
	if err := os.MkdirAll(userConfigDir(), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(userConfigPath(), []byte(`{"provider":`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	report := collectKernforgeDoctorReport(workspace, kernforgeDoctorOptions{StrictConfig: true})
	check := report.Checks["configuration.config"]
	if check.Status != kernforgeDoctorStatusFail {
		t.Fatalf("expected config load failure to be reported, got %#v", check)
	}
	if !strings.Contains(check.Details["error"], "unexpected end") {
		t.Fatalf("expected parse error details, got %#v", check.Details)
	}
}

func TestCollectKernforgeDoctorReportDoesNotCreateUserConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := t.TempDir()

	report := collectKernforgeDoctorReport(workspace, kernforgeDoctorOptions{})
	if _, ok := report.Checks["configuration.config"]; !ok {
		t.Fatalf("expected config check, got %#v", report.Checks)
	}
	if _, err := os.Stat(userConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("doctor must not create user config, stat err=%v", err)
	}
}

func TestRunKernforgeDoctorCommandJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := t.TempDir()
	var output bytes.Buffer

	if err := runKernforgeDoctorCommand(workspace, []string{"--json"}, kernforgeDoctorOptions{Writer: &output}); err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	var decoded kernforgeDoctorReport
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("decode doctor JSON: %v\n%s", err, output.String())
	}
	if decoded.SchemaVersion != kernforgeDoctorSchemaVersion {
		t.Fatalf("unexpected schema version: %#v", decoded)
	}
	if _, ok := decoded.Checks["runtime.provenance"]; !ok {
		t.Fatalf("expected keyed checks object, got %#v", decoded.Checks)
	}
}

func TestRunDoctorBypassesTopLevelConfigLoadFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := t.TempDir()
	if err := os.MkdirAll(userConfigDir(), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(userConfigPath(), []byte(`{"provider":`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	oldStdout := os.Stdout
	stdoutPath := filepath.Join(t.TempDir(), "stdout.json")
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatalf("create stdout file: %v", err)
	}
	os.Stdout = stdoutFile
	t.Cleanup(func() {
		os.Stdout = oldStdout
		_ = stdoutFile.Close()
	})

	runErr := run([]string{"-cwd", workspace, "doctor", "--json"})
	_ = stdoutFile.Close()
	os.Stdout = oldStdout
	if runErr != nil {
		t.Fatalf("doctor should report config failure instead of returning it: %v", runErr)
	}
	data, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	var decoded kernforgeDoctorReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode doctor JSON: %v\n%s", err, data)
	}
	if got := decoded.Checks["configuration.config"].Status; got != kernforgeDoctorStatusFail {
		t.Fatalf("expected config failure check, got %q in %#v", got, decoded.Checks["configuration.config"])
	}
}

func TestKernforgeDoctorDirStatsTruncatesLargeDirectories(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(filepath.Join(root, "file-"+string(rune('a'+i))+".txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}
	stats, err := kernforgeDoctorDirStats(root, 2)
	if err != nil {
		t.Fatalf("dir stats: %v", err)
	}
	if !stats.truncated || stats.entries != 2 {
		t.Fatalf("expected truncated stats at two entries, got %#v", stats)
	}
}
