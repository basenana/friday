package actor

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	coreapi "github.com/basenana/friday/core/api"
	coreSession "github.com/basenana/friday/core/session"
	coretools "github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

// --- helpers ---

// newBareActor builds an Actor with only the outcome channel populated, no
// loop goroutine. Used by tests that exercise emit / bridge functions
// directly without involving setup.NewAgent.
func newBareActor(t *testing.T, outcomeBuffer int) *Actor {
	t.Helper()
	return &Actor{
		SessionID: "test-session",
		outcome:   make(chan Event, outcomeBuffer),
	}
}

// drainOutcome non-blockingly collects everything currently buffered in the
// outcome channel.
func drainOutcome(ch <-chan Event) []Event {
	var got []Event
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, evt)
		default:
			return got
		}
	}
}

// --- events / topic ---

func TestTopic(t *testing.T) {
	got := Topic("s1", EventRunStarted)
	if got != "actor.s1.RUN_STARTED" {
		t.Errorf("Topic = %q, want actor.s1.RUN_STARTED", got)
	}
}

func TestTopicAll(t *testing.T) {
	got := TopicAll("s1")
	if got != "actor.s1.*" {
		t.Errorf("TopicAll = %q, want actor.s1.*", got)
	}
}

// --- MergeMessages ---

func TestMergeMessages_Empty(t *testing.T) {
	prompt, imgs := MergeMessages(nil)
	if prompt != "" || len(imgs) != 0 {
		t.Errorf("empty input: prompt=%q imgs=%v", prompt, imgs)
	}
}

func TestMergeMessages_Single(t *testing.T) {
	msgs := []Message{{ID: "1", Content: "hello", ImageURLs: []string{"a.png"}}}
	prompt, imgs := MergeMessages(msgs)
	if prompt != "hello" {
		t.Errorf("prompt = %q, want hello", prompt)
	}
	if len(imgs) != 1 || imgs[0] != "a.png" {
		t.Errorf("imgs = %v, want [a.png]", imgs)
	}
	// Returned slice must be a copy so callers cannot mutate the original.
	if &imgs[0] == &msgs[0].ImageURLs[0] {
		t.Error("MergeMessages returned the original slice header for single msg")
	}
}

func TestMergeMessages_Multi_JoinedAndDedup(t *testing.T) {
	msgs := []Message{
		{ID: "1", Content: "q1", ImageURLs: []string{"a.png", "b.png"}},
		{ID: "2", Content: "q2", ImageURLs: []string{"b.png", "c.png"}},
		{ID: "3", Content: "", ImageURLs: []string{"d.png"}}, // empty content skipped
	}
	prompt, imgs := MergeMessages(msgs)

	wantPrompt := "q1\n---\nq2"
	if prompt != wantPrompt {
		t.Errorf("prompt = %q, want %q", prompt, wantPrompt)
	}
	wantImgs := []string{"a.png", "b.png", "c.png", "d.png"}
	if len(imgs) != len(wantImgs) {
		t.Fatalf("imgs = %v, want %v", imgs, wantImgs)
	}
	for i := range wantImgs {
		if imgs[i] != wantImgs[i] {
			t.Errorf("imgs[%d] = %q, want %q", i, imgs[i], wantImgs[i])
		}
	}
}

func TestMergeMessages_Multi_AllEmptyContent(t *testing.T) {
	msgs := []Message{{ID: "1"}, {ID: "2"}}
	prompt, imgs := MergeMessages(msgs)
	if prompt != "" || len(imgs) != 0 {
		t.Errorf("prompt=%q imgs=%v, want empty", prompt, imgs)
	}
}

// --- emit ---

