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
	scheduler       *DaemonScheduler
	// stream fans observe-only MCP RPC progress out to connected IDE clients over
	// the token-authed GET /stream SSE endpoint. It holds no edit authority and is
	// fully opt-in: when no client is connected, publishing is a cheap no-op and
	// the daemon's existing behavior is unchanged.
	stream *daemonStreamHub
}

func runKernforgeDaemonCommand(cwd string, cfg Config, resumeID string, args []string, options mcpServerRunOptions) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: kernforge daemon <start|run|status|stop|schedule>")
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
	case "schedule":
		return runKernforgeDaemonScheduleCommand(cwd, args[1:], os.Stdout)
	default:
		return fmt.Errorf("unknown daemon command: %s", args[0])
	}
}

func runKernforgeDaemon(cwd string, cfg Config, resumeID string, options mcpServerRunOptions) error {
	options.Entrypoint = normalizeMCPServerEntrypoint(options.Entrypoint, mcpServerEntrypointDaemonServer)
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
		stream:         newDaemonStreamHub(0),
	}
	daemon.scheduler = daemon.buildScheduler(cfg)
	if err := daemon.scheduler.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "daemon scheduler load failed (continuing without persisted jobs): %v\n", err)
	}
	// The scheduler poll loop is opt-in. When disabled it never runs anything
	// unattended; the registry/RPC surface stays available so an operator can
	// still list/add/remove and run jobs explicitly.
	if cfg.Scheduler.Enabled {
		daemon.scheduler.Start()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", daemon.handleHealth)
	mux.HandleFunc("/rpc", daemon.handleRPC)
	mux.HandleFunc("/stream", daemon.handleStream)
	mux.HandleFunc("/schedule", daemon.handleSchedule)
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
		"stream_clients":  d.stream.subscriberCount(),
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
		d.publishRPCObservation("rpc_error", req, nil, false, err)
		writeDaemonJSON(w, kernforgeDaemonRPCResponse{Respond: true, Error: err.Error()})
		return
	}
	d.publishRPCObservation("rpc_request", req, nil, false, nil)
	response, respond := server.handleMessage(r.Context(), req.Message)
	d.publishRPCObservation("rpc_response", req, response, respond, nil)
	writeDaemonJSON(w, kernforgeDaemonRPCResponse{Respond: respond, Response: response})
}

// publishRPCObservation mirrors one proxied RPC step to any connected IDE stream
// client. It is observe-only: it reports method/tool/id metadata and an error or
// isError flag, never secrets and never an edit authority. Watching the stream
// lets an IDE follow tool-call progress; it cannot drive or bypass the agent
// loop's permission, review, or edit gates. With no subscriber the hub publish is
// a no-op, so this stays free on the RPC path.
func (d *kernforgeDaemonServer) publishRPCObservation(event string, req kernforgeDaemonRPCRequest, response map[string]any, respond bool, rpcErr error) {
	if d == nil || d.stream == nil {
		return
	}
	data := map[string]any{}
	if method := strings.TrimSpace(stringValue(req.Message, "method")); method != "" {
		data["method"] = method
		if strings.EqualFold(method, "tools/call") {
			if params, ok := req.Message["params"].(map[string]any); ok {
				if tool := strings.TrimSpace(stringValue(params, "name")); tool != "" {
					data["tool"] = tool
				}
			}
		}
	}
	if id, ok := req.Message["id"]; ok {
		data["id"] = id
	}
	if ws := strings.TrimSpace(req.Workspace); ws != "" {
		data["workspace"] = ws
	}
	if rpcErr != nil {
		data["error"] = rpcErr.Error()
	}
	if event == "rpc_response" {
		data["respond"] = respond
		if isError, ok := mcpResponseIsError(response); ok {
			data["is_error"] = isError
		}
	}
	d.stream.publish(event, data)
}

