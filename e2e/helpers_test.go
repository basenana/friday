//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basenana/friday/actor"
	a2apkg "github.com/basenana/friday/a2a"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sandbox"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/sessions/file"
	"github.com/basenana/friday/setup"
)

// ---------------------------------------------------------------------------
// Config / client construction
// ---------------------------------------------------------------------------

// loadConfig finds and loads the e2e config, skipping the test if absent.
func loadConfig(t *testing.T) *E2EConfig {
	t.Helper()
	path := FindE2EConfig()
	if path == "" {
		t.Skip("e2e config not found; set E2E_CONFIG or create .local/e2e.yaml")
	}
	cfg, err := LoadE2EConfig(path)
	if err != nil {
		t.Fatalf("load e2e config %s: %v", path, err)
	}
	return cfg
}

// mustModel returns the named model config or skips if missing.
func mustModel(t *testing.T, cfg *E2EConfig, name string) config.ModelConfig {
	t.Helper()
	m, ok := cfg.Models[name]
	if !ok || !m.IsConfigured() {
		t.Skipf("model %q not configured in e2e config", name)
	}
	return m
}

// newClient builds a real providers.Client from a named model.
func newClient(t *testing.T, cfg *E2EConfig, modelName string) providers.Client {
	t.Helper()
	m := mustModel(t, cfg, modelName)
	c, err := setup.CreateProviderClientFromModel(m)
	if err != nil {
		t.Fatalf("create provider client for %s: %v", modelName, err)
	}
	return c
}

// fridayConfig builds a *config.Config suitable for setup.NewAgent / actor,
// using the named e2e model as the primary model.
func fridayConfig(t *testing.T, cfg *E2EConfig, modelName string) *config.Config {
	t.Helper()
	m := mustModel(t, cfg, modelName)
	c := config.DefaultConfig()
	c.Model = m
	if img, ok := cfg.Models["image"]; ok && img.IsConfigured() {
		c.ImageModel = img
	}
	c.Sandbox = sandboxConfig(cfg)
	c.DataDir = t.TempDir()
	c.Workspace = filepath.Join(c.DataDir, "workspace")
	c.Memory.Enabled = false
	return c
}

// sandboxConfig builds a *sandbox.Config from the e2e toggle, with a
// permissive default suitable for tests.
func sandboxConfig(cfg *E2EConfig) *sandbox.Config {
	sc := sandbox.DefaultConfig()
	sc.Sandbox.Enabled = cfg.Sandbox.Enabled
	// Allow common dev utilities used in tests.
	return sc
}

// ---------------------------------------------------------------------------
// Executor / tools
// ---------------------------------------------------------------------------

// newExecutor builds a sandbox Executor honouring the e2e sandbox toggle.
func newExecutor(t *testing.T, cfg *E2EConfig) *sandbox.Executor {
	t.Helper()
	return sandbox.NewExecutor(sandboxConfig(cfg))
}

// newBashFsTools returns bash + fs tools bound to workdir.
func newBashFsTools(t *testing.T, exec *sandbox.Executor, workdir string) []*tools.Tool {
	t.Helper()
	out := []*tools.Tool{sandbox.NewBashTool(exec, workdir)}
	out = append(out, sandbox.NewFsTools(exec, workdir)...)
	return out
}

// newAllTools returns bash + fs + background_task + image tools.
// The image tool uses a real provider-backed analyzer built from the
// "image" model (or skips if not configured).
func newAllTools(t *testing.T, cfg *E2EConfig, exec *sandbox.Executor, workdir string) []*tools.Tool {
	t.Helper()
	out := newBashFsTools(t, exec, workdir)
	tm := sandbox.NewTaskManager(exec)
	out = append(out, sandbox.NewBackgroundTaskTools(tm, workdir)...)

	if img, ok := cfg.Models["image"]; ok && img.IsConfigured() {
		client, err := setup.CreateProviderClientFromModel(img)
		if err != nil {
			t.Fatalf("create image client: %v", err)
		}
		analyzer := &providerImageAnalyzer{client: client}
		out = append(out, sandbox.NewImageTool(exec, workdir, analyzer))
	}
	return out
}

// providerImageAnalyzer adapts a providers.Client to sandbox.ImageAnalyzer.
type providerImageAnalyzer struct {
	client providers.Client
}