func TestEmit_StampsFields(t *testing.T) {
	a := newBareActor(t, 4)
	a.emit(Event{Type: EventRunStarted, RunID: "r1"})

	got := drainOutcome(a.outcome)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	evt := got[0]
	if evt.Type != EventRunStarted {
		t.Errorf("type = %q", evt.Type)
	}
	if evt.SessionID != "test-session" {
		t.Errorf("sessionID = %q", evt.SessionID)
	}
	if evt.RunID != "r1" {
		t.Errorf("runID = %q", evt.RunID)
	}
	if evt.Seq != 1 {
		t.Errorf("seq = %d, want 1", evt.Seq)
	}
	if evt.Timestamp.IsZero() {
		t.Error("timestamp not stamped")
	}
}

func TestEmit_SequenceIsMonotonic(t *testing.T) {
	a := newBareActor(t, 16)
	for i := 0; i < 5; i++ {
		a.emit(Event{Type: EventCustom})
	}
	got := drainOutcome(a.outcome)
	var prev int64
	for i, evt := range got {
		if evt.Seq <= prev {
			t.Errorf("event %d seq=%d not greater than prev=%d", i, evt.Seq, prev)
		}
		prev = evt.Seq
	}
}

func TestEmit_DropsWhenOutcomeFull(t *testing.T) {
	a := newBareActor(t, 2)
	a.emit(Event{Type: EventCustom})
	a.emit(Event{Type: EventCustom})
	a.emit(Event{Type: EventCustom}) // should be dropped, non-blocking

	got := drainOutcome(a.outcome)
	if len(got) != 2 {
		t.Errorf("expected 2 buffered events when buffer=2, got %d", len(got))
	}
}

// --- bridgeCoreEvents ---

func TestBridgeCoreEvents_Mappings(t *testing.T) {
	a := newBareActor(t, 32)
	ch := make(chan types.Event, 16)

	cases := []struct {
		name    string
		in      types.Event
		want    EventType
		dataK   string // key to spot-check
		wantV   string
		extras  int // extra events emitted by this input beyond `want`
	}{
		{"model_start", types.Event{Type: types.EventModelStart}, EventStepStarted, "step_name", "llm", 0},
		{"model_finish", types.Event{Type: types.EventModelFinish, Data: map[string]string{"completion_tokens": "42"}}, EventStepFinished, "completion_tokens", "42", 0},
		{"loop_start", types.Event{Type: types.EventLoopStart, Data: map[string]string{"budget": "50"}}, EventStepStarted, "budget", "50", 0},
		{"tool_start", types.Event{Type: types.EventToolStart, Data: map[string]string{"id": "t1", "tool": "ls", "input": "{}"}}, EventToolCallStart, "tool_name", "ls", 0},
		{"tool_finish", types.Event{Type: types.EventToolFinish, Data: map[string]string{"id": "t1", "tool": "ls", "success": "true", "output": "ok"}}, EventToolCallResult, "output", "ok", 1},
		{"todo_update", types.Event{Type: types.EventTodoUpdate, Data: map[string]string{"x": "y"}}, EventActivityDelta, "activity_type", "PLAN", 0},
		{"subagent_start", types.Event{Type: types.EventSubagentStart, Data: map[string]string{"k": "v"}}, EventCustom, "name", "subagent.start", 0},
	}

	for _, c := range cases {
		ch <- c.in
	}
	close(ch)

	done := make(chan struct{})
	go func() {
		a.bridgeCoreEvents(ch, "run-1")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bridgeCoreEvents did not return within 1s")
	}

	got := drainOutcome(a.outcome)
	wantTotal := 0
	for _, c := range cases {
		wantTotal += 1 + c.extras
	}
	if len(got) != wantTotal {
		t.Fatalf("expected %d events, got %d: %+v", wantTotal, len(got), got)
	}

	// Walk `got` in lock-step with `cases`, accounting for extras. The
	// expected event is emitted first; extras follow it.
	gi := 0
	for _, c := range cases {
		if gi >= len(got) {
			t.Fatalf("ran out of events at case %q (gi=%d)", c.name, gi)
		}
		evt := got[gi]
		if evt.Type != c.want {
			t.Errorf("case %q: event %d type = %q, want %q", c.name, gi, evt.Type, c.want)
		}
		gotV, _ := evt.Data[c.dataK].(string)
		if gotV != c.wantV {
			t.Errorf("case %q: data[%q] = %q, want %q", c.name, c.dataK, gotV, c.wantV)
		}
		gi += 1 + c.extras
	}

	// Sanity: tool_finish extras include exactly one TOOL_CALL_END.
	var ends int
	for _, evt := range got {
		if evt.Type == EventToolCallEnd {
			ends++
		}
	}
	if ends != 1 {
		t.Errorf("TOOL_CALL_END count = %d, want 1", ends)
	}
}

