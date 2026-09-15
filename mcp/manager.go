package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	fridaycache "github.com/basenana/friday/cache"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/workspace"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

type State string

const (
	StateDisabled State = "disabled"
	StateBlocked  State = "blocked"
	StateCached   State = "cached"
	StateStarting State = "starting"
	StateReady    State = "ready"
	StateDegraded State = "degraded"
	StateFailed   State = "failed"
	StateClosed   State = "closed"
)

type ManagerConfig struct {
	ConfigRoots      []workspace.ResourceRoot
	ProjectRoot      string
	Cache            fridaycache.Store
	CacheRoot        string
	TrustPath        string
	HandshakeTimeout time.Duration
	CacheTTL         time.Duration
}

type ServerStatus struct {
	Name      string        `json:"name"`
	Transport TransportType `json:"transport"`
	State     State         `json:"state"`
	Tools     int           `json:"tools"`
	Source    string        `json:"source"`
	Project   bool          `json:"project"`
	Trusted   bool          `json:"trusted"`
	Error     string        `json:"error,omitempty"`
}

type Manager struct {
	mu       sync.RWMutex
	servers  map[string]*managedServer
	cache    fridaycache.Store
	trust    *trustStore
	timeout  time.Duration
	cacheTTL time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	closed   bool
}

type managedServer struct {
	mu         sync.Mutex
	config     ServerConfig
	state      State
	lastErr    error
	tools      []*tools.Tool
	client     *client.Client
	connCancel context.CancelFunc
	attempt    chan struct{}
	refreshing bool
}

type cachedDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type cachedToolSet struct {
	Tools []cachedDefinition `json:"tools"`
}

func NewManager(config ManagerConfig) (*Manager, error) {
	definitions, err := LoadConfigRoots(config.ConfigRoots)
	if err != nil {
		return nil, err
	}
	if config.HandshakeTimeout <= 0 {
		config.HandshakeTimeout = 10 * time.Second
	}
	if config.CacheTTL <= 0 {
		config.CacheTTL = 24 * time.Hour
	}
	if config.CacheRoot == "" || config.TrustPath == "" {
		home, _ := os.UserHomeDir()
		dataRoot := filepath.Join(home, ".friday")
		if config.CacheRoot == "" {
			config.CacheRoot = filepath.Join(dataRoot, "caches")
		}
		if config.TrustPath == "" {
			config.TrustPath = filepath.Join(dataRoot, "states", "mcp_trust.json")
		}
	}
	if config.Cache == nil {
		config.Cache = fridaycache.NewFileStore(config.CacheRoot)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		servers: make(map[string]*managedServer, len(definitions)), cache: config.Cache,
		trust:   newTrustStore(config.TrustPath, canonicalProject(config.ProjectRoot)),
		timeout: config.HandshakeTimeout, cacheTTL: config.CacheTTL, ctx: ctx, cancel: cancel,
	}
	for name, definition := range definitions {
		state := StateStarting
		trusted := !definition.Project || m.trust.trusted(name, definition.Digest)
		if definition.Disabled {
			state = StateDisabled
		} else if !trusted {
			state = StateBlocked
		}
		server := &managedServer{config: definition, state: state}
		m.servers[name] = server
		if trusted && !definition.Disabled {
			var cached cachedToolSet
			status, cacheErr := m.cache.Get(context.Background(), "mcp", cacheKey(definition), &cached)
			if cacheErr == nil && status != fridaycache.StatusMiss {
				server.tools = m.adaptCached(name, cached.Tools)
				server.state = StateCached
			}
		}
	}
	return m, nil
}

// Warmup connects cache misses before returning and refreshes cached servers
// in the background. Each server has an independent timeout and failure.
func (m *Manager) Warmup(ctx context.Context) {
	var wait sync.WaitGroup
	for _, name := range m.names() {
		server := m.server(name)
		server.mu.Lock()
		state := server.state
		server.mu.Unlock()
		if state == StateDisabled || state == StateBlocked {
			continue
		}
		if state == StateCached {
			go func(n string) { _ = m.ensureReady(context.Background(), n, false) }(name)
			continue
		}
		wait.Add(1)
		go func(n string) { defer wait.Done(); _ = m.ensureReady(ctx, n, false) }(name)
	}
	wait.Wait()
}