func (a *providerImageAnalyzer) Analyze(ctx context.Context, prompt, modelOverride string, image *types.ImageContent) (string, error) {
	sysPrompt := "You are an image understanding assistant. Analyze the image and answer concisely."
	req := providers.NewRequest(sysPrompt, types.Message{Role: types.RoleUser, Content: prompt, Image: image})
	stream := a.client.Completion(ctx, req)
	var buf bytes.Buffer
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case err := <-stream.Error():
			if err != nil {
				return "", err
			}
		case d, ok := <-stream.Message():
			if !ok {
				return strings.TrimSpace(buf.String()), nil
			}
			buf.WriteString(d.Content)
		}
	}
}

// ---------------------------------------------------------------------------
// Session / agent construction
// ---------------------------------------------------------------------------

// newTestSession creates an in-memory session (no persistence).
func newTestSession(t *testing.T, client providers.Client) *session.Session {
	t.Helper()
	return session.New(types.NewID(), client)
}

// newPersistentSession creates a session backed by a file store in dir.
// Returns the session and its ID.
func newPersistentSession(t *testing.T, client providers.Client, dir string) (*session.Session, string) {
	t.Helper()
	store := file.NewFileSessionStore(dir)
	id := types.NewID()
	sess := session.New(id, client, session.WithMessageWriter(store))
	return sess, id
}

// newSessionManager builds a sessions.Manager backed by a file store in dir.
func newSessionManager(t *testing.T, dir string) *sessions.Manager {
	t.Helper()
	store := file.NewFileSessionStore(filepath.Join(dir, "sessions"))
	cur := filepath.Join(dir, "current")
	mgr := sessions.NewManager(store, cur, "e2e-tty")
	return mgr
}

type agentOpts struct {
	SystemPrompt string
	Tools        []*tools.Tool
	MaxLoops     int
	Hooks        []session.Hook
}

// newReactAgent constructs a ReAct agent with sensible test defaults.
func newReactAgent(t *testing.T, client providers.Client, opts agentOpts) agents.Agent {
	t.Helper()
	if opts.MaxLoops == 0 {
		opts.MaxLoops = 20
	}
	return agents.New(client, agents.Option{
		SystemPrompt: opts.SystemPrompt,
		Tools:        opts.Tools,
		MaxLoopTimes: opts.MaxLoops,
	})
}

// newAgentWithTools is the all-in-one helper: client + executor + bash/fs
// tools + session + workdir. Returns (agent, session, workdir).
func newAgentWithTools(t *testing.T, cfg *E2EConfig, modelName string) (agents.Agent, *session.Session, string) {
	t.Helper()
	client := newClient(t, cfg, modelName)
	workdir := t.TempDir()
	exec := newExecutor(t, cfg)
	allTools := newBashFsTools(t, exec, workdir)
	sess := newTestSession(t, client)
	agent := newReactAgent(t, client, agentOpts{Tools: allTools, MaxLoops: 20})
	return agent, sess, workdir
}

// newAgentCtxWithTools builds a full setup.AgentContext (used by actor/a2a).
// Returns (agentCtx, workdir, cleanup).
func newAgentCtxWithTools(t *testing.T, cfg *E2EConfig, modelName string) (*setup.AgentContext, string, func()) {
	t.Helper()
	fc := fridayConfig(t, cfg, modelName)
	dir := fc.DataDir
	mgr := newSessionManager(t, dir)
	mgr.SetLLM(newClient(t, cfg, modelName))
	ac, err := setup.NewAgent(mgr, fc, setup.WithIsolate(true))
	if err != nil {
		t.Fatalf("setup.NewAgent: %v", err)
	}
	return ac, dir, func() { ac.Close() }
}

// ---------------------------------------------------------------------------
// Response collection
// ---------------------------------------------------------------------------

// collectResponse drains an *api.Response, returning the full content and
// all deltas observed. Fails on stream error.
func collectResponse(t *testing.T, ctx context.Context, resp *api.Response) (string, []types.Delta) {
	t.Helper()
	var (
		buf    strings.Builder
		deltas []types.Delta
	)
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("collect response: context deadline: %v", ctx.Err())
		case err := <-resp.Error():
			if err != nil {
				t.Fatalf("response stream error: %v", err)
			}
		case d, ok := <-resp.Deltas():
			if !ok {
				return buf.String(), deltas
			}
			deltas = append(deltas, d)
			buf.WriteString(d.Content)
		}
	}
}