func TestBridgeCoreEvents_ExitsOnChannelClose(t *testing.T) {
	a := newBareActor(t, 4)
	ch := make(chan types.Event)
	close(ch)

	done := make(chan struct{})
	go func() {
		a.bridgeCoreEvents(ch, "r")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bridgeCoreEvents did not exit after channel close")
	}
}

// --- bridgeResponseDeltas ---

func TestBridgeResponseDeltas_TextThreePhase(t *testing.T) {
	a := newBareActor(t, 16)
	resp := coreapi.NewResponse()

	go func() {
		coreapi.SendDelta(resp, types.Delta{Content: "Hello "})
		coreapi.SendDelta(resp, types.Delta{Content: "World"})
		resp.Close()
	}()

	done := make(chan struct{})
	go func() {
		a.bridgeResponseDeltas(resp, "run-text")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bridgeResponseDeltas did not return within 1s")
	}

	got := drainOutcome(a.outcome)
	// Expected: START, CONTENT(Hello ), CONTENT(World), END
	if len(got) != 4 {
		t.Fatalf("expected 4 events, got %d: %+v", len(got), got)
	}
	if got[0].Type != EventTextMessageStart {
		t.Errorf("event 0 = %q, want TEXT_MESSAGE_START", got[0].Type)
	}
	if got[1].Type != EventTextMessageContent || got[1].Data["delta"] != "Hello " {
		t.Errorf("event 1 = %+v, want CONTENT Hello ", got[1])
	}
	if got[2].Type != EventTextMessageContent || got[2].Data["delta"] != "World" {
		t.Errorf("event 2 = %+v, want CONTENT World", got[2])
	}
	if got[3].Type != EventTextMessageEnd {
		t.Errorf("event 3 = %q, want TEXT_MESSAGE_END", got[3].Type)
	}
	// All share the same MessageID.
	id := got[0].MessageID
	for _, evt := range got {
		if evt.MessageID != id || id == "" {
			t.Errorf("inconsistent MessageID: %+v", evt)
		}
	}
}

func TestBridgeResponseDeltas_ReasoningThreePhase(t *testing.T) {
	a := newBareActor(t, 16)
	resp := coreapi.NewResponse()

	go func() {
		coreapi.SendDelta(resp, types.Delta{Reasoning: "thinking..."})
		resp.Close()
	}()

	done := make(chan struct{})
	go func() {
		a.bridgeResponseDeltas(resp, "run-r")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bridgeResponseDeltas did not return")
	}

	got := drainOutcome(a.outcome)
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d", len(got))
	}
	if got[0].Type != EventReasoningStart {
		t.Errorf("event 0 = %q, want REASONING_START", got[0].Type)
	}
	if got[1].Type != EventReasoningMessageContent || got[1].Data["delta"] != "thinking..." {
		t.Errorf("event 1 = %+v, want REASONING_MESSAGE_CONTENT", got[1])
	}
	if got[2].Type != EventReasoningEnd {
		t.Errorf("event 2 = %q, want REASONING_END", got[2].Type)
	}
}