// mcpResponseIsError extracts the result.isError flag from an MCP response map so
// the stream can mark a failed tool call without re-running it. It returns ok
// false when the response carries no result object.
func mcpResponseIsError(response map[string]any) (bool, bool) {
	if response == nil {
		return false, false
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		return false, false
	}
	isError, _ := result["isError"].(bool)
	return isError, true
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
			if workspaceCfg, err := LoadConfigWithOptions(resolved, ConfigLoadOptions{
				StrictConfig: d.options.StrictConfig,
				Profile:      d.options.ConfigOverrides.Profile,
			}); err == nil {
				cfg = workspaceCfg
			} else {
				d.mu.Unlock()
				return nil, err
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
	if d.scheduler != nil {
		d.scheduler.Stop()
	}
	if d.stream != nil {
		d.stream.closeAll()
	}
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

func kernforgeDaemonSchedulePath() string {
	return filepath.Join(kernforgeDaemonDir(), "schedule.json")
}

func kernforgeDaemonScheduleRunLogPath() string {
	return filepath.Join(kernforgeDaemonDir(), "schedule-runs.log")
}

// buildScheduler constructs the daemon's scheduler. The runFn is the daemon's
// only execution coupling: it durably records each fired job to a run-log file
// (alongside daemon state) so an unattended run survives a restart and is
// auditable. It carries the per-job workspace and budgets, keeping multi-
// workspace isolation, and never touches a foreground goal. A heavier in-process
// goal loop or BackgroundShellBundle dispatch can be plugged in here later
// without changing the scheduler core (see ScheduleRunFunc).
func (d *kernforgeDaemonServer) buildScheduler(cfg Config) *DaemonScheduler {
	pollEvery := time.Duration(0)
	if cfg.Scheduler.PollSeconds > 0 {
		pollEvery = time.Duration(cfg.Scheduler.PollSeconds) * time.Second
	}
	return NewDaemonScheduler(kernforgeDaemonSchedulePath(), pollEvery, time.Now, recordScheduledJobRun)
}

// recordScheduledJobRun is the fail-closed default execution transport: it
// appends a durable JSON line describing the fired job. It never panics on a
// write failure (the scheduler already contains panics) and returns the error so
// the scheduler can mark the run without disabling the job.
func recordScheduledJobRun(req ScheduleRunRequest) error {
	entry := map[string]any{
		"job_id":    req.Job.ID,
		"name":      req.Job.Name,
		"type":      req.Job.Type,
		"objective": req.Job.Objective,
		"workspace": req.Job.Workspace,
		"budgets":   req.Job.Budgets,
		"triggered": req.Triggered.Format(time.RFC3339),
		"next_run":  scheduleTimeOrDash(req.Job.NextRun),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(kernforgeDaemonDir(), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(kernforgeDaemonScheduleRunLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

type kernforgeScheduleRPCRequest struct {
	Token  string         `json:"token"`
	Action string         `json:"action"`
	Job    *JobDefinition `json:"job,omitempty"`
	Name   string         `json:"name,omitempty"`
}

type kernforgeScheduleRPCResponse struct {
	OK    bool            `json:"ok"`
	Jobs  []JobDefinition `json:"jobs,omitempty"`
	Job   *JobDefinition  `json:"job,omitempty"`
	Note  string          `json:"note,omitempty"`
	Error string          `json:"error,omitempty"`
}

// handleSchedule is the token-authed RPC endpoint that delegates to the
// scheduler. It supports list|add|remove|run, mirroring the subcommands. It does
// not start or stop the poll loop; the loop is governed solely by config.
func (d *kernforgeDaemonServer) handleSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req kernforgeScheduleRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Token != d.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if d.scheduler == nil {
		writeDaemonJSON(w, kernforgeScheduleRPCResponse{Error: "scheduler is not configured"})
		return
	}
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "", "list":
		writeDaemonJSON(w, kernforgeScheduleRPCResponse{OK: true, Jobs: d.scheduler.Jobs()})
	case "add":
		if req.Job == nil {
			writeDaemonJSON(w, kernforgeScheduleRPCResponse{Error: "add requires a job definition"})
			return
		}
		job, err := d.scheduler.Add(*req.Job)
		if err != nil {
			writeDaemonJSON(w, kernforgeScheduleRPCResponse{Error: err.Error()})
			return
		}
		writeDaemonJSON(w, kernforgeScheduleRPCResponse{OK: true, Job: &job, Note: "scheduled job added"})
	case "remove":
		removed, err := d.scheduler.Remove(req.Name)
		if err != nil {
			writeDaemonJSON(w, kernforgeScheduleRPCResponse{Error: err.Error()})
			return
		}
		if !removed {
			writeDaemonJSON(w, kernforgeScheduleRPCResponse{Error: "no scheduled job matched " + req.Name})
			return
		}
		writeDaemonJSON(w, kernforgeScheduleRPCResponse{OK: true, Note: "scheduled job removed"})
	case "run":
		job, err := d.scheduler.RunNow(req.Name)
		if err != nil {
			writeDaemonJSON(w, kernforgeScheduleRPCResponse{Error: err.Error()})
			return
		}
		writeDaemonJSON(w, kernforgeScheduleRPCResponse{OK: true, Job: &job, Note: "scheduled job run dispatched"})
	default:
		writeDaemonJSON(w, kernforgeScheduleRPCResponse{Error: "unknown schedule action: " + req.Action})
	}
}

// runKernforgeDaemonScheduleCommand parses the 'daemon schedule' subcommand and
// posts to the running daemon's /schedule endpoint. It follows the goal command
// parser pattern: a leading action verb, then flags for add.
func runKernforgeDaemonScheduleCommand(cwd string, args []string, w io.Writer) error {
	action := "list"
	if len(args) > 0 {
		action = strings.ToLower(strings.TrimSpace(args[0]))
		args = args[1:]
	}
	state, ok := readKernforgeDaemonState()
	if !ok {
		return fmt.Errorf("KernForge daemon is not running; start it with: kernforge daemon start")
	}
	if _, err := kernforgeDaemonHealth(state, 2*time.Second); err != nil {
		return fmt.Errorf("KernForge daemon is not reachable: %w", err)
	}
	req := kernforgeScheduleRPCRequest{Token: state.Token, Action: action}
	switch action {
	case "list":
		// no extra args
	case "add":
		job, err := parseScheduleAddArgs(cwd, args)
		if err != nil {
			return err
		}
		req.Job = &job
	case "remove", "run":
		name := strings.TrimSpace(strings.Join(args, " "))
		if name == "" {
			return fmt.Errorf("usage: kernforge daemon schedule %s <name>", action)
		}
		req.Name = name
	default:
		return fmt.Errorf("usage: kernforge daemon schedule <list|add|remove|run>")
	}
	resp, err := postKernforgeScheduleRPC(state, req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(resp.Error) != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	printScheduleRPCResponse(w, action, resp)
	return nil
}

func parseScheduleAddArgs(cwd string, args []string) (JobDefinition, error) {
	job := JobDefinition{Type: scheduleJobTypeGoal}
	objectiveParts := []string{}
	for i := 0; i < len(args); i++ {
		field := strings.TrimSpace(args[i])
		if field == "" {
			continue
		}
		switch field {
		case "--name":
			if i+1 >= len(args) {
				return job, fmt.Errorf("--name requires a value")
			}
			i++
			job.Name = strings.TrimSpace(args[i])
		case "--type":
			if i+1 >= len(args) {
				return job, fmt.Errorf("--type requires a value (goal|verify|batch)")
			}
			i++
			job.Type = strings.ToLower(strings.TrimSpace(args[i]))
		case "--interval":
			if i+1 >= len(args) {
				return job, fmt.Errorf("--interval requires a value, for example 30m or 6h")
			}
			i++
			job.Interval = strings.TrimSpace(args[i])
		case "--cron":
			if i+1 >= len(args) {
				return job, fmt.Errorf("--cron requires a 5-field expression")
			}
			i++
			job.Cron = strings.TrimSpace(args[i])
		case "--workspace":
			if i+1 >= len(args) {
				return job, fmt.Errorf("--workspace requires a path")
			}
			i++
			job.Workspace = strings.TrimSpace(args[i])
		case "--token-budget":
			value, err := parseScheduleIntFlag(args, &i, "--token-budget")
			if err != nil {
				return job, err
			}
			job.Budgets.TokenBudget = value
		case "--time-budget-seconds":
			value, err := parseScheduleIntFlag(args, &i, "--time-budget-seconds")
			if err != nil {
				return job, err
			}
			job.Budgets.TimeBudgetSeconds = value
		case "--max-iterations":
			value, err := parseScheduleIntFlag(args, &i, "--max-iterations")
			if err != nil {
				return job, err
			}
			job.Budgets.MaxIterations = value
		default:
			objectiveParts = append(objectiveParts, field)
		}
	}
	job.Objective = strings.TrimSpace(strings.Join(objectiveParts, " "))
	if strings.TrimSpace(job.Workspace) == "" {
		job.Workspace = strings.TrimSpace(cwd)
	}
	return job, nil
}

func parseScheduleIntFlag(args []string, i *int, name string) (int, error) {
	if *i+1 >= len(args) {
		return 0, fmt.Errorf("%s requires a non-negative integer", name)
	}
	*i++
	value, err := strconv.Atoi(strings.TrimSpace(args[*i]))
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid %s value: %s", name, args[*i])
	}
	return value, nil
}

func postKernforgeScheduleRPC(state kernforgeDaemonState, req kernforgeScheduleRPCRequest) (kernforgeScheduleRPCResponse, error) {
	data, _ := json.Marshal(req)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post("http://"+state.Addr+"/schedule", "application/json", bytes.NewReader(data))
	if err != nil {
		return kernforgeScheduleRPCResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return kernforgeScheduleRPCResponse{}, fmt.Errorf("daemon schedule rpc failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out kernforgeScheduleRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return kernforgeScheduleRPCResponse{}, err
	}
	return out, nil
}

func printScheduleRPCResponse(w io.Writer, action string, resp kernforgeScheduleRPCResponse) {
	switch action {
	case "list":
		lines := renderScheduleJobLines(resp.Jobs)
		if len(lines) == 0 {
			fmt.Fprintln(w, "No scheduled jobs.")
			return
		}
		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
	default:
		note := strings.TrimSpace(resp.Note)
		if note == "" {
			note = "ok"
		}
		if resp.Job != nil {
			fmt.Fprintf(w, "%s: %s [%s] next=%s\n", note, resp.Job.Name, resp.Job.ID, scheduleTimeOrDash(resp.Job.NextRun))
			return
		}
		fmt.Fprintln(w, note)
	}
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