// chatOnce sends a single message and returns the full content.
func chatOnce(t *testing.T, ctx context.Context, agent agents.Agent, sess *session.Session, msg string) string {
	t.Helper()
	resp := agent.Chat(ctx, &api.Request{Session: sess, UserMessage: msg})
	content, _ := collectResponse(t, ctx, resp)
	return content
}

// chatMulti sends several messages sequentially and returns each response.
func chatMulti(t *testing.T, ctx context.Context, agent agents.Agent, sess *session.Session, msgs []string) []string {
	t.Helper()
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, chatOnce(t, ctx, agent, sess, m))
	}
	return out
}

// ---------------------------------------------------------------------------
// Assertions (stdlib only)
// ---------------------------------------------------------------------------

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected %q to contain %q", truncate(s, 500), substr)
	}
}

func assertContainsAny(t *testing.T, s string, substrs ...string) {
	t.Helper()
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return
		}
	}
	t.Errorf("expected %q to contain at least one of %v", truncate(s, 500), substrs)
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()
	if strings.TrimSpace(s) == "" {
		t.Error("expected non-empty string")
	}
}

func assertMatch(t *testing.T, s, pattern string) {
	t.Helper()
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("invalid regex %q: %v", pattern, err)
	}
	if !re.MatchString(s) {
		t.Errorf("expected %q to match %q", truncate(s, 500), pattern)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file %s to exist: %v", path, err)
	}
}

func assertFileContent(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != expected {
		t.Errorf("file %s content = %q, want %q", path, truncate(string(data), 500), truncate(expected, 500))
	}
}

func assertFileContains(t *testing.T, path, substr string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(data), substr) {
		t.Errorf("file %s content %q does not contain %q", path, truncate(string(data), 500), substr)
	}
}

// assertHistoryHasToolCall checks that session history contains at least one
// tool call with the given tool name.
func assertHistoryHasToolCall(t *testing.T, sess *session.Session, toolName string) {
	t.Helper()
	if !historyHasToolCall(sess, toolName, 1) {
		t.Errorf("expected session history to contain a %q tool call", toolName)
	}
}

// assertHistoryHasToolCallN checks that session history contains at least
// minCount tool calls with the given tool name.
func assertHistoryHasToolCallN(t *testing.T, sess *session.Session, toolName string, minCount int) {
	t.Helper()
	if !historyHasToolCall(sess, toolName, minCount) {
		t.Errorf("expected session history to contain >=%d %q tool calls", minCount, toolName)
	}
}

func historyHasToolCall(sess *session.Session, toolName string, minCount int) bool {
	count := 0
	for _, msg := range sess.GetHistory() {
		for _, tc := range msg.ToolCalls {
			if tc.Name == toolName {
				count++
			}
		}
	}
	return count >= minCount
}

// assertEventReceived checks that a slice of core events contains eventType.
func assertEventReceived(t *testing.T, events []types.Event, eventType types.EventType) {
	t.Helper()
	for _, e := range events {
		if e.Type == eventType {
			return
		}
	}
	t.Errorf("expected event %q in %d events", eventType, len(events))
}

// assertActorEvent checks that a slice of actor events contains eventType.
func assertActorEvent(t *testing.T, events []actor.Event, eventType actor.EventType) {
	t.Helper()
	for _, e := range events {
		if e.Type == eventType {
			return
		}
	}
	t.Errorf("expected actor event %q in %d events", eventType, len(events))
}

// ---------------------------------------------------------------------------
// Retry
// ---------------------------------------------------------------------------

// withRetry retries fn up to cfg.MaxAttempts(). fn should return nil on
// success. Only the final attempt's error is fatal.
func withRetry(t *testing.T, cfg *E2EConfig, fn func(attempt int) error) {
	t.Helper()
	max := cfg.MaxAttempts()
	backoff := cfg.BackoffDuration()
	var lastErr error
	for i := 1; i <= max; i++ {
		lastErr = fn(i)
		if lastErr == nil {
			return
		}
		t.Logf("attempt %d/%d failed: %v", i, max, lastErr)
		if i < max {
			time.Sleep(backoff)
		}
	}
	if lastErr != nil {
		t.Fatalf("all %d attempts failed; last error: %v", max, lastErr)
	}
}