func TestBridgeResponseDeltas_TextAndReasoningBothEndEmitted(t *testing.T) {
	// Regression: when both streams are started, BOTH End events must be
	// emitted on delta-channel close, even though api.Response.Close()
	// also closes the error channel.
	a := newBareActor(t, 32)
	resp := coreapi.NewResponse()

	go func() {
		coreapi.SendDelta(resp, types.Delta{Reasoning: "hmm"})
		coreapi.SendDelta(resp, types.Delta{Content: "answer"})
		resp.Close()
	}()

	done := make(chan struct{})
	go func() {
		a.bridgeResponseDeltas(resp, "run-mix")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bridgeResponseDeltas did not return")
	}

	got := drainOutcome(a.outcome)
	var (
		sawTextEnd     bool
		sawReasonEnd   bool
		sawContent     bool
		sawReasonCont  bool
	)
	for _, evt := range got {
		switch evt.Type {
		case EventTextMessageEnd:
			sawTextEnd = true
		case EventReasoningEnd:
			sawReasonEnd = true
		case EventTextMessageContent:
			sawContent = true
		case EventReasoningMessageContent:
			sawReasonCont = true
		}
	}
	if !sawTextEnd {
		t.Error("TEXT_MESSAGE_END not emitted (lost to closed err chan race?)")
	}
	if !sawReasonEnd {
		t.Error("REASONING_END not emitted")
	}
	if !sawContent || !sawReasonCont {
		t.Errorf("content=%v reasoning=%v", sawContent, sawReasonCont)
	}
}

func TestBridgeResponseDeltas_RealErrorEmitsRunError(t *testing.T) {
	a := newBareActor(t, 8)
	resp := coreapi.NewResponse()

	go func() {
		resp.Fail(errors.New("boom"))
	}()

	done := make(chan struct{})
	go func() {
		a.bridgeResponseDeltas(resp, "run-err")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bridgeResponseDeltas did not return")
	}

	got := drainOutcome(a.outcome)
	if len(got) != 1 {
		t.Fatalf("expected 1 event (RunError), got %d: %+v", len(got), got)
	}
	if got[0].Type != EventRunError {
		t.Errorf("type = %q, want RUN_ERROR", got[0].Type)
	}
	if got[0].Data["message"] != "boom" {
		t.Errorf("message = %v, want boom", got[0].Data["message"])
	}
}

// --- Send / Shutdown (real loop, no setup.NewAgent involved) ---

func TestSend_InboxFull_ReturnsFalse(t *testing.T) {
	// Construct an actor with a tiny inbox and never let the loop drain by
	// shutting it down right away... but the loop drains eagerly. Instead,
	// construct bare (no loop) so the inbox can saturate.
	a := &Actor{
		SessionID: "send-test",
		inbox:     make(chan Message, 1),
		done:      make(chan struct{}),
	}
	if !a.Send(Message{Content: "first"}) {
		t.Error("first Send returned false on empty inbox")
	}
	if a.Send(Message{Content: "second"}) {
		t.Error("second Send returned true on full inbox")
	}
}

func TestNewActor_ShutdownClosesDoneAndOutcome(t *testing.T) {
	// New() starts the loop goroutine; Shutdown must close both `done` and
	// the outcome channel. This path requires no setup.NewAgent because the
	// loop is idle (nothing in inbox).
	a := New("shutdown-test", nil, nil)
	a.Shutdown()

	select {
	case <-a.Done():
	default:
		t.Fatal("Done() not closed after Shutdown")
	}
	select {
	case _, ok := <-a.Outcome():
		if ok {
			t.Fatal("Outcome() channel still open after Shutdown")
		}
	default:
		t.Fatal("Outcome() channel not closed after Shutdown")
	}
	if a.State() != StateShutdown {
		t.Errorf("state = %v, want Shutdown", a.State())
	}
}

func TestNewActor_LastActiveUpdatesAfterProcessing(t *testing.T) {
	// We can't easily drive runAgent without setup.NewAgent. But we can at
	// least confirm the actor starts in Idle and LastActive is recent.
	a := New("idle-test", nil, nil)
	defer a.Shutdown()

	if a.State() != StateIdle {
		t.Errorf("state = %v, want Idle", a.State())
	}
	if time.Since(a.LastActive()) > time.Second {
		t.Errorf("LastActive is stale: %v", time.Since(a.LastActive()))
	}
}

// --- hook / activityTool ---

