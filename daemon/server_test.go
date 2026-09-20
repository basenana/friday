package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	coreactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/sessions"
)

type fakeRegistry struct {
	bus            *eventbus.Bus
	mu             sync.Mutex
	got            []string
	getOrCreateErr error
}

func newFakeRegistry() *fakeRegistry { return &fakeRegistry{bus: eventbus.NewBus()} }

func (r *fakeRegistry) GetOrCreate(id string) (*coreactor.Actor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, id)
	return nil, r.getOrCreateErr
}

func (r *fakeRegistry) DispatchInput(env bus.Envelope) error {
	r.bus.Publish(bus.TopicInbox(env.Session), env)
	return nil
}

func (r *fakeRegistry) DispatchPreempt(env bus.Envelope) error {
	r.bus.Publish(bus.TopicPreempt(env.Session), env)
	return nil
}

func (r *fakeRegistry) Bus() *eventbus.Bus { return r.bus }
func (r *fakeRegistry) ShutdownAll()       {}

type fakeCatalog struct {
	mu       sync.Mutex
	next     string
	events   map[string][]events.Event
	existing map[string]bool
}

func newFakeCatalog(ids ...string) *fakeCatalog {
	c := &fakeCatalog{next: "new-session", events: make(map[string][]events.Event), existing: make(map[string]bool)}
	for _, id := range ids {
		c.existing[id] = true
	}
	return c
}

func (c *fakeCatalog) Create(context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.existing[c.next] = true
	return c.next, nil
}

func (c *fakeCatalog) Exists(id string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.existing[id], nil
}

func (c *fakeCatalog) LoadEvents(_ context.Context, id string) ([]events.Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]events.Event(nil), c.events[id]...), nil
}

