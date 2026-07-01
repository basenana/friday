package a2a

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"github.com/a2aproject/a2a-go/a2asrv/eventqueue"

	"github.com/basenana/friday/actor"
)

// --- Agent Card Tests ---

func TestAgentCardContainsChatSkill(t *testing.T) {
	card := NewAgentCard(Config{BaseURL: "http://127.0.0.1:8999/"})

	if card.Name != "Friday" {
		t.Errorf("expected name Friday, got %q", card.Name)
	}
	if card.URL != "http://127.0.0.1:8999/" {
		t.Errorf("expected URL http://127.0.0.1:8999/, got %q", card.URL)
	}
	if !card.Capabilities.Streaming {
		t.Error("expected streaming capability to be true")
	}
	if len(card.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(card.Skills))
	}
	if card.Skills[0].ID != "chat" {
		t.Errorf("expected skill ID 'chat', got %q", card.Skills[0].ID)
	}
	if card.PreferredTransport != a2a.TransportProtocolJSONRPC {
		t.Errorf("expected JSONRPC transport, got %q", card.PreferredTransport)
	}
}

func TestAgentCardInputOutputModes(t *testing.T) {
	card := NewAgentCard(Config{BaseURL: "http://localhost:9999/"})

	if len(card.DefaultInputModes) != 1 || card.DefaultInputModes[0] != "text/plain" {
		t.Errorf("expected input modes [text/plain], got %v", card.DefaultInputModes)
	}
	if len(card.DefaultOutputModes) != 1 || card.DefaultOutputModes[0] != "text/plain" {
		t.Errorf("expected output modes [text/plain], got %v", card.DefaultOutputModes)
	}
}

// --- Well-Known Endpoint Test ---

func TestWellKnownAgentCardEndpoint(t *testing.T) {
	cfg := Config{BaseURL: "http://127.0.0.1:8999/", Listen: "127.0.0.1:0"}
	card := NewAgentCard(cfg)

	executor := &fakeExecutor{response: "hello"}
	handler := a2asrv.NewHandler(executor)

	mux := http.NewServeMux()
	mux.Handle("/", a2asrv.NewJSONRPCHandler(handler))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/.well-known/agent-card.json")
	if err != nil {
		t.Fatalf("GET agent card: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var gotCard a2a.AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&gotCard); err != nil {
		t.Fatalf("decode agent card: %v", err)
	}

	if gotCard.Name != "Friday" {
		t.Errorf("expected card name Friday, got %q", gotCard.Name)
	}
	if !gotCard.Capabilities.Streaming {
		t.Error("expected streaming in card")
	}
}

func TestAgentCardVersionNotHardcoded(t *testing.T) {
	card := NewAgentCard(Config{BaseURL: "http://127.0.0.1:8999/"})
	// moduleVersion should return a non-empty string (at least "dev")
	if card.Version == "" {
		t.Error("expected non-empty version")
	}
}

// --- Auth Middleware Tests ---

func TestAuthMiddlewareRejectsNoToken(t *testing.T) {
	protected := authMiddleware("secret", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach handler")
	}))

	req := httptest.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddlewareRejectsWrongToken(t *testing.T) {
	protected := authMiddleware("secret", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach handler")
	}))

	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddlewareAcceptsCorrectToken(t *testing.T) {
	called := false
	protected := authMiddleware("secret", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, req)

	if !called {
		t.Error("expected handler to be called")
	}
}

// --- Cancel Propagation Test ---

func TestFridayExecutorCancelShutsDownActor(t *testing.T) {
	registry := newFakeRegistry(4)
	executor := newFridayExecutor(registry)

	taskID := a2a.TaskID("cancel-prop-test")
	reqCtx := &a2asrv.RequestContext{TaskID: taskID, ContextID: "ctx-1"}
	queue := newTestQueue()

	if err := executor.Cancel(context.Background(), reqCtx, queue); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	events := queue.events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event from cancel, got %d", len(events))
	}
	statusUpdate, ok := events[0].(*a2a.TaskStatusUpdateEvent)
	if !ok {
		t.Fatalf("expected TaskStatusUpdateEvent, got %T", events[0])
	}
	if statusUpdate.Status.State != a2a.TaskStateCanceled {
		t.Errorf("expected canceled state, got %q", statusUpdate.Status.State)
	}
	if !registry.wasShutdown(string(taskID)) {
		t.Fatalf("expected registry shutdown for task %q", taskID)
	}
}

// --- Extract Text Helper Tests ---

func TestExtractTextFromMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  *a2a.Message
		want string
	}{
		{
			name: "nil message",
			msg:  nil,
			want: "",
		},
		{
			name: "single text part",
			msg:  a2a.NewMessage(a2a.MessageRoleUser, a2a.TextPart{Text: "hello"}),
			want: "hello",
		},
		{
			name: "multiple text parts",
			msg: a2a.NewMessage(a2a.MessageRoleUser,
				a2a.TextPart{Text: "line1"},
				a2a.TextPart{Text: "line2"},
			),
			want: "line1\nline2",
		},
		{
			name: "empty message",
			msg:  a2a.NewMessage(a2a.MessageRoleUser),
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTextFromMessage(tt.msg)
			if got != tt.want {
				t.Errorf("extractTextFromMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Executor Integration Tests ---

func TestFridayExecutorExecuteCompleted(t *testing.T) {
	registry := newFakeRegistry(8)
	registry.actor.send = func(msg actor.Message) bool {
		go func() {
			registry.events <- actor.Event{Type: actor.EventTextMessageContent, Data: map[string]any{"delta": "Hello "}}
			registry.events <- actor.Event{Type: actor.EventTextMessageContent, Data: map[string]any{"delta": "Friday"}}
			registry.events <- actor.Event{Type: actor.EventRunFinished, Data: map[string]any{"stop_reason": "end_turn"}}
		}()
		return msg.Content == "hi"
	}

	executor := newFridayExecutor(registry)
	reqCtx := &a2asrv.RequestContext{
		TaskID:    "test-exec-1",
		ContextID: "ctx-exec-1",
		Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.TextPart{Text: "hi"}),
	}
	queue := newTestQueue()

	err := executor.Execute(context.Background(), reqCtx, queue)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	events := queue.events()
	if len(events) == 0 {
		t.Fatal("expected events from executor")
	}

	last := events[len(events)-1]
	statusUpdate, ok := last.(*a2a.TaskStatusUpdateEvent)
	if !ok {
		t.Fatalf("expected final TaskStatusUpdateEvent, got %T", last)
	}
	if statusUpdate.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("expected completed state, got %q", statusUpdate.Status.State)
	}
	if statusUpdate.Status.Message == nil {
		t.Fatal("expected final response message")
	}
	if got := extractTextFromMessage(statusUpdate.Status.Message); got != "Hello Friday" {
		t.Fatalf("unexpected final message: %q", got)
	}

	var artifactCount int
	for _, evt := range events {
		if _, ok := evt.(*a2a.TaskArtifactUpdateEvent); ok {
			artifactCount++
		}
	}
	if artifactCount != 2 {
		t.Fatalf("expected 2 artifact events, got %d", artifactCount)
	}
}

func TestFridayExecutorExecuteFailed(t *testing.T) {
	registry := newFakeRegistry(4)
	registry.actor.send = func(msg actor.Message) bool {
		go func() {
			registry.events <- actor.Event{Type: actor.EventRunError, Data: map[string]any{"message": "boom"}}
			registry.events <- actor.Event{Type: actor.EventRunFinished, Data: map[string]any{"stop_reason": "error"}}
		}()
		return true
	}

	executor := newFridayExecutor(registry)
	reqCtx := &a2asrv.RequestContext{TaskID: "test-fail-1", ContextID: "ctx-fail-1"}
	queue := newTestQueue()

	err := executor.Execute(context.Background(), reqCtx, queue)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	events := queue.events()
	if len(events) == 0 {
		t.Fatal("expected failure events")
	}

	last := events[len(events)-1]
	statusUpdate, ok := last.(*a2a.TaskStatusUpdateEvent)
	if !ok {
		t.Fatalf("expected TaskStatusUpdateEvent, got %T", last)
	}
	if statusUpdate.Status.State != a2a.TaskStateFailed {
		t.Fatalf("expected failed state, got %q", statusUpdate.Status.State)
	}
	if got := extractTextFromMessage(statusUpdate.Status.Message); got != "boom" {
		t.Fatalf("unexpected failure message: %q", got)
	}
}

func TestFridayExecutorExecuteCanceledOnContextDone(t *testing.T) {
	registry := newFakeRegistry(2)
	registry.actor.send = func(msg actor.Message) bool {
		return true
	}

	executor := newFridayExecutor(registry)
	reqCtx := &a2asrv.RequestContext{TaskID: "test-cancel-1", ContextID: "ctx-cancel-1"}
	queue := newTestQueue()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := executor.Execute(ctx, reqCtx, queue)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	events := queue.events()
	if len(events) == 0 {
		t.Fatal("expected canceled terminal event")
	}

	last := events[len(events)-1]
	statusUpdate, ok := last.(*a2a.TaskStatusUpdateEvent)
	if !ok {
		t.Fatalf("expected TaskStatusUpdateEvent, got %T", last)
	}
	if statusUpdate.Status.State != a2a.TaskStateCanceled {
		t.Fatalf("expected canceled state, got %q", statusUpdate.Status.State)
	}
	if !registry.wasShutdown(string(reqCtx.TaskID)) {
		t.Fatalf("expected shutdown for task %q", reqCtx.TaskID)
	}
}

// --- JSON-RPC Protocol Test ---

func TestJSONRPCSendMessage(t *testing.T) {
	executor := &fakeExecutor{response: "response text"}
	handler := a2asrv.NewHandler(executor)
	card := NewAgentCard(Config{BaseURL: "http://127.0.0.1:8999/"})

	mux := http.NewServeMux()
	mux.Handle("/", a2asrv.NewJSONRPCHandler(handler))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))

	server := httptest.NewServer(mux)
	defer server.Close()

	// Send a message/send JSON-RPC request
	body := `{
		"jsonrpc": "2.0",
		"method": "message/send",
		"id": 1,
		"params": {
			"message": {
				"messageId": "test-msg-001",
				"role": "user",
				"parts": [{"kind": "text", "text": "Hello!"}]
			}
		}
	}`

	resp, err := http.Post(server.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST SendMessage: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if _, ok := result["result"]; !ok {
		t.Errorf("expected 'result' in response, got %v", result)
	}
}

// --- Test Helpers ---

// fakeExecutor is a test double for a2asrv.AgentExecutor that emits a simple text response.
type fakeExecutor struct {
	response string
}

var _ a2asrv.AgentExecutor = (*fakeExecutor)(nil)

func (e *fakeExecutor) Execute(ctx context.Context, reqCtx *a2asrv.RequestContext, queue eventqueue.Queue) error {
	if reqCtx.StoredTask == nil {
		if err := queue.Write(ctx, a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateSubmitted, nil)); err != nil {
			return err
		}
	}
	if err := queue.Write(ctx, a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateWorking, nil)); err != nil {
		return err
	}

	msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.TextPart{Text: e.response})
	return queue.Write(ctx, msg)
}

