package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const kernforgeDaemonStateVersion = 1

type kernforgeDaemonState struct {
	Version   int       `json:"version"`
	PID       int       `json:"pid"`
	Addr      string    `json:"addr"`
	Token     string    `json:"token"`
	StartedAt time.Time `json:"started_at"`
	LogPath   string    `json:"log_path,omitempty"`
}

type kernforgeDaemonRPCRequest struct {
	Token           string         `json:"token"`
	Workspace       string         `json:"workspace,omitempty"`
	WorkspaceSource string         `json:"workspace_source,omitempty"`
	Message         map[string]any `json:"message"`
}

type kernforgeDaemonRPCResponse struct {
	Respond  bool           `json:"respond"`
	Response map[string]any `json:"response,omitempty"`
	Error    string         `json:"error,omitempty"`
}

type kernforgeDaemonServer struct {
	mu              sync.Mutex
	fallbackCWD     string
	fallbackConfig  Config
	resumeID        string
	options         mcpServerRunOptions
	token           string
	runtimes        map[string]*kernforgeMCPServerRuntime
	httpServer      *http.Server
	shutdownStarted bool
}

func runKernforgeDaemonCommand(cwd string, cfg Config, resumeID string, args []string, options mcpServerRunOptions) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: kernforge daemon <start|run|status|stop>")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "start":
		return startKernforgeDaemon(cwd, options.StrictConfig, options.ConfigOverrides.BypassHookTrust, args[1:])
	case "run":
		return runKernforgeDaemon(cwd, cfg, resumeID, options)
	case "status":
		return printKernforgeDaemonStatus(os.Stdout)
	case "stop":
		return stopKernforgeDaemon(os.Stdout)
	default:
		return fmt.Errorf("unknown daemon command: %s", args[0])
	}
}