type wireFrame struct {
	Version   int             `json:"version"`
	Type      string          `json:"type"`
	RequestID string          `json:"requestId,omitempty"`
	ThreadID  string          `json:"threadId,omitempty"`
	Replay    bool            `json:"replay,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

func newTestServer(t *testing.T, catalog *fakeCatalog) (*Server, *fakeRegistry, *httptest.Server) {
	t.Helper()
	registry := newFakeRegistry()
	server, err := NewServer(Config{}, registry, catalog)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return server, registry, httpServer
}

func dialTestServer(t *testing.T, server *httptest.Server, origin string) *websocket.Conn {
	t.Helper()
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "test complete") })
	return conn
}

func rawPayload(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeClientFrame(t *testing.T, conn *websocket.Conn, frame ClientFrame) {
	t.Helper()
	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatalf("write websocket frame: %v", err)
	}
}

func readWireFrame(t *testing.T, conn *websocket.Conn) wireFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	kind, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read websocket frame: %v", err)
	}
	if kind != websocket.MessageText {
		t.Fatalf("message kind = %v, want text", kind)
	}
	var frame wireFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	return frame
}

func subscribeSession(t *testing.T, conn *websocket.Conn, threadID, requestID string) {
	t.Helper()
	writeClientFrame(t, conn, ClientFrame{Version: ProtocolVersion, Type: TypeSessionSubscribe, RequestID: requestID, ThreadID: threadID})
	if got := readWireFrame(t, conn); got.Type != TypeHistoryBegin || got.ThreadID != threadID {
		t.Fatalf("history begin = %+v", got)
	}
	for {
		got := readWireFrame(t, conn)
		if got.Type == TypeHistoryEnd {
			break
		}
		if got.Type != TypeEvent || !got.Replay {
			t.Fatalf("history event = %+v", got)
		}
	}
	if got := readWireFrame(t, conn); got.Type != TypeResult || got.RequestID != requestID {
		t.Fatalf("subscribe result = %+v", got)
	}
}

func TestHandlerOnlyExposesWebSocketAndChecksOrigin(t *testing.T) {
	_, _, httpServer := newTestServer(t, newFakeCatalog())
	response, err := http.Get(httpServer.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("GET / status = %d, want 404", response.StatusCode)
	}

	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"https://evil.example"}}})
	if conn != nil {
		_ = conn.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("evil origin err/status = %v/%v, want 403", err, response)
	}

	local := dialTestServer(t, httpServer, "http://localhost:3000")
	writeClientFrame(t, local, ClientFrame{Version: ProtocolVersion, Type: TypeSessionCreate, RequestID: "create"})
	if got := readWireFrame(t, local); got.Type != TypeResult || got.ThreadID != "new-session" {
		t.Fatalf("create result = %+v", got)
	}
}

func TestSubscribeReportsActorStartFailureAndRemovesSubscription(t *testing.T) {
	_, registry, httpServer := newTestServer(t, newFakeCatalog("busy-session"))
	registry.getOrCreateErr = sessions.ErrEventWriterActive
	conn := dialTestServer(t, httpServer, "")

	writeClientFrame(t, conn, ClientFrame{Version: ProtocolVersion, Type: TypeSessionSubscribe, RequestID: "subscribe", ThreadID: "busy-session"})
	got := readWireFrame(t, conn)
	if got.Type != TypeError || got.RequestID != "subscribe" || got.ThreadID != "busy-session" {
		t.Fatalf("subscribe error frame = %+v", got)
	}
	var payload ErrorPayload
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != "actor_start_failed" || !strings.Contains(payload.Message, sessions.ErrEventWriterActive.Error()) {
		t.Fatalf("subscribe error payload = %+v", payload)
	}

	writeClientFrame(t, conn, ClientFrame{Version: ProtocolVersion, Type: TypeSessionUnsubscribe, RequestID: "unsubscribe", ThreadID: "busy-session"})
	got = readWireFrame(t, conn)
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if got.Type != TypeError || payload.Code != "not_subscribed" {
		t.Fatalf("unsubscribe after failed subscribe = frame %+v payload %+v", got, payload)
	}
}

func TestSubscribeReplaysHistoryThenForwardsMultipleSessions(t *testing.T) {
	catalog := newFakeCatalog("thread-one", "thread-two")
	historyEvent := events.NewEvent(events.KindTextMessageContent, "old-run").
		WithMessageID("old-message").WithPayload(events.TextMessageContentData{Content: "old"})
	historyEvent.CausedBy = []string{"old-input"}
	catalog.events["thread-one"] = []events.Event{historyEvent}
	_, registry, httpServer := newTestServer(t, catalog)
	conn := dialTestServer(t, httpServer, "")

	subscribeSession(t, conn, "thread-one", "sub-one")
	subscribeSession(t, conn, "thread-two", "sub-two")

	one := events.NewEvent(events.KindTextMessageContent, "run-one").WithPayload(events.TextMessageContentData{Content: "one"})
	two := events.NewEvent(events.KindRunStarted, "run-two").WithPayload(events.RunStartedData{})
	registry.bus.Publish(bus.TopicReplyContent("thread-one"), bus.Envelope{Event: one, Session: "thread-one"})
	registry.bus.Publish(bus.TopicRun("thread-two", "started"), bus.Envelope{Event: two, Session: "thread-two"})

	gotThreads := map[string]bool{}
	for len(gotThreads) < 2 {
		frame := readWireFrame(t, conn)
		if frame.Type == TypeEvent {
			gotThreads[frame.ThreadID] = true
		}
	}
	if !gotThreads["thread-one"] || !gotThreads["thread-two"] {
		t.Fatalf("routed threads = %v", gotThreads)
	}
}

func TestRunConsumesOnlyUnseenUserTailAndIsIdempotent(t *testing.T) {
	catalog := newFakeCatalog("thread-run")
	old := events.NewEvent(events.KindCustom, "old-run").WithName(events.CustomInputAccepted)
	old.CausedBy = []string{"old-user"}
	catalog.events["thread-run"] = []events.Event{old}
	_, registry, httpServer := newTestServer(t, catalog)
	conn := dialTestServer(t, httpServer, "")
	subscribeSession(t, conn, "thread-run", "sub")

	inbox := make(chan bus.Envelope, 2)
	listener := registry.bus.SubscribeSerial([]string{bus.TopicInbox("thread-run")}, func(env bus.Envelope) {
		inbox <- env
	}, eventbus.SerialConfig{Buffer: 4, Overflow: eventbus.OverflowBlock})
	defer registry.bus.Unsubscribe(listener)

	input := RunAgentInput{
		ThreadID: "thread-run",
		RunID:    "run-1",
		Messages: []AGUIMessage{
			{ID: "old-user", Role: "user", Content: rawPayload(t, "old")},
			{ID: "assistant", Role: "assistant", Content: rawPayload(t, "answer")},
			{ID: "new-user", Role: "user", Content: rawPayload(t, []any{
				map[string]any{"type": "text", "text": "new"},
				map[string]any{"type": "image", "source": map[string]any{"type": "url", "value": "https://example.test/image.png"}},
			})},
		},
	}
	frame := ClientFrame{Version: ProtocolVersion, Type: TypeRun, RequestID: "run-request", ThreadID: "thread-run", Payload: rawPayload(t, input)}
	writeClientFrame(t, conn, frame)
	if got := readWireFrame(t, conn); got.Type != TypeResult || got.RequestID != "run-request" {
		t.Fatalf("run result = %+v", got)
	}

	select {
	case env := <-inbox:
		if env.ID != "new-user" || env.Name != bus.InboxUserText {
			t.Fatalf("input envelope = %+v", env)
		}
		var payload bus.UserTextInput
		if err := events.DecodePayload(env.Event, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Text != "new" || payload.TurnID != "run-1" || len(payload.Images) != 1 {
			t.Fatalf("input payload = %+v", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("actor input was not published")
	}

	writeClientFrame(t, conn, ClientFrame{Version: ProtocolVersion, Type: TypeRun, RequestID: "duplicate", ThreadID: "thread-run", Payload: rawPayload(t, input)})
	if got := readWireFrame(t, conn); got.Type != TypeResult || !strings.Contains(string(got.Payload), `"duplicate":true`) {
		t.Fatalf("duplicate result = %+v", got)
	}
	select {
	case env := <-inbox:
		t.Fatalf("duplicate input was republished: %+v", env)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRunRejectsUnsupportedAGUIState(t *testing.T) {
	_, _, httpServer := newTestServer(t, newFakeCatalog("thread-state"))
	conn := dialTestServer(t, httpServer, "")
	subscribeSession(t, conn, "thread-state", "sub")
	input := RunAgentInput{ThreadID: "thread-state", RunID: "run", State: rawPayload(t, map[string]any{"value": true}),
		Messages: []AGUIMessage{{ID: "user", Role: "user", Content: rawPayload(t, "hello")}}}
	writeClientFrame(t, conn, ClientFrame{Version: ProtocolVersion, Type: TypeRun, RequestID: "run", ThreadID: "thread-state", Payload: rawPayload(t, input)})
	got := readWireFrame(t, conn)
	if got.Type != TypeError || !strings.Contains(string(got.Payload), "unsupported_feature") {
		t.Fatalf("unsupported state response = %+v", got)
	}
}

func TestRunRejectsDuplicateTrailingMessageIDs(t *testing.T) {
	_, _, httpServer := newTestServer(t, newFakeCatalog("thread-duplicate"))
	conn := dialTestServer(t, httpServer, "")
	subscribeSession(t, conn, "thread-duplicate", "sub")
	input := RunAgentInput{ThreadID: "thread-duplicate", RunID: "run", Messages: []AGUIMessage{
		{ID: "duplicate", Role: "user", Content: rawPayload(t, "one")},
		{ID: "duplicate", Role: "user", Content: rawPayload(t, "two")},
	}}
	writeClientFrame(t, conn, ClientFrame{Version: ProtocolVersion, Type: TypeRun, RequestID: "run", ThreadID: "thread-duplicate", Payload: rawPayload(t, input)})
	got := readWireFrame(t, conn)
	if got.Type != TypeError || !strings.Contains(string(got.Payload), "duplicate user message id") {
		t.Fatalf("duplicate message response = %+v", got)
	}
}

func TestRunErrorClearsActiveRun(t *testing.T) {
	server, _, _ := newTestServer(t, newFakeCatalog())
	server.noteEvent("thread", events.NewEvent(events.KindRunStarted, "run"))
	if !server.isActiveRun("thread", "run") {
		t.Fatal("started run is not active")
	}
	server.noteEvent("thread", events.NewEvent(events.KindRunError, "run"))
	if server.isActiveRun("thread", "run") {
		t.Fatal("errored run is still active")
	}
}

func TestControlFramesPublishActorMessages(t *testing.T) {
	_, registry, httpServer := newTestServer(t, newFakeCatalog("thread-control"))
	conn := dialTestServer(t, httpServer, "")
	subscribeSession(t, conn, "thread-control", "sub")

	preempts := make(chan bus.Envelope, 1)
	preemptID := registry.bus.SubscribeSerial([]string{bus.TopicPreempt("thread-control")}, func(env bus.Envelope) {
		preempts <- env
	}, eventbus.SerialConfig{Buffer: 2, Overflow: eventbus.OverflowBlock})
	defer registry.bus.Unsubscribe(preemptID)
	inbox := make(chan bus.Envelope, 3)
	inboxID := registry.bus.SubscribeSerial([]string{bus.TopicInbox("thread-control")}, func(env bus.Envelope) {
		inbox <- env
	}, eventbus.SerialConfig{Buffer: 4, Overflow: eventbus.OverflowBlock})
	defer registry.bus.Unsubscribe(inboxID)

	started := events.NewEvent(events.KindRunStarted, "active-run").WithPayload(events.RunStartedData{})
	registry.bus.Publish(bus.TopicRun("thread-control", "started"), bus.Envelope{Event: started, Session: "thread-control"})
	if frame := readWireFrame(t, conn); frame.Type != TypeEvent {
		t.Fatalf("run start frame = %+v", frame)
	}

	writeClientFrame(t, conn, ClientFrame{Version: ProtocolVersion, Type: TypeRunCancel, RequestID: "cancel-run", ThreadID: "thread-control",
		Payload: rawPayload(t, map[string]any{"runId": "active-run"})})
	if frame := readWireFrame(t, conn); frame.Type != TypeResult {
		t.Fatalf("cancel result = %+v", frame)
	}
	select {
	case env := <-preempts:
		var payload bus.PreemptInput
		if events.DecodePayload(env.Event, &payload) != nil || payload.Scope != string(bus.PreemptCurrent) {
			t.Fatalf("preempt payload = %+v", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("preempt was not published")
	}

	controls := []ClientFrame{
		{Version: ProtocolVersion, Type: TypeInputCancel, RequestID: "cancel-input", ThreadID: "thread-control", Payload: rawPayload(t, map[string]any{"eventId": "input-1"})},
		{Version: ProtocolVersion, Type: TypeFormSubmit, RequestID: "submit-form", ThreadID: "thread-control", Payload: rawPayload(t, map[string]any{"formId": "form-1", "values": map[string]any{"answer": "yes"}})},
		{Version: ProtocolVersion, Type: TypeFormCancel, RequestID: "cancel-form", ThreadID: "thread-control", Payload: rawPayload(t, map[string]any{"formId": "form-2"})},
	}
	for _, control := range controls {
		writeClientFrame(t, conn, control)
		if frame := readWireFrame(t, conn); frame.Type != TypeResult || frame.RequestID != control.RequestID {
			t.Fatalf("control result = %+v", frame)
		}
	}
	for _, want := range []string{bus.InboxCancelInput, bus.InboxFormSubmit, bus.InboxFormCancel} {
		select {
		case env := <-inbox:
			if env.Name != want {
				t.Fatalf("control event = %q, want %q", env.Name, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("control %q was not published", want)
		}
	}
}