func (m *Manager) Tools() []*tools.Tool {
	var result []*tools.Tool
	for _, name := range m.names() {
		server := m.server(name)
		server.mu.Lock()
		if server.state != StateBlocked && server.state != StateDisabled && server.state != StateClosed {
			result = append(result, server.tools...)
		}
		server.mu.Unlock()
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (m *Manager) Status() []ServerStatus {
	result := make([]ServerStatus, 0, len(m.servers))
	for _, name := range m.names() {
		status, _ := m.Inspect(name)
		result = append(result, status)
	}
	return result
}

func (m *Manager) Inspect(name string) (ServerStatus, error) {
	server := m.server(name)
	if server == nil {
		return ServerStatus{}, fmt.Errorf("unknown MCP server %q", name)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	status := ServerStatus{Name: name, Transport: server.config.Type, State: server.state, Tools: len(server.tools), Source: server.config.Source, Project: server.config.Project, Trusted: !server.config.Project || m.trust.trusted(name, server.config.Digest)}
	if server.lastErr != nil {
		status.Error = server.lastErr.Error()
	}
	return status, nil
}

func (m *Manager) Refresh(ctx context.Context, name string) error {
	if name != "" {
		return m.ensureReady(ctx, name, true)
	}
	var wait sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for _, serverName := range m.names() {
		status, _ := m.Inspect(serverName)
		if status.State == StateDisabled || status.State == StateBlocked {
			continue
		}
		wait.Add(1)
		go func(n string) {
			defer wait.Done()
			if err := m.ensureReady(ctx, n, true); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(serverName)
	}
	wait.Wait()
	return errors.Join(errs...)
}

func (m *Manager) Reconnect(ctx context.Context, name string) error {
	return m.ensureReady(ctx, name, true)
}

func (m *Manager) Trust(ctx context.Context, name string) error {
	server := m.server(name)
	if server == nil {
		return fmt.Errorf("unknown MCP server %q", name)
	}
	server.mu.Lock()
	config := server.config
	server.mu.Unlock()
	if !config.Project {
		return nil
	}
	if err := m.trust.set(name, config.Digest); err != nil {
		return err
	}
	server.mu.Lock()
	if server.state == StateBlocked {
		server.state = StateStarting
	}
	server.mu.Unlock()
	return m.ensureReady(ctx, name, false)
}

func (m *Manager) Untrust(_ context.Context, name string) error {
	server := m.server(name)
	if server == nil {
		return fmt.Errorf("unknown MCP server %q", name)
	}
	server.mu.Lock()
	config := server.config
	server.mu.Unlock()
	if !config.Project {
		return fmt.Errorf("HOME MCP server %q is trusted by default", name)
	}
	if err := m.trust.remove(name); err != nil {
		return err
	}
	server.mu.Lock()
	old, cancel := server.client, server.connCancel
	server.client = nil
	server.connCancel = nil
	server.tools = nil
	server.state = StateBlocked
	server.lastErr = nil
	server.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	if cancel != nil {
		cancel()
	}
	return nil
}

func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()
	var errs []error
	for _, server := range m.servers {
		server.mu.Lock()
		cli, cancel := server.client, server.connCancel
		server.client = nil
		server.connCancel = nil
		server.state = StateClosed
		server.mu.Unlock()
		if cli != nil {
			if err := cli.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if cancel != nil {
			cancel()
		}
	}
	m.cancel()
	return errors.Join(errs...)
}

func (m *Manager) ensureReady(ctx context.Context, name string, force bool) error {
	server := m.server(name)
	if server == nil {
		return fmt.Errorf("unknown MCP server %q", name)
	}
	server.mu.Lock()
	if server.state == StateDisabled {
		server.mu.Unlock()
		return fmt.Errorf("MCP server %q is disabled", name)
	}
	if server.state == StateBlocked {
		server.mu.Unlock()
		return fmt.Errorf("MCP server %q is not trusted", name)
	}
	if server.state == StateClosed {
		server.mu.Unlock()
		return fmt.Errorf("MCP server %q is closed", name)
	}
	if !force && server.state == StateReady && server.client != nil {
		server.mu.Unlock()
		return nil
	}
	if server.attempt != nil {
		done := server.attempt
		server.mu.Unlock()
		select {
		case <-done:
			server.mu.Lock()
			err := server.lastErr
			ready := server.client != nil
			server.mu.Unlock()
			if ready {
				return nil
			}
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan struct{})
	server.attempt = done
	hadTools := len(server.tools) > 0
	if server.state != StateCached {
		server.state = StateStarting
	}
	old, oldCancel := server.client, server.connCancel
	server.client = nil
	server.connCancel = nil
	server.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	if oldCancel != nil {
		oldCancel()
	}
	cli, cancel, definitions, err := m.connect(ctx, server.config, name)
	if err != nil {
		err = sanitizeConnectionError(err, server.config)
	}
	server.mu.Lock()
	m.mu.RLock()
	managerClosed := m.closed
	m.mu.RUnlock()
	if managerClosed || server.state == StateClosed {
		if cli != nil {
			_ = cli.Close()
		}
		if cancel != nil {
			cancel()
		}
		server.lastErr = context.Canceled
		server.state = StateClosed
		server.attempt = nil
		close(done)
		server.mu.Unlock()
		return context.Canceled
	}
	if err != nil {
		server.lastErr = err
		if hadTools {
			server.state = StateDegraded
		} else {
			server.state = StateFailed
		}
	} else {
		server.client = cli
		server.connCancel = cancel
		server.lastErr = nil
		server.state = StateReady
		server.tools = m.adaptCached(name, definitions)
	}
	server.attempt = nil
	close(done)
	server.mu.Unlock()
	if err == nil {
		_ = m.cache.Put(context.Background(), "mcp", cacheKey(server.config), cachedToolSet{Tools: definitions}, m.cacheTTL)
	}
	return err
}

func (m *Manager) connect(parent context.Context, config ServerConfig, name string) (*client.Client, context.CancelFunc, []cachedDefinition, error) {
	connCtx, connCancel := context.WithCancel(m.ctx)
	var trans transport.Interface
	switch config.Type {
	case TransportStdio:
		args := make([]string, len(config.Args))
		for i, arg := range config.Args {
			args[i] = expand(arg)
		}
		opts := []transport.StdioOption{
			transport.WithCommandLogger(newTransportLogger(name, config.Type)),
		}
		if config.Cwd != "" {
			cwd := expand(config.Cwd)
			opts = append(opts, transport.WithCommandFunc(func(ctx context.Context, command string, env, args []string) (*exec.Cmd, error) {
				cmd := exec.CommandContext(ctx, command, args...)
				cmd.Env = append(os.Environ(), env...)
				cmd.Dir = cwd
				return cmd, nil
			}))
		}
		trans = transport.NewStdioWithOptions(expand(config.Command), config.expandedEnv(), args, opts...)
	case TransportSSE:
		headers := expandedMap(config.Headers)
		t, err := transport.NewSSE(
			expand(config.URL),
			transport.WithHeaders(headers),
			transport.WithSSELogger(newTransportLogger(name, config.Type)),
		)
		if err != nil {
			connCancel()
			return nil, nil, nil, err
		}
		trans = t
	case TransportStreamableHTTP:
		headers := expandedMap(config.Headers)
		t, err := transport.NewStreamableHTTP(
			expand(config.URL),
			transport.WithHTTPHeaders(headers),
			transport.WithContinuousListening(),
			transport.WithHTTPLogger(newTransportLogger(name, config.Type)),
		)
		if err != nil {
			connCancel()
			return nil, nil, nil, err
		}
		trans = t
	default:
		connCancel()
		return nil, nil, nil, fmt.Errorf("unsupported MCP transport %q", config.Type)
	}
	cli := client.NewClient(trans)
	startDone := make(chan error, 1)
	go func() { startDone <- cli.Start(connCtx) }()
	opCtx, cancel := context.WithTimeout(parent, m.timeout)
	defer cancel()
	select {
	case err := <-startDone:
		if err != nil {
			connCancel()
			_ = cli.Close()
			return nil, nil, nil, fmt.Errorf("start MCP server %q: %w", name, err)
		}
	case <-opCtx.Done():
		connCancel()
		_ = cli.Close()
		return nil, nil, nil, fmt.Errorf("start MCP server %q: %w", name, opCtx.Err())
	}
	if stderr, ok := client.GetStderr(cli); ok {
		go func() { _, _ = io.Copy(io.Discard, stderr) }()
	}
	cli.OnNotification(func(notification mcpgo.JSONRPCNotification) {
		if string(notification.Method) == mcpgo.MethodNotificationToolsListChanged {
			m.scheduleToolRefresh(name)
		}
	})
	cli.OnConnectionLost(func(connectionErr error) {
		server := m.server(name)
		if server == nil {
			return
		}
		server.mu.Lock()
		if server.client == cli {
			server.client = nil
			server.state = StateDegraded
			server.lastErr = sanitizeConnectionError(connectionErr, config)
		}
		server.mu.Unlock()
	})
	_, err := cli.Initialize(opCtx, mcpgo.InitializeRequest{Params: mcpgo.InitializeParams{ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION, ClientInfo: mcpgo.Implementation{Name: "friday", Version: "1"}, Capabilities: mcpgo.ClientCapabilities{}}})
	if err != nil {
		connCancel()
		_ = cli.Close()
		return nil, nil, nil, fmt.Errorf("initialize MCP server %q: %w", name, err)
	}
	listed, err := cli.ListTools(opCtx, mcpgo.ListToolsRequest{})
	if err != nil {
		connCancel()
		_ = cli.Close()
		return nil, nil, nil, fmt.Errorf("list tools from MCP server %q: %w", name, err)
	}
	definitions := make([]cachedDefinition, 0, len(listed.Tools))
	for i := range listed.Tools {
		tool := &listed.Tools[i]
		if !config.allowedTool(tool.Name) {
			continue
		}
		raw := tool.RawInputSchema
		if len(raw) == 0 {
			raw, _ = json.Marshal(tool.InputSchema)
		}
		definitions = append(definitions, cachedDefinition{Name: tool.Name, Description: tool.Description, InputSchema: append(json.RawMessage(nil), raw...)})
	}
	return cli, connCancel, definitions, nil
}

func (m *Manager) adaptCached(serverName string, definitions []cachedDefinition) []*tools.Tool {
	result := make([]*tools.Tool, 0, len(definitions))
	for _, definition := range definitions {
		original := definition.Name
		converted := covertMCPTool(&mcpgo.Tool{Name: original, Description: definition.Description, RawInputSchema: definition.InputSchema})
		converted.Name = namespacedTool(serverName, original)
		converted.Handler = func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
			return m.call(ctx, serverName, original, request)
		}
		result = append(result, converted)
	}
	return result
}

func (m *Manager) call(ctx context.Context, serverName, toolName string, request *tools.Request) (*tools.Result, error) {
	if err := m.ensureReady(ctx, serverName, false); err != nil {
		return nil, err
	}
	server := m.server(serverName)
	server.mu.Lock()
	cli := server.client
	server.mu.Unlock()
	if cli == nil {
		return nil, fmt.Errorf("MCP server %q is not connected", serverName)
	}
	var arguments map[string]interface{}
	if request != nil {
		arguments = request.Arguments
	}
	result, err := cli.CallTool(ctx, mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{Name: toolName, Arguments: arguments}})
	if err != nil {
		return nil, sanitizeConnectionError(fmt.Errorf("MCP tool %s request failed: %w", toolName, err), server.config)
	}
	if result == nil {
		return nil, fmt.Errorf("MCP tool %s returned no response", toolName)
	}
	return convertMCPResult(toolName, result), nil
}

func (m *Manager) scheduleToolRefresh(name string) {
	server := m.server(name)
	if server == nil {
		return
	}
	server.mu.Lock()
	if server.refreshing {
		server.mu.Unlock()
		return
	}
	server.refreshing = true
	server.mu.Unlock()
	go func() {
		defer func() { server.mu.Lock(); server.refreshing = false; server.mu.Unlock() }()
		select {
		case <-time.After(200 * time.Millisecond):
		case <-m.ctx.Done():
			return
		}
		server.mu.Lock()
		cli := server.client
		config := server.config
		server.mu.Unlock()
		if cli != nil {
			ctx, cancel := context.WithTimeout(m.ctx, m.timeout)
			listed, err := cli.ListTools(ctx, mcpgo.ListToolsRequest{})
			cancel()
			if err == nil && m.ctx.Err() == nil {
				defs := make([]cachedDefinition, 0, len(listed.Tools))
				for i := range listed.Tools {
					tool := &listed.Tools[i]
					if !config.allowedTool(tool.Name) {
						continue
					}
					raw := tool.RawInputSchema
					if len(raw) == 0 {
						raw, _ = json.Marshal(tool.InputSchema)
					}
					defs = append(defs, cachedDefinition{Name: tool.Name, Description: tool.Description, InputSchema: raw})
				}
				server.mu.Lock()
				server.tools = m.adaptCached(name, defs)
				server.state = StateReady
				server.lastErr = nil
				server.mu.Unlock()
				_ = m.cache.Put(context.Background(), "mcp", cacheKey(config), cachedToolSet{Tools: defs}, m.cacheTTL)
			}
		}
	}()
}

func expandedMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = expand(value)
	}
	return result
}

func sanitizeConnectionError(err error, config ServerConfig) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	expandedURL := expand(config.URL)
	if parsed, parseErr := url.Parse(expandedURL); parseErr == nil && expandedURL != "" {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		message = strings.ReplaceAll(message, expandedURL, parsed.String())
	}
	for _, values := range []map[string]string{config.Headers, config.Env} {
		for _, value := range values {
			if secret := expand(value); secret != "" {
				message = strings.ReplaceAll(message, secret, "[redacted]")
			}
		}
	}
	return errors.New(message)
}
func (m *Manager) server(name string) *managedServer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.servers[name]
}
func (m *Manager) names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var unsafeToolName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func safePart(value string) string {
	value = unsafeToolName.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "unnamed"
	}
	return value
}
func namespacedTool(server, tool string) string {
	return "mcp__" + safePart(server) + "__" + safePart(tool)
}
func cacheKey(config ServerConfig) string {
	digest := config.Digest
	if len(digest) > 16 {
		digest = digest[:16]
	}
	return safePart(config.Name) + "_" + digest
}

// ConfigSourceSummary is useful to diagnostic frontends without exposing
// environment values, headers or command arguments.
func (m *Manager) ConfigSourceSummary(name string) (string, error) {
	status, err := m.Inspect(name)
	if err != nil {
		return "", err
	}
	return filepath.Clean(status.Source), nil
}