func runKernforgeDaemon(cwd string, cfg Config, resumeID string, options mcpServerRunOptions) error {
	if err := os.MkdirAll(kernforgeDaemonDir(), 0o755); err != nil {
		return err
	}
	token, err := randomDaemonToken()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	state := kernforgeDaemonState{
		Version:   kernforgeDaemonStateVersion,
		PID:       os.Getpid(),
		Addr:      listener.Addr().String(),
		Token:     token,
		StartedAt: time.Now(),
		LogPath:   kernforgeDaemonLogPath(),
	}
	if err := writeKernforgeDaemonState(state); err != nil {
		_ = listener.Close()
		return err
	}
	daemon := &kernforgeDaemonServer{
		fallbackCWD:    cwd,
		fallbackConfig: cfg,
		resumeID:       resumeID,
		options:        options,
		token:          token,
		runtimes:       map[string]*kernforgeMCPServerRuntime{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", daemon.handleHealth)
	mux.HandleFunc("/rpc", daemon.handleRPC)
	mux.HandleFunc("/shutdown", daemon.handleShutdown)
	daemon.httpServer = &http.Server{Handler: mux}
	defer daemon.close()
	defer func() {
		current, ok := readKernforgeDaemonState()
		if ok && current.PID == os.Getpid() {
			_ = os.Remove(kernforgeDaemonStatePath())
		}
	}()
	err = daemon.httpServer.Serve(listener)
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func startKernforgeDaemon(cwd string, strictConfig bool, bypassHookTrust bool, args []string) error {
	if state, ok := readKernforgeDaemonState(); ok {
		if _, err := kernforgeDaemonHealth(state, 2*time.Second); err == nil {
			fmt.Fprintf(os.Stdout, "KernForge daemon already running at %s pid=%d\n", state.Addr, state.PID)
			return nil
		}
	}
	if err := os.MkdirAll(kernforgeDaemonDir(), 0o755); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	childArgs := []string{}
	if strings.TrimSpace(cwd) != "" {
		childArgs = append(childArgs, "-cwd", cwd)
	}
	if strictConfig {
		childArgs = append(childArgs, "-strict-config")
	}
	if bypassHookTrust {
		childArgs = append(childArgs, "-dangerously-bypass-hook-trust")
	}
	childArgs = append(childArgs, "daemon", "run")
	cmd := exec.Command(exe, childArgs...)
	cmd.Dir = cwd
	logPath := kernforgeDaemonLogPath()
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, ok := readKernforgeDaemonState()
		if ok {
			if _, err := kernforgeDaemonHealth(state, 500*time.Millisecond); err == nil {
				fmt.Fprintf(os.Stdout, "KernForge daemon started at %s pid=%d\n", state.Addr, state.PID)
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon process started pid=%d but did not become ready; see %s", cmd.Process.Pid, logPath)
}

func printKernforgeDaemonStatus(w io.Writer) error {
	state, ok := readKernforgeDaemonState()
	if !ok {
		fmt.Fprintln(w, "KernForge daemon is not running.")
		return nil
	}
	health, err := kernforgeDaemonHealth(state, 2*time.Second)
	if err != nil {
		fmt.Fprintf(w, "KernForge daemon state exists but is not reachable: %v\n", err)
		fmt.Fprintf(w, "state: %s pid=%d addr=%s\n", kernforgeDaemonStatePath(), state.PID, state.Addr)
		return nil
	}
	data, _ := json.MarshalIndent(health, "", "  ")
	fmt.Fprintln(w, string(data))
	return nil
}

func stopKernforgeDaemon(w io.Writer) error {
	state, ok := readKernforgeDaemonState()
	if !ok {
		fmt.Fprintln(w, "KernForge daemon is not running.")
		return nil
	}
	body, _ := json.Marshal(map[string]any{"token": state.Token})
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post("http://"+state.Addr+"/shutdown", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("daemon shutdown failed: %s %s", resp.Status, strings.TrimSpace(string(data)))
	}
	fmt.Fprintf(w, "KernForge daemon stopped at %s pid=%d\n", state.Addr, state.PID)
	return nil
}

func runKernforgeMCPDaemonProxy(cwd string, in io.Reader, out io.Writer) error {
	state, ok := readKernforgeDaemonState()
	if !ok {
		return fmt.Errorf("KernForge daemon is not running; start it with: kernforge daemon start")
	}
	if _, err := kernforgeDaemonHealth(state, 2*time.Second); err != nil {
		return fmt.Errorf("KernForge daemon is not reachable: %w", err)
	}
	reader := bufio.NewReader(in)
	activeWorkspace := cwd
	activeSource := "fallback"
	client := &http.Client{Timeout: 10 * time.Minute}
	for {
		msg, frameMode, err := readRPCMessageFramed(reader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if workspace, source := mcpWorkspaceHintFromMessage(msg); workspace != "" {
			activeWorkspace = workspace
			activeSource = source
		}
		response, respond, err := callKernforgeDaemonRPCWithStateRefresh(
			client,
			&state,
			activeWorkspace,
			activeSource,
			msg,
			readKernforgeDaemonState,
			kernforgeDaemonHealth,
		)
		if err != nil {
			id := msg["id"]
			response = mcpServerError(id, -32000, err.Error(), nil)
			respond = true
		}
		if !respond {
			continue
		}
		if err := writeRPCMessageFramed(out, response, frameMode); err != nil {
			return err
		}
	}
}

func (d *kernforgeDaemonServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	d.mu.Lock()
	workspaceCount := len(d.runtimes)
	d.mu.Unlock()
	writeDaemonJSON(w, map[string]any{
		"ok":              true,
		"pid":             os.Getpid(),
		"version":         currentVersion(),
		"workspace_count": workspaceCount,
	})
}

func (d *kernforgeDaemonServer) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req kernforgeDaemonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Token != d.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	server, err := d.ensureServer(req.Workspace, req.WorkspaceSource)
	if err != nil {
		writeDaemonJSON(w, kernforgeDaemonRPCResponse{Respond: true, Error: err.Error()})
		return
	}
	response, respond := server.handleMessage(r.Context(), req.Message)
	writeDaemonJSON(w, kernforgeDaemonRPCResponse{Respond: respond, Response: response})
}

func (d *kernforgeDaemonServer) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(stringValue(req, "token")) != d.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	d.mu.Lock()
	if !d.shutdownStarted {
		d.shutdownStarted = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = d.httpServer.Shutdown(ctx)
		}()
	}
	d.mu.Unlock()
	writeDaemonJSON(w, map[string]any{"ok": true})
}

func (d *kernforgeDaemonServer) ensureServer(workspace string, source string) (*kernforgeMCPServer, error) {
	root := strings.TrimSpace(workspace)
	if root == "" {
		root = d.fallbackCWD
		source = "fallback"
	}
	resolved, err := resolveMCPWorkspacePath(root)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	runtime := d.runtimes[filepath.Clean(resolved)]
	if runtime == nil {
		cfg := d.fallbackConfig
		if d.options.LoadWorkspaceConfig && !samePath(resolved, d.fallbackCWD) {
			if workspaceCfg, err := LoadConfigWithOptions(resolved, ConfigLoadOptions{StrictConfig: d.options.StrictConfig}); err == nil {
				cfg = workspaceCfg
			}
		}
		runtime = &kernforgeMCPServerRuntime{
			fallbackCWD:     resolved,
			fallbackConfig:  cfg,
			resumeID:        d.resumeID,
			options:         d.options,
			workspaceSource: firstNonBlankString(source, "fallback"),
		}
		d.runtimes[filepath.Clean(resolved)] = runtime
	}
	d.mu.Unlock()
	return runtime.ensureServer(resolved, firstNonBlankString(source, "daemon"))
}

func (d *kernforgeDaemonServer) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, runtime := range d.runtimes {
		runtime.close()
	}
	d.runtimes = map[string]*kernforgeMCPServerRuntime{}
}

func callKernforgeDaemonRPC(client *http.Client, state kernforgeDaemonState, workspace string, source string, msg map[string]any) (map[string]any, bool, error) {
	req := kernforgeDaemonRPCRequest{
		Token:           state.Token,
		Workspace:       workspace,
		WorkspaceSource: source,
		Message:         msg,
	}
	data, _ := json.Marshal(req)
	resp, err := client.Post("http://"+state.Addr+"/rpc", "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, false, fmt.Errorf("daemon rpc failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out kernforgeDaemonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(out.Error) != "" {
		return nil, false, fmt.Errorf(out.Error)
	}
	return out.Response, out.Respond, nil
}

type kernforgeDaemonStateReader func() (kernforgeDaemonState, bool)

type kernforgeDaemonHealthChecker func(kernforgeDaemonState, time.Duration) (map[string]any, error)

func callKernforgeDaemonRPCWithStateRefresh(
	client *http.Client,
	state *kernforgeDaemonState,
	workspace string,
	source string,
	msg map[string]any,
	readState kernforgeDaemonStateReader,
	health kernforgeDaemonHealthChecker,
) (map[string]any, bool, error) {
	response, respond, err := callKernforgeDaemonRPC(client, *state, workspace, source, msg)
	if err == nil {
		return response, respond, nil
	}
	if !kernforgeDaemonRPCErrorCanUseStateRefresh(err) {
		return nil, false, err
	}
	refreshed, refreshErr := refreshKernforgeDaemonStateAfterRPCError(state, readState, health)
	if refreshErr != nil {
		return nil, false, err
	}
	if !refreshed {
		return nil, false, err
	}
	return callKernforgeDaemonRPC(client, *state, workspace, source, msg)
}

func refreshKernforgeDaemonStateAfterRPCError(
	state *kernforgeDaemonState,
	readState kernforgeDaemonStateReader,
	health kernforgeDaemonHealthChecker,
) (bool, error) {
	if state == nil || readState == nil || health == nil {
		return false, nil
	}
	next, ok := readState()
	if !ok {
		return false, nil
	}
	if _, err := health(next, 2*time.Second); err != nil {
		return false, err
	}
	if kernforgeDaemonStateEquivalent(*state, next) {
		return true, nil
	}
	*state = next
	return true, nil
}

func kernforgeDaemonStateEquivalent(left kernforgeDaemonState, right kernforgeDaemonState) bool {
	return strings.TrimSpace(left.Addr) == strings.TrimSpace(right.Addr) &&
		strings.TrimSpace(left.Token) == strings.TrimSpace(right.Token) &&
		left.PID == right.PID
}

func kernforgeDaemonRPCErrorCanUseStateRefresh(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	if text == "" {
		return false
	}
	recoverable := []string{
		"connection refused",
		"actively refused",
		"connectex",
		"connection reset",
		"connection aborted",
		"broken pipe",
		"eof",
		"timeout",
		"deadline exceeded",
		"no connection could be made",
		"daemon rpc failed: 401",
		"daemon rpc failed: 403",
	}
	for _, marker := range recoverable {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func kernforgeDaemonHealth(state kernforgeDaemonState, timeout time.Duration) (map[string]any, error) {
	if strings.TrimSpace(state.Addr) == "" {
		return nil, fmt.Errorf("daemon address is empty")
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get("http://" + state.Addr + "/health")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(resp.Status)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	out["addr"] = state.Addr
	out["state_path"] = kernforgeDaemonStatePath()
	out["log_path"] = state.LogPath
	out["started_at"] = state.StartedAt.Format(time.RFC3339)
	return out, nil
}

func writeDaemonJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func readKernforgeDaemonState() (kernforgeDaemonState, bool) {
	data, err := os.ReadFile(kernforgeDaemonStatePath())
	if err != nil {
		return kernforgeDaemonState{}, false
	}
	var state kernforgeDaemonState
	if err := json.Unmarshal(data, &state); err != nil {
		return kernforgeDaemonState{}, false
	}
	if strings.TrimSpace(state.Addr) == "" || strings.TrimSpace(state.Token) == "" {
		return kernforgeDaemonState{}, false
	}
	return state, true
}

func writeKernforgeDaemonState(state kernforgeDaemonState) error {
	if err := os.MkdirAll(kernforgeDaemonDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(kernforgeDaemonStatePath(), data, 0o600)
}

func randomDaemonToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func kernforgeDaemonDir() string {
	return filepath.Join(userConfigDir(), "daemon")
}

func kernforgeDaemonStatePath() string {
	return filepath.Join(kernforgeDaemonDir(), "daemon.json")
}

func kernforgeDaemonLogPath() string {
	return filepath.Join(kernforgeDaemonDir(), "daemon.log")
}

func daemonFlagValue(args []string, name string) string {
	prefix := name + "="
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
	}
	return ""
}

func daemonBoolFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == name {
			return true
		}
		if strings.HasPrefix(arg, name+"=") {
			value := strings.TrimPrefix(arg, name+"=")
			parsed, _ := strconv.ParseBool(value)
			return parsed
		}
	}
	return false
}
