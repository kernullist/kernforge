package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type MCPServerConfig struct {
	Name     string            `json:"name"`
	Command  string            `json:"command"`
	Args     []string          `json:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Cwd      string            `json:"cwd,omitempty"`
	Disabled bool              `json:"disabled,omitempty"`
}

type MCPToolDescriptor struct {
	Name        string
	Description string
	InputSchema map[string]any
}

type MCPResourceDescriptor struct {
	URI         string
	Name        string
	Description string
	MIMEType    string
}

type MCPPromptArgument struct {
	Name        string
	Description string
	Required    bool
}

type MCPPromptDescriptor struct {
	Name        string
	Description string
	Arguments   []MCPPromptArgument
}

type MCPResourceRef struct {
	Server   string
	Resource MCPResourceDescriptor
}

type MCPPromptRef struct {
	Server string
	Prompt MCPPromptDescriptor
}

type MCPServerStatus struct {
	Name          string
	Command       string
	Cwd           string
	ToolCount     int
	ResourceCount int
	PromptCount   int
	Error         string
}

type MCPManager struct {
	servers []*MCPClient
	status  []MCPServerStatus
}

type MCPClient struct {
	config    MCPServerConfig
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    *bufio.Reader
	mu        sync.Mutex
	nextID    int64
	tools     []MCPToolDescriptor
	resources []MCPResourceDescriptor
	prompts   []MCPPromptDescriptor
	status    MCPServerStatus
	stderrMu  sync.Mutex
	stderr    []string
}

type MCPTool struct {
	client      *MCPClient
	namespaced  string
	remote      MCPToolDescriptor
	description string
}

type MCPResourceTool struct {
	client *MCPClient
}

type MCPPromptTool struct {
	client *MCPClient
}

func LoadMCPManager(ws Workspace, configs []MCPServerConfig) (*MCPManager, []string) {
	manager := &MCPManager{}
	warnings := []string{}
	usedNames := map[string]int{}
	for _, cfg := range configs {
		if cfg.Disabled {
			continue
		}
		name := deriveMCPServerName(cfg)
		if name == "" {
			warnings = append(warnings, "mcp server is missing name/command")
			continue
		}
		usedNames[name]++
		if usedNames[name] > 1 {
			name = fmt.Sprintf("%s-%d", name, usedNames[name])
		}
		cfg.Name = name
		client, clientWarnings, err := startMCPClient(ws, cfg)
		if err != nil {
			manager.status = append(manager.status, MCPServerStatus{
				Name:    cfg.Name,
				Command: cfg.Command,
				Cwd:     resolveMCPServerCwd(ws, cfg),
				Error:   err.Error(),
			})
			warnings = append(warnings, fmt.Sprintf("mcp server %s: %v", cfg.Name, err))
			continue
		}
		warnings = append(warnings, clientWarnings...)
		manager.servers = append(manager.servers, client)
		manager.status = append(manager.status, client.status)
	}
	return manager, warnings
}

func deriveMCPServerName(cfg MCPServerConfig) string {
	if trimmed := strings.TrimSpace(cfg.Name); trimmed != "" {
		return trimmed
	}
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		return ""
	}
	base := filepath.Base(command)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return base
}

func resolveMCPServerCwd(ws Workspace, cfg MCPServerConfig) string {
	if strings.TrimSpace(cfg.Cwd) == "" {
		return ws.Root
	}
	if filepath.IsAbs(cfg.Cwd) {
		return filepath.Clean(cfg.Cwd)
	}
	return filepath.Clean(filepath.Join(ws.Root, cfg.Cwd))
}

func startMCPClient(ws Workspace, cfg MCPServerConfig) (*MCPClient, []string, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, nil, fmt.Errorf("missing command")
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Dir = resolveMCPServerCwd(ws, cfg)
	cmd.Env = os.Environ()
	if len(cfg.Env) > 0 {
		env := make([]string, 0, len(cmd.Env)+len(cfg.Env))
		env = append(env, cmd.Env...)
		for key, value := range cfg.Env {
			env = append(env, key+"="+value)
		}
		cmd.Env = env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	client := &MCPClient{
		config: cfg,
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
		status: MCPServerStatus{
			Name:    cfg.Name,
			Command: cfg.Command,
			Cwd:     cmd.Dir,
		},
	}
	go client.captureStderr(stderr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.initialize(ctx); err != nil {
		client.Close()
		return nil, nil, err
	}
	tools, err := client.listTools(ctx)
	if err != nil {
		client.Close()
		return nil, nil, err
	}
	client.tools = tools
	client.status.ToolCount = len(tools)
	warnings := []string{}
	if resources, err := client.listResources(ctx); err == nil {
		client.resources = resources
		client.status.ResourceCount = len(resources)
	} else if !isOptionalMCPMethodError(err) {
		warnings = append(warnings, fmt.Sprintf("mcp server %s resources: %v", cfg.Name, err))
	}
	if prompts, err := client.listPrompts(ctx); err == nil {
		client.prompts = prompts
		client.status.PromptCount = len(prompts)
	} else if !isOptionalMCPMethodError(err) {
		warnings = append(warnings, fmt.Sprintf("mcp server %s prompts: %v", cfg.Name, err))
	}
	return client, warnings, nil
}

func (m *MCPManager) Close() {
	if m == nil {
		return
	}
	for _, server := range m.servers {
		server.Close()
	}
	m.servers = nil
}

func (m *MCPManager) Tools() []Tool {
	if m == nil {
		return nil
	}
	var out []Tool
	for _, server := range m.servers {
		for _, tool := range server.tools {
			out = append(out, MCPTool{
				client:      server,
				namespaced:  namespacedMCPToolName(server.config.Name, tool.Name),
				remote:      tool,
				description: fmt.Sprintf("[MCP:%s] %s", server.config.Name, strings.TrimSpace(tool.Description)),
			})
		}
		if len(server.resources) > 0 {
			out = append(out, MCPResourceTool{client: server})
		}
		if len(server.prompts) > 0 {
			out = append(out, MCPPromptTool{client: server})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Definition().Name < out[j].Definition().Name
	})
	return out
}

func (m *MCPManager) Status() []MCPServerStatus {
	if m == nil {
		return nil
	}
	return append([]MCPServerStatus(nil), m.status...)
}

func (m *MCPManager) Resources() []MCPResourceRef {
	if m == nil {
		return nil
	}
	var out []MCPResourceRef
	for _, server := range m.servers {
		for _, resource := range server.resources {
			out = append(out, MCPResourceRef{
				Server:   server.config.Name,
				Resource: resource,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Server != out[j].Server {
			return out[i].Server < out[j].Server
		}
		left := out[i].Resource.URI
		if left == "" {
			left = out[i].Resource.Name
		}
		right := out[j].Resource.URI
		if right == "" {
			right = out[j].Resource.Name
		}
		return left < right
	})
	return out
}

func (m *MCPManager) Prompts() []MCPPromptRef {
	if m == nil {
		return nil
	}
	var out []MCPPromptRef
	for _, server := range m.servers {
		for _, prompt := range server.prompts {
			out = append(out, MCPPromptRef{
				Server: server.config.Name,
				Prompt: prompt,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Server != out[j].Server {
			return out[i].Server < out[j].Server
		}
		return out[i].Prompt.Name < out[j].Prompt.Name
	})
	return out
}

func (m *MCPManager) ResourceCatalogPrompt() string {
	items := m.Resources()
	if len(items) == 0 {
		return ""
	}
	var lines []string
	for _, item := range items {
		label := item.Resource.URI
		if label == "" {
			label = item.Resource.Name
		}
		extra := ""
		if item.Resource.Name != "" && item.Resource.Name != label {
			extra = " (" + item.Resource.Name + ")"
		}
		if desc := strings.TrimSpace(item.Resource.Description); desc != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s%s - %s", item.Server, label, extra, desc))
		} else {
			lines = append(lines, fmt.Sprintf("- %s: %s%s", item.Server, label, extra))
		}
	}
	return strings.Join(lines, "\n")
}

func (m *MCPManager) PromptCatalogPrompt() string {
	items := m.Prompts()
	if len(items) == 0 {
		return ""
	}
	var lines []string
	for _, item := range items {
		args := []string{}
		for _, arg := range item.Prompt.Arguments {
			label := arg.Name
			if arg.Required {
				label += "*"
			}
			args = append(args, label)
		}
		signature := item.Prompt.Name + "(" + strings.Join(args, ", ") + ")"
		if desc := strings.TrimSpace(item.Prompt.Description); desc != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s - %s", item.Server, signature, desc))
		} else {
			lines = append(lines, fmt.Sprintf("- %s: %s", item.Server, signature))
		}
	}
	return strings.Join(lines, "\n")
}

func namespacedMCPToolName(server, tool string) string {
	return "mcp__" + sanitizeMCPName(server) + "__" + sanitizeMCPName(tool)
}

func sanitizeMCPName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z':
			b.WriteRune(ch)
		case ch >= '0' && ch <= '9':
			b.WriteRune(ch)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func (c *MCPClient) captureStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		c.stderrMu.Lock()
		c.stderr = append(c.stderr, line)
		if len(c.stderr) > 20 {
			c.stderr = append([]string(nil), c.stderr[len(c.stderr)-20:]...)
		}
		c.stderrMu.Unlock()
	}
}

func (c *MCPClient) stderrSummary() string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	if len(c.stderr) == 0 {
		return ""
	}
	return strings.Join(c.stderr, " | ")
}

func (c *MCPClient) initialize(ctx context.Context) error {
	_, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"clientInfo": map[string]any{
			"name":    "kernforge",
			"version": currentVersion(),
		},
	})
	if err != nil {
		return err
	}
	return c.notify("notifications/initialized", map[string]any{})
}

func (c *MCPClient) listTools(ctx context.Context) ([]MCPToolDescriptor, error) {
	var out []MCPToolDescriptor
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, err := c.request(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		rawTools, _ := result["tools"].([]any)
		for _, raw := range rawTools {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			tool := MCPToolDescriptor{
				Name:        stringValue(item, "name"),
				Description: stringValue(item, "description"),
				InputSchema: emptyObjectSchema(),
			}
			if schema, ok := item["inputSchema"].(map[string]any); ok && len(schema) > 0 {
				tool.InputSchema = schema
			} else if schema, ok := item["input_schema"].(map[string]any); ok && len(schema) > 0 {
				tool.InputSchema = schema
			}
			if strings.TrimSpace(tool.Name) != "" {
				out = append(out, tool)
			}
		}
		nextCursor := stringValue(result, "nextCursor")
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	return out, nil
}

func (c *MCPClient) listResources(ctx context.Context) ([]MCPResourceDescriptor, error) {
	var out []MCPResourceDescriptor
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, err := c.request(ctx, "resources/list", params)
		if err != nil {
			return nil, err
		}
		rawItems, _ := result["resources"].([]any)
		for _, raw := range rawItems {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			resource := MCPResourceDescriptor{
				URI:         stringValue(item, "uri"),
				Name:        stringValue(item, "name"),
				Description: stringValue(item, "description"),
				MIMEType:    firstNonEmptyString(item, "mimeType", "mime_type"),
			}
			if strings.TrimSpace(resource.URI) != "" || strings.TrimSpace(resource.Name) != "" {
				out = append(out, resource)
			}
		}
		nextCursor := firstNonEmptyString(result, "nextCursor", "next_cursor")
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	return out, nil
}

func (c *MCPClient) listPrompts(ctx context.Context) ([]MCPPromptDescriptor, error) {
	var out []MCPPromptDescriptor
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, err := c.request(ctx, "prompts/list", params)
		if err != nil {
			return nil, err
		}
		rawItems, _ := result["prompts"].([]any)
		for _, raw := range rawItems {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			prompt := MCPPromptDescriptor{
				Name:        stringValue(item, "name"),
				Description: stringValue(item, "description"),
			}
			if rawArgs, ok := item["arguments"].([]any); ok {
				for _, rawArg := range rawArgs {
					argObj, ok := rawArg.(map[string]any)
					if !ok {
						continue
					}
					prompt.Arguments = append(prompt.Arguments, MCPPromptArgument{
						Name:        stringValue(argObj, "name"),
						Description: stringValue(argObj, "description"),
						Required:    boolValue(argObj, "required", false),
					})
				}
			}
			if strings.TrimSpace(prompt.Name) != "" {
				out = append(out, prompt)
			}
		}
		nextCursor := firstNonEmptyString(result, "nextCursor", "next_cursor")
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	return out, nil
}

func emptyObjectSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (c *MCPClient) callTool(ctx context.Context, name string, args any) (string, error) {
	result, err := c.request(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return "", err
	}
	text := formatMCPToolResult(result)
	if boolValue(result, "isError", false) {
		if text == "" {
			text = "remote tool reported an error"
		}
		return text, fmt.Errorf("mcp tool %s failed", name)
	}
	if text == "" {
		text = "(no output)"
	}
	return text, nil
}

func (c *MCPClient) readResource(ctx context.Context, query string) (string, string, error) {
	resource, ok := c.findResource(query)
	if !ok {
		return "", "", fmt.Errorf("resource not found: %s", query)
	}
	result, err := c.request(ctx, "resources/read", map[string]any{
		"uri": resource.URI,
	})
	if err != nil {
		return "", "", err
	}
	text := formatMCPResourceResult(result)
	if text == "" {
		text = "(no content)"
	}
	display := c.config.Name + ":" + resource.URI
	if strings.TrimSpace(resource.Name) != "" && resource.Name != resource.URI {
		display += " (" + resource.Name + ")"
	}
	return display, text, nil
}

func (c *MCPClient) getPrompt(ctx context.Context, name string, args map[string]any) (string, string, error) {
	prompt, ok := c.findPrompt(name)
	if !ok {
		return "", "", fmt.Errorf("prompt not found: %s", name)
	}
	result, err := c.request(ctx, "prompts/get", map[string]any{
		"name":      prompt.Name,
		"arguments": args,
	})
	if err != nil {
		return "", "", err
	}
	text := formatMCPPromptResult(result)
	if text == "" {
		text = "(no prompt output)"
	}
	return c.config.Name + ":" + prompt.Name, text, nil
}

func formatMCPToolResult(result map[string]any) string {
	var sections []string
	if content, ok := result["content"].([]any); ok {
		for _, raw := range content {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch strings.TrimSpace(stringValue(item, "type")) {
			case "text":
				if text := strings.TrimSpace(stringValue(item, "text")); text != "" {
					sections = append(sections, text)
				}
			default:
				if data, err := json.MarshalIndent(item, "", "  "); err == nil {
					sections = append(sections, string(data))
				}
			}
		}
	}
	if len(sections) == 0 {
		if structured, ok := result["structuredContent"]; ok {
			if data, err := json.MarshalIndent(structured, "", "  "); err == nil {
				sections = append(sections, string(data))
			}
		}
	}
	if len(sections) == 0 && strings.TrimSpace(stringValue(result, "text")) != "" {
		sections = append(sections, strings.TrimSpace(stringValue(result, "text")))
	}
	return strings.Join(sections, "\n\n")
}

func formatMCPResourceResult(result map[string]any) string {
	var sections []string
	if contents, ok := result["contents"].([]any); ok {
		for _, raw := range contents {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			label := firstNonEmptyString(item, "uri", "name")
			text := strings.TrimSpace(stringValue(item, "text"))
			if text != "" {
				sections = append(sections, joinNonEmpty("Resource: "+label, text))
				continue
			}
			if blob := strings.TrimSpace(stringValue(item, "blob")); blob != "" {
				mimeType := firstNonEmptyString(item, "mimeType", "mime_type")
				sections = append(sections, joinNonEmpty(
					"Resource: "+label,
					fmt.Sprintf("[binary content omitted; mime=%s, base64_chars=%d]", valueOrDefault(mimeType, "unknown"), len(blob)),
				))
			}
		}
	}
	if len(sections) == 0 {
		if data, err := json.MarshalIndent(result, "", "  "); err == nil {
			return string(data)
		}
	}
	return strings.Join(sections, "\n\n")
}

func formatMCPPromptResult(result map[string]any) string {
	var sections []string
	if desc := strings.TrimSpace(stringValue(result, "description")); desc != "" {
		sections = append(sections, desc)
	}
	if messages, ok := result["messages"].([]any); ok {
		for _, raw := range messages {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			role := strings.TrimSpace(stringValue(item, "role"))
			text := strings.TrimSpace(formatMCPPromptContent(item["content"]))
			if text == "" {
				continue
			}
			if role == "" {
				sections = append(sections, text)
			} else {
				sections = append(sections, strings.ToUpper(role)+":\n"+text)
			}
		}
	}
	if len(sections) == 0 {
		if data, err := json.MarshalIndent(result, "", "  "); err == nil {
			return string(data)
		}
	}
	return strings.Join(sections, "\n\n")
}

func formatMCPPromptContent(raw any) string {
	switch value := raw.(type) {
	case string:
		return value
	case []any:
		var parts []string
		for _, rawItem := range value {
			switch item := rawItem.(type) {
			case string:
				if strings.TrimSpace(item) != "" {
					parts = append(parts, item)
				}
			case map[string]any:
				kind := strings.TrimSpace(stringValue(item, "type"))
				switch kind {
				case "", "text":
					if text := strings.TrimSpace(stringValue(item, "text")); text != "" {
						parts = append(parts, text)
					}
				default:
					if data, err := json.MarshalIndent(item, "", "  "); err == nil {
						parts = append(parts, string(data))
					}
				}
			}
		}
		return strings.Join(parts, "\n\n")
	case map[string]any:
		if text := strings.TrimSpace(stringValue(value, "text")); text != "" {
			return text
		}
		if data, err := json.MarshalIndent(value, "", "  "); err == nil {
			return string(data)
		}
	}
	return ""
}

func (c *MCPClient) findResource(query string) (MCPResourceDescriptor, bool) {
	trimmed := strings.TrimSpace(query)
	for _, resource := range c.resources {
		if strings.EqualFold(resource.URI, trimmed) || strings.EqualFold(resource.Name, trimmed) {
			return resource, true
		}
	}
	return MCPResourceDescriptor{}, false
}

func (c *MCPClient) findPrompt(name string) (MCPPromptDescriptor, bool) {
	trimmed := strings.TrimSpace(name)
	for _, prompt := range c.prompts {
		if strings.EqualFold(prompt.Name, trimmed) {
			return prompt, true
		}
	}
	return MCPPromptDescriptor{}, false
}

func (c *MCPClient) request(ctx context.Context, method string, params any) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextID++
	id := c.nextID
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := writeRPCMessage(c.stdin, payload); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}

	type result struct {
		payload map[string]any
		err     error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := c.readResponse(id)
		done <- result{payload: resp, err: err}
	}()

	select {
	case out := <-done:
		if out.err != nil {
			if stderr := c.stderrSummary(); stderr != "" {
				return nil, fmt.Errorf("%w (%s)", out.err, stderr)
			}
			return nil, out.err
		}
		return out.payload, nil
	case <-ctx.Done():
		c.Close()
		return nil, ctx.Err()
	}
}

func (c *MCPClient) notify(method string, params any) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	return writeRPCMessage(c.stdin, payload)
}

func (c *MCPClient) readResponse(expectedID int64) (map[string]any, error) {
	for {
		msg, err := readRPCMessage(c.stdout)
		if err != nil {
			return nil, err
		}
		if method, _ := msg["method"].(string); method != "" {
			continue
		}
		if !rpcIDMatches(msg["id"], expectedID) {
			continue
		}
		if rawErr, ok := msg["error"]; ok && rawErr != nil {
			return nil, fmt.Errorf("rpc error: %s", formatRPCError(rawErr))
		}
		if result, ok := msg["result"].(map[string]any); ok {
			return result, nil
		}
		return map[string]any{}, nil
	}
}

func (m *MCPManager) ReadResource(ctx context.Context, target string) (string, string, error) {
	serverName, resourceQuery, ok := parseMCPQualifiedTarget(target)
	if !ok {
		return "", "", fmt.Errorf("usage: server:resource-uri-or-name")
	}
	client, ok := m.findServer(serverName)
	if !ok {
		return "", "", fmt.Errorf("unknown MCP server: %s", serverName)
	}
	return client.readResource(ctx, resourceQuery)
}

func (m *MCPManager) GetPrompt(ctx context.Context, target string, args map[string]any) (string, string, error) {
	serverName, promptName, ok := parseMCPQualifiedTarget(target)
	if !ok {
		return "", "", fmt.Errorf("usage: server:prompt-name")
	}
	client, ok := m.findServer(serverName)
	if !ok {
		return "", "", fmt.Errorf("unknown MCP server: %s", serverName)
	}
	return client.getPrompt(ctx, promptName, args)
}

func (m *MCPManager) ResolveMention(ctx context.Context, raw string) (string, string, bool) {
	if m == nil {
		return "", "", false
	}
	token := strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(token), "mcp:") {
		return "", "", false
	}
	display, text, err := m.ReadResource(ctx, strings.TrimPrefix(token, "mcp:"))
	if err != nil {
		return "", "", false
	}
	return "mcp:" + display, text, true
}

func (m *MCPManager) findServer(name string) (*MCPClient, bool) {
	if m == nil {
		return nil, false
	}
	for _, server := range m.servers {
		if strings.EqualFold(server.config.Name, strings.TrimSpace(name)) {
			return server, true
		}
	}
	return nil, false
}

func parseMCPQualifiedTarget(target string) (string, string, bool) {
	trimmed := strings.TrimSpace(target)
	if strings.HasPrefix(strings.ToLower(trimmed), "mcp:") {
		trimmed = strings.TrimSpace(trimmed[len("mcp:"):])
	}
	parts := strings.SplitN(trimmed, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	server := strings.TrimSpace(parts[0])
	name := strings.TrimSpace(parts[1])
	if server == "" || name == "" {
		return "", "", false
	}
	return server, name, true
}

func writeRPCMessage(w io.Writer, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	return nil
}

func readRPCMessage(r *bufio.Reader) (map[string]any, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, err
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var msg map[string]any
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func rpcIDMatches(raw any, expected int64) bool {
	switch value := raw.(type) {
	case float64:
		return int64(value) == expected
	case int64:
		return value == expected
	case json.Number:
		n, _ := value.Int64()
		return n == expected
	default:
		return false
	}
}

func formatRPCError(raw any) string {
	if obj, ok := raw.(map[string]any); ok {
		if msg := strings.TrimSpace(stringValue(obj, "message")); msg != "" {
			return msg
		}
		if data, err := json.Marshal(obj); err == nil {
			return string(data)
		}
	}
	return fmt.Sprint(raw)
}

func isOptionalMCPMethodError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "method not found") ||
		strings.Contains(lower, "unsupported method") ||
		strings.Contains(lower, "not supported")
}

func firstNonEmptyString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringValue(m, key)); value != "" {
			return value
		}
	}
	return ""
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (c *MCPClient) Close() error {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	_ = c.stdin.Close()
	_ = c.cmd.Process.Kill()
	_, _ = c.cmd.Process.Wait()
	c.cmd = nil
	return nil
}

func (t MCPTool) Definition() ToolDefinition {
	schema := t.remote.InputSchema
	if len(schema) == 0 {
		schema = emptyObjectSchema()
	}
	description := strings.TrimSpace(t.description)
	if description == "" {
		description = fmt.Sprintf("[MCP:%s] %s", t.client.config.Name, t.remote.Name)
	}
	return ToolDefinition{
		Name:        t.namespaced,
		Description: description,
		InputSchema: schema,
	}
}

func (t MCPTool) Execute(ctx context.Context, input any) (string, error) {
	return t.client.callTool(ctx, t.remote.Name, input)
}

func (t MCPResourceTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "mcp__resource__" + sanitizeMCPName(t.client.config.Name),
		Description: fmt.Sprintf("[MCP:%s] Read a listed MCP resource by uri or name.", t.client.config.Name),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"uri": map[string]any{"type": "string"},
			},
			"required": []string{"uri"},
		},
	}
}

func (t MCPResourceTool) Execute(ctx context.Context, input any) (string, error) {
	args, _ := input.(map[string]any)
	_, text, err := t.client.readResource(ctx, stringValue(args, "uri"))
	return text, err
}

func (t MCPPromptTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "mcp__prompt__" + sanitizeMCPName(t.client.config.Name),
		Description: fmt.Sprintf("[MCP:%s] Resolve a listed MCP prompt by name and arguments.", t.client.config.Name),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
				"arguments": map[string]any{
					"type":                 "object",
					"additionalProperties": true,
				},
			},
			"required": []string{"name"},
		},
	}
}

func (t MCPPromptTool) Execute(ctx context.Context, input any) (string, error) {
	args, _ := input.(map[string]any)
	promptArgs := map[string]any{}
	if raw, ok := args["arguments"].(map[string]any); ok {
		promptArgs = raw
	}
	_, text, err := t.client.getPrompt(ctx, stringValue(args, "name"), promptArgs)
	return text, err
}