func (e *fakeExecutor) Cancel(ctx context.Context, reqCtx *a2asrv.RequestContext, queue eventqueue.Queue) error {
	return queue.Write(ctx, a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateCanceled, nil))
}

// testQueue is a simple in-memory event queue for testing.
type testQueue struct {
	mu     sync.Mutex
	items  []a2a.Event
	closed bool
}

func newTestQueue() *testQueue {
	return &testQueue{}
}

func (q *testQueue) Read(ctx context.Context) (a2a.Event, a2a.TaskVersion, error) {
	return nil, a2a.TaskVersionMissing, io.EOF
}

func (q *testQueue) Write(ctx context.Context, event a2a.Event) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, event)
	return nil
}

func (q *testQueue) WriteVersioned(ctx context.Context, event a2a.Event, version a2a.TaskVersion) error {
	return q.Write(ctx, event)
}

func (q *testQueue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	return nil
}

func (q *testQueue) events() []a2a.Event {
	q.mu.Lock()
	defer q.mu.Unlock()
	result := make([]a2a.Event, len(q.items))
	copy(result, q.items)
	return result
}

type fakeRegistry struct {
	actor  *fakeActorSession
	events chan actor.Event

	mu          sync.Mutex
	shutdownIDs []string
}

func newFakeRegistry(buffer int) *fakeRegistry {
	return &fakeRegistry{
		actor:  &fakeActorSession{},
		events: make(chan actor.Event, buffer),
	}
}

func (r *fakeRegistry) GetOrCreate(sessionID string) actorSession {
	return r.actor
}

func (r *fakeRegistry) Subscribe(sessionID string, buffer int) (<-chan actor.Event, func(), error) {
	return r.events, func() {}, nil
}

func (r *fakeRegistry) Shutdown(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shutdownIDs = append(r.shutdownIDs, sessionID)
}

func (r *fakeRegistry) ShutdownAll() {}

func (r *fakeRegistry) wasShutdown(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range r.shutdownIDs {
		if id == sessionID {
			return true
		}
	}
	return false
}

type fakeActorSession struct {
	send func(actor.Message) bool
}

func (a *fakeActorSession) Send(msg actor.Message) bool {
	if a.send == nil {
		return true
	}
	return a.send(msg)
}