// ---------------------------------------------------------------------------
// Event subscription
// ---------------------------------------------------------------------------

// subscribeSessionEvents subscribes to core session events and collects them
// into a slice. Returns a pointer to the collected events, a pointer to the
// mutex guarding it, and an unsubscribe function.
func subscribeSessionEvents(t *testing.T, sess *session.Session) (*[]types.Event, *sync.Mutex, func()) {
	t.Helper()
	ch, unsub := sess.SubscribeEvents()
	var (
		mu     sync.Mutex
		events []types.Event
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for evt := range ch {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
		}
	}()
	cleanup := func() {
		unsub()
		<-done
	}
	return &events, &mu, cleanup
}

func getEvents(ptr *[]types.Event, mu *sync.Mutex) []types.Event {
	mu.Lock()
	defer mu.Unlock()
	cp := make([]types.Event, len(*ptr))
	copy(cp, *ptr)
	return cp
}

// ---------------------------------------------------------------------------
// Actor helpers
// ---------------------------------------------------------------------------

// collectActorEvents drains the actor's Outcome channel until ctx is done,
// the actor is shut down, stop is called, or a RunFinished event is observed.
// Returns the collected events.
func collectActorEvents(ctx context.Context, act *actor.Actor, stop <-chan struct{}) []actor.Event {
	var events []actor.Event
	for {
		select {
		case <-ctx.Done():
			return events
		case <-stop:
			return events
		case evt, ok := <-act.Outcome():
			if !ok {
				return events
			}
			events = append(events, evt)
			if evt.Type == actor.EventRunFinished {
				return events
			}
		}
	}
}

// ---------------------------------------------------------------------------
// A2A server helpers
// ---------------------------------------------------------------------------

// startA2AServer starts an A2A server on a random port using the given
// friday config and auth token. Returns (serverURL, shutdown).
func startA2AServer(t *testing.T, fc *config.Config, sessMgr setup.SessionManager, authToken string) (string, func()) {
	t.Helper()
	registry := a2apkg.NewRegistry(fc, sessMgr)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cfg := a2apkg.Config{
		BaseURL: "http://" + addr + "/",
		Listen:  addr,
	}
	server, err := a2apkg.NewServer(cfg, registry, authToken)
	if err != nil {
		t.Fatalf("new a2a server: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Start()
	}()
	// Wait until the port is reachable.
	url := "http://" + addr + "/"
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		<-done
	}
	_ = url
	return "http://" + addr + "/", cleanup
}

// a2aMessageSend sends a message/send JSON-RPC request.
// Returns (statusCode, parsedResponseBody).
func a2aMessageSend(t *testing.T, serverURL, authToken, taskID, text string) (int, map[string]any) {
	t.Helper()
	body := fmt.Sprintf(`{
		"jsonrpc": "2.0",
		"method": "message/send",
		"id": 1,
		"params": {
			"message": {
				"messageId": "%s",
				"role": "user",
				"parts": [{"kind": "text", "text": %s}]
			}
		}
	}`, taskID, jsonStr(text))
	return a2aPost(t, serverURL, authToken, body)
}

// a2aTaskCancel sends a tasks/cancel JSON-RPC request.
func a2aTaskCancel(t *testing.T, serverURL, authToken, taskID string) (int, map[string]any) {
	t.Helper()
	body := fmt.Sprintf(`{
		"jsonrpc": "2.0",
		"method": "tasks/cancel",
		"id": 2,
		"params": {"taskId": "%s"}
	}`, taskID)
	return a2aPost(t, serverURL, authToken, body)
}