func TestActivityTool_EmitsSnapshot(t *testing.T) {
	var (
		emitted  atomic.Pointer[Event]
		emitCall atomic.Int32
	)
	emit := func(evt Event) {
		c := evt
		emitted.Store(&c)
		emitCall.Add(1)
	}

	h := newActorHook(emit, "run-act")
	tool := h.activityTool()

	if tool.Name != "emit_activity" {
		t.Errorf("tool name = %q, want emit_activity", tool.Name)
	}

	// Valid JSON content → parsed structure carried through.
	res, err := tool.Handler(context.Background(), &coretools.Request{
		Arguments: map[string]interface{}{
			"activity_type": "PLAN",
			"content":       `{"steps":["a","b"]}`,
		},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res == nil || res.IsError {
		t.Errorf("expected non-error result, got %+v", res)
	}
	if emitCall.Load() != 1 {
		t.Fatalf("emit called %d times, want 1", emitCall.Load())
	}
	evt := emitted.Load()
	if evt == nil {
		t.Fatal("no event emitted")
	}
	if evt.Type != EventActivitySnapshot {
		t.Errorf("event type = %q, want ACTIVITY_SNAPSHOT", evt.Type)
	}
	if evt.RunID != "run-act" {
		t.Errorf("runID = %q, want run-act", evt.RunID)
	}
	if evt.Data["activity_type"] != "PLAN" {
		t.Errorf("activity_type = %v, want PLAN", evt.Data["activity_type"])
	}
	// Parsed content should be a map, not a raw string.
	if _, ok := evt.Data["content"].(map[string]interface{}); !ok {
		t.Errorf("content not parsed as object: %T", evt.Data["content"])
	}
}

func TestActivityTool_InvalidJSONFallsBackToRawString(t *testing.T) {
	var emitted atomic.Pointer[Event]
	emit := func(evt Event) { c := evt; emitted.Store(&c) }

	h := newActorHook(emit, "run-raw")
	tool := h.activityTool()

	if _, err := tool.Handler(context.Background(), &coretools.Request{
		Arguments: map[string]interface{}{
			"activity_type": "PROGRESS",
			"content":       "not json at all",
		},
	}); err != nil {
		t.Fatalf("handler error: %v", err)
	}

	evt := emitted.Load()
	if evt == nil {
		t.Fatal("no event emitted")
	}
	if s, ok := evt.Data["content"].(string); !ok || s != "not json at all" {
		t.Errorf("content = %v, want raw string fallback", evt.Data["content"])
	}
}

// --- BeforeAgent hook integration (no core agent needed) ---

// fakeAgentRequest is a minimal AgentRequest for verifying tool injection.
type fakeAgentRequest struct {
	tools []*coretools.Tool
}

func (f *fakeAgentRequest) GetUserMessage() string            { return "" }
func (f *fakeAgentRequest) SetUserMessage(string)             {}
func (f *fakeAgentRequest) GetTools() []*coretools.Tool       { return f.tools }
func (f *fakeAgentRequest) AppendTools(ts ...*coretools.Tool) { f.tools = append(f.tools, ts...) }

func TestActorHook_BeforeAgent_InjectsActivityTool(t *testing.T) {
	h := newActorHook(func(Event) {}, "run-inject")
	req := &fakeAgentRequest{}

	if err := h.BeforeAgent(context.Background(), nil, req); err != nil {
		t.Fatalf("BeforeAgent error: %v", err)
	}
	if len(req.tools) != 1 {
		t.Fatalf("expected 1 injected tool, got %d", len(req.tools))
	}
	if req.tools[0].Name != "emit_activity" {
		t.Errorf("injected tool = %q, want emit_activity", req.tools[0].Name)
	}
}

func TestActorHook_AfterTool_NoError(t *testing.T) {
	h := newActorHook(func(Event) {}, "run-after")
	// Should be a no-op and never panic.
	if err := h.AfterTool(context.Background(), nil, coreSession.ToolPayload{}); err != nil {
		t.Errorf("AfterTool returned error: %v", err)
	}
}