func a2aPost(t *testing.T, serverURL, authToken, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest("POST", serverURL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed map[string]any
	_ = json.Unmarshal(raw, &parsed)
	return resp.StatusCode, parsed
}

// a2aGetAgentCard fetches the agent card JSON.
func a2aGetAgentCard(t *testing.T, serverURL string) map[string]any {
	t.Helper()
	resp, err := http.Get(serverURL + ".well-known/agent-card.json")
	if err != nil {
		t.Fatalf("get agent card: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse agent card: %v\nbody: %s", err, string(raw))
	}
	return parsed
}

// ---------------------------------------------------------------------------
// Image generation
// ---------------------------------------------------------------------------

// makeTestPNG returns a path to a PNG file containing a large letter "A"
// on a white background. The file is written into the test's temp dir.
// We use a simple geometric rendering rather than text (to avoid a font
// dependency) so the image is recognisable as a distinct shape.
func makeTestPNG(t *testing.T) string {
	t.Helper()
	const (
		w = 128
		h = 128
	)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// White background.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	// Draw a large red filled circle in the centre — easy for vision models
	// to identify.
	cx, cy, r := w/2, h/2, 40
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx := x - cx
			dy := y - cy
			if dx*dx+dy*dy <= r*r {
				img.Set(x, y, color.RGBA{R: 220, G: 0, B: 0, A: 255})
			}
		}
	}

	path := filepath.Join(t.TempDir(), "sample.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create sample png: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return path
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ---------------------------------------------------------------------------
// Event waiting helpers
// ---------------------------------------------------------------------------

// waitForEventType polls eventsPtr every 50ms until it contains an event of
// the given type or the timeout elapses. Returns true if found.
func waitForEventType(eventsPtr *[]types.Event, mu *sync.Mutex, eventType types.EventType, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			return false
		case <-time.After(50 * time.Millisecond):
			mu.Lock()
			for _, e := range *eventsPtr {
				if e.Type == eventType {
					mu.Unlock()
					return true
				}
			}
			mu.Unlock()
		}
	}
}

// ---------------------------------------------------------------------------
// Sandbox availability
// ---------------------------------------------------------------------------

// isBwrapAvailable reports whether the bwrap binary is available on PATH.
// Used by sandbox tests to skip when bwrap is missing. This is pure e2e
// framework code — the sandbox/ business package is never modified.
func isBwrapAvailable() bool {
	_, err := exec.LookPath("bwrap")
	return err == nil
}

// ---------------------------------------------------------------------------
// Deterministic failing client (for fallback edge-case tests)
// ---------------------------------------------------------------------------

// errAlwaysFail is the sentinel error returned by alwaysFailClient.
var errAlwaysFail = errors.New("always-fail-client: simulated retriable failure")

// alwaysFailClient is a providers.Client implementation whose every method
// returns a retriable error. Used by fallback edge-case tests so they don't
// need a "broken" model configured in e2e.yaml.
type alwaysFailClient struct {
	// delay, if non-zero, is slept before returning from each method.
	// Useful for verifying context cancellation behaviour.
	delay time.Duration
}

func newAlwaysFailClient() *alwaysFailClient  { return &alwaysFailClient{} }
func newDelayedFailClient(d time.Duration) *alwaysFailClient {
	return &alwaysFailClient{delay: d}
}

func (c *alwaysFailClient) sleep(ctx context.Context) error {
	if c.delay > 0 {
		select {
		case <-time.After(c.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (c *alwaysFailClient) Completion(ctx context.Context, _ providers.Request) providers.Response {
	r := providers.NewCommonResponse()
	go func() {
		defer close(r.Stream)
		defer close(r.Err)
		if err := c.sleep(ctx); err != nil {
			r.Err <- err
			return
		}
		r.Err <- errAlwaysFail
	}()
	return r
}

func (c *alwaysFailClient) CompletionNonStreaming(ctx context.Context, _ providers.Request) (string, error) {
	if err := c.sleep(ctx); err != nil {
		return "", err
	}
	return "", errAlwaysFail
}

func (c *alwaysFailClient) StructuredPredict(ctx context.Context, _ providers.Request, _ any) error {
	if err := c.sleep(ctx); err != nil {
		return err
	}
	return errAlwaysFail
}

// windowOverrideClient wraps a providers.Client and reports a pinned
// ContextWindow value. This is used by context-manager tests to force the
// compaction thresholds to be hit regardless of what the underlying provider
// reports (the production code at contextmgr/manager.go:343 prefers the
// provider's ContextWindow over the config value when it is > 0).
type windowOverrideClient struct {
	providers.Client
	window int64
}

// withContextWindow returns a wrapper that pins ContextWindow() to window.
func withContextWindow(c providers.Client, window int64) providers.Client {
	return &windowOverrideClient{Client: c, window: window}
}

func (w *windowOverrideClient) ContextWindow() int64 { return w.window }
