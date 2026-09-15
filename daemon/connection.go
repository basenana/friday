package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	coretypes "github.com/basenana/friday/core/types"
)

const (
	maxClientFrameBytes = 1 << 20
	connectionQueueSize = 256
	liveQueueSize       = 512
)

type connection struct {
	server *Server
	ws     *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	out    chan ServerFrame

	mu   sync.Mutex
	subs map[string]*sessionSubscription
	once sync.Once

	closeMu     sync.Mutex
	closeStatus websocket.StatusCode
	closeReason string
}

type sessionSubscription struct {
	threadID string
	ids      []string
	live     chan bus.Envelope
	done     chan struct{}
	once     sync.Once
}

func (s *sessionSubscription) close(b *eventbus.Bus) {
	s.once.Do(func() {
		close(s.done)
		bus.UnsubscribeAll(b, s.ids...)
	})
}

func (s *Server) serveWebSocket(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{
		"localhost", "localhost:*", "127.0.0.1", "127.0.0.1:*",
		"http://localhost", "http://localhost:*", "https://localhost", "https://localhost:*",
		"http://127.0.0.1", "http://127.0.0.1:*", "https://127.0.0.1", "https://127.0.0.1:*",
	}})
	if err != nil {
		return
	}
	ws.SetReadLimit(maxClientFrameBytes)
	ctx, cancel := context.WithCancel(s.ctx)
	c := &connection{server: s, ws: ws, ctx: ctx, cancel: cancel,
		out: make(chan ServerFrame, connectionQueueSize), subs: make(map[string]*sessionSubscription)}
	c.run()
}

func (c *connection) run() {
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		c.writeLoop()
	}()
	pingDone := make(chan struct{})
	go func() {
		defer close(pingDone)
		c.pingLoop()
	}()

	c.readLoop()
	c.stop()
	<-writerDone
	<-pingDone
	status, reason := c.closeDetails()
	_ = c.ws.Close(status, reason)
}

func (c *connection) fail(status websocket.StatusCode, reason string) {
	c.closeMu.Lock()
	if c.closeStatus == 0 {
		c.closeStatus = status
		c.closeReason = reason
	}
	c.closeMu.Unlock()
	c.cancel()
}

func (c *connection) closeDetails() (websocket.StatusCode, string) {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closeStatus == 0 {
		return websocket.StatusNormalClosure, "connection closed"
	}
	return c.closeStatus, c.closeReason
}

func (c *connection) stop() {
	c.once.Do(func() {
		c.cancel()
		c.mu.Lock()
		for threadID, sub := range c.subs {
			sub.close(c.server.registry.Bus())
			delete(c.subs, threadID)
		}
		c.mu.Unlock()
	})
}

func (c *connection) readLoop() {
	for {
		kind, data, err := c.ws.Read(c.ctx)
		if err != nil {
			return
		}
		if kind != websocket.MessageText {
			_ = c.ws.Close(websocket.StatusUnsupportedData, "JSON text frames required")
			return
		}
		var frame ClientFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			c.sendError("", "", "invalid_json", "invalid JSON frame")
			continue
		}
		c.handle(frame)
	}
}

func (c *connection) writeLoop() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case frame := <-c.out:
			data, err := json.Marshal(frame)
			if err != nil {
				c.cancel()
				return
			}
			ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
			err = c.ws.Write(ctx, websocket.MessageText, data)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		}
	}
}

func (c *connection) pingLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
			err := c.ws.Ping(ctx)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		}
	}
}

func (c *connection) handle(frame ClientFrame) {
	if frame.Version != ProtocolVersion {
		c.sendError(frame.RequestID, frame.ThreadID, "unsupported_version", "protocol version must be 1")
		return
	}
	if frame.RequestID == "" {
		c.sendError("", frame.ThreadID, "invalid_request", "requestId is required")
		return
	}
	if frame.Type != TypeSessionCreate && !validThreadID(frame.ThreadID) {
		c.sendError(frame.RequestID, frame.ThreadID, "invalid_thread", "threadId is invalid")
		return
	}

	switch frame.Type {
	case TypeSessionCreate:
		c.createSession(frame)
	case TypeSessionSubscribe:
		c.subscribe(frame)
	case TypeSessionUnsubscribe:
		c.unsubscribe(frame)
	case TypeRun:
		c.runAgent(frame)
	case TypeRunCancel:
		c.publishPreempt(frame)
	case TypeInputCancel:
		c.cancelInput(frame)
	case TypeFormSubmit:
		c.submitForm(frame)
	case TypeFormCancel:
		c.cancelForm(frame)
	default:
		c.sendError(frame.RequestID, frame.ThreadID, "unknown_type", "unknown frame type")
	}
}

func (c *connection) createSession(frame ClientFrame) {
	id, err := c.server.catalog.Create(c.ctx)
	if err != nil {
		c.sendError(frame.RequestID, "", "session_create_failed", err.Error())
		return
	}
	c.sendResult(frame.RequestID, id, map[string]any{"threadId": id})
}

func (c *connection) subscribe(frame ClientFrame) {
	c.mu.Lock()
	_, duplicate := c.subs[frame.ThreadID]
	c.mu.Unlock()
	if duplicate {
		c.sendError(frame.RequestID, frame.ThreadID, "already_subscribed", "session is already subscribed")
		return
	}
	exists, err := c.server.catalog.Exists(frame.ThreadID)
	if err != nil {
		c.sendError(frame.RequestID, frame.ThreadID, "session_lookup_failed", err.Error())
		return
	}
	if !exists {
		c.sendError(frame.RequestID, frame.ThreadID, "session_not_found", "session does not exist")
		return
	}

	sub := &sessionSubscription{threadID: frame.ThreadID, live: make(chan bus.Envelope, liveQueueSize), done: make(chan struct{})}
	sub.ids = bus.SubscribeAgent(c.server.registry.Bus(), frame.ThreadID, func(env bus.Envelope) {
		select {
		case <-sub.done:
			return
		case sub.live <- env:
		default:
			c.fail(websocket.StatusTryAgainLater, "client event queue is full")
		}
	}, eventbus.SerialConfig{Buffer: 256, Overflow: eventbus.OverflowBlock})

	c.mu.Lock()
	if c.ctx.Err() != nil {
		c.mu.Unlock()
		sub.close(c.server.registry.Bus())
		return
	}
	c.subs[frame.ThreadID] = sub
	c.mu.Unlock()

	if _, err := c.server.registry.GetOrCreate(frame.ThreadID); err != nil {
		c.removeSubscription(frame.ThreadID)
		c.sendError(frame.RequestID, frame.ThreadID, "actor_start_failed", err.Error())
		return
	}
	history, err := c.server.catalog.LoadEvents(c.ctx, frame.ThreadID)
	if err != nil {
		c.removeSubscription(frame.ThreadID)
		c.sendError(frame.RequestID, frame.ThreadID, "history_load_failed", err.Error())
		return
	}

	c.send(ServerFrame{Version: ProtocolVersion, Type: TypeHistoryBegin, RequestID: frame.RequestID,
		ThreadID: frame.ThreadID, Payload: map[string]any{"count": len(history)}})
	replayed := make(map[string]struct{}, len(history))
	for _, evt := range history {
		if evt.ID != "" {
			replayed[evt.ID] = struct{}{}
		}
		c.server.noteEvent(frame.ThreadID, evt)
		if !c.send(ServerFrame{Version: ProtocolVersion, Type: TypeEvent, ThreadID: frame.ThreadID, Replay: true, Payload: evt}) {
			return
		}
	}
	if !c.send(ServerFrame{Version: ProtocolVersion, Type: TypeHistoryEnd, RequestID: frame.RequestID,
		ThreadID: frame.ThreadID, Payload: map[string]any{"count": len(history)}}) {
		return
	}
	c.sendResult(frame.RequestID, frame.ThreadID, map[string]any{"subscribed": true})
	go c.forwardLive(sub, replayed)
}

func (c *connection) forwardLive(sub *sessionSubscription, replayed map[string]struct{}) {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-sub.done:
			return
		case env := <-sub.live:
			if _, duplicate := replayed[env.ID]; duplicate && env.ID != "" {
				delete(replayed, env.ID)
				continue
			}
			c.server.noteEvent(sub.threadID, env.Event)
			if !c.trySend(ServerFrame{Version: ProtocolVersion, Type: TypeEvent, ThreadID: sub.threadID, Payload: env.Event}) {
				c.fail(websocket.StatusTryAgainLater, "client write queue is full")
				return
			}
		}
	}
}

func (c *connection) unsubscribe(frame ClientFrame) {
	if !c.removeSubscription(frame.ThreadID) {
		c.sendError(frame.RequestID, frame.ThreadID, "not_subscribed", "session is not subscribed")
		return
	}
	c.sendResult(frame.RequestID, frame.ThreadID, map[string]any{"subscribed": false})
}

func (c *connection) removeSubscription(threadID string) bool {
	c.mu.Lock()
	sub, ok := c.subs[threadID]
	if ok {
		delete(c.subs, threadID)
	}
	c.mu.Unlock()
	if ok {
		sub.close(c.server.registry.Bus())
	}
	return ok
}

func (c *connection) subscribed(threadID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.subs[threadID]
	return ok
}

func (c *connection) requireSubscription(frame ClientFrame) bool {
	if c.subscribed(frame.ThreadID) {
		return true
	}
	c.sendError(frame.RequestID, frame.ThreadID, "not_subscribed", "subscribe to the session first")
	return false
}

func (c *connection) runAgent(frame ClientFrame) {
	if !c.requireSubscription(frame) {
		return
	}
	var input RunAgentInput
	if err := json.Unmarshal(frame.Payload, &input); err != nil {
		c.sendError(frame.RequestID, frame.ThreadID, "invalid_run_input", "payload must be an AG-UI RunAgentInput")
		return
	}
	if input.ThreadID != frame.ThreadID || input.RunID == "" {
		c.sendError(frame.RequestID, frame.ThreadID, "invalid_run_input", "threadId must match and runId is required")
		return
	}
	if nonEmptyJSON(input.State) || len(input.Tools) > 0 || len(input.Context) > 0 || len(input.Resume) > 0 {
		c.sendError(frame.RequestID, frame.ThreadID, "unsupported_feature", "state, tools, context, and resume are not supported in v1")
		return
	}
	delivery, err := input.delivery()
	if err != nil {
		c.sendError(frame.RequestID, frame.ThreadID, "invalid_run_input", err.Error())
		return
	}
	if _, err := c.server.registry.GetOrCreate(frame.ThreadID); err != nil {
		c.sendError(frame.RequestID, frame.ThreadID, "actor_start_failed", err.Error())
		return
	}
	tail, err := c.server.reserveUnseenTail(frame.ThreadID, input.Messages)
	if err != nil {
		c.sendError(frame.RequestID, frame.ThreadID, "invalid_run_input", err.Error())
		return
	}
	if len(tail) == 0 {
		c.sendResult(frame.RequestID, frame.ThreadID, map[string]any{"duplicate": true, "runId": input.RunID})
		return
	}
	type preparedInput struct {
		message AGUIMessage
		text    string
		images  []coretypes.ImageContent
	}
	prepared := make([]preparedInput, 0, len(tail))
	for _, message := range tail {
		text, images, err := message.userContent()
		if err != nil {
			ids := make([]string, 0, len(tail))
			for _, reserved := range tail {
				ids = append(ids, reserved.ID)
			}
			c.server.forgetSeen(frame.ThreadID, ids...)
			c.sendError(frame.RequestID, frame.ThreadID, "unsupported_input", err.Error())
			return
		}
		prepared = append(prepared, preparedInput{message: message, text: text, images: images})
	}
	for i, item := range prepared {
		env := bus.NewUserInput(frame.ThreadID, "user.ws", bus.UserTextInput{
			Text: item.text, TurnID: input.RunID, Delivery: bus.InputDelivery(delivery), Images: item.images,
			Metadata: map[string]any{"parentRunId": input.ParentRunID},
		})
		env.ID = item.message.ID
		if err := c.server.registry.DispatchInput(env); err != nil {
			undelivered := make([]string, 0, len(prepared)-i)
			for _, pending := range prepared[i:] {
				undelivered = append(undelivered, pending.message.ID)
			}
			c.server.forgetSeen(frame.ThreadID, undelivered...)
			c.sendError(frame.RequestID, frame.ThreadID, "actor_dispatch_failed", err.Error())
			return
		}
	}
	c.sendResult(frame.RequestID, frame.ThreadID, map[string]any{"accepted": len(tail), "runId": input.RunID})
}

func (c *connection) publishPreempt(frame ClientFrame) {
	if !c.requireSubscription(frame) {
		return
	}
	var payload struct {
		RunID  string `json:"runId"`
		Reason string `json:"reason,omitempty"`
	}
	if err := json.Unmarshal(frame.Payload, &payload); err != nil || payload.RunID == "" {
		c.sendError(frame.RequestID, frame.ThreadID, "invalid_request", "runId is required")
		return
	}
	if !c.server.isActiveRun(frame.ThreadID, payload.RunID) {
		c.sendError(frame.RequestID, frame.ThreadID, "run_not_active", "run is not active")
		return
	}
	reason := payload.Reason
	if reason == "" {
		reason = "cancelled by websocket client"
	}
	if err := c.server.registry.DispatchPreempt(bus.NewScopedPreempt(frame.ThreadID, "user.ws", reason, bus.PreemptCurrent)); err != nil {
		c.sendError(frame.RequestID, frame.ThreadID, "run_not_active", err.Error())
		return
	}
	c.sendResult(frame.RequestID, frame.ThreadID, map[string]any{"runId": payload.RunID})
}

func (c *connection) cancelInput(frame ClientFrame) {
	if !c.requireSubscription(frame) {
		return
	}
	var payload struct {
		EventID string `json:"eventId"`
		Reason  string `json:"reason,omitempty"`
	}
	if err := json.Unmarshal(frame.Payload, &payload); err != nil || payload.EventID == "" {
		c.sendError(frame.RequestID, frame.ThreadID, "invalid_request", "eventId is required")
		return
	}
	if err := c.server.registry.DispatchInput(bus.NewCancelInput(frame.ThreadID, "user.ws", bus.CancelInput{EventID: payload.EventID, Reason: payload.Reason})); err != nil {
		c.sendError(frame.RequestID, frame.ThreadID, "interaction_not_active", err.Error())
		return
	}
	c.sendResult(frame.RequestID, frame.ThreadID, map[string]any{"eventId": payload.EventID})
}

func (c *connection) submitForm(frame ClientFrame) {
	if !c.requireSubscription(frame) {
		return
	}
	var payload struct {
		FormID string         `json:"formId"`
		Values map[string]any `json:"values,omitempty"`
	}
	if err := json.Unmarshal(frame.Payload, &payload); err != nil || payload.FormID == "" {
		c.sendError(frame.RequestID, frame.ThreadID, "invalid_request", "formId is required")
		return
	}
	if err := c.server.registry.DispatchInput(bus.NewFormSubmit(frame.ThreadID, "user.ws", bus.FormSubmitInput{FormID: payload.FormID, Values: payload.Values})); err != nil {
		c.sendError(frame.RequestID, frame.ThreadID, "interaction_not_active", err.Error())
		return
	}
	c.sendResult(frame.RequestID, frame.ThreadID, map[string]any{"formId": payload.FormID})
}

func (c *connection) cancelForm(frame ClientFrame) {
	if !c.requireSubscription(frame) {
		return
	}
	var payload struct {
		FormID string `json:"formId"`
	}
	if err := json.Unmarshal(frame.Payload, &payload); err != nil || payload.FormID == "" {
		c.sendError(frame.RequestID, frame.ThreadID, "invalid_request", "formId is required")
		return
	}
	if err := c.server.registry.DispatchInput(bus.NewFormCancel(frame.ThreadID, "user.ws", bus.FormCancelInput{FormID: payload.FormID})); err != nil {
		c.sendError(frame.RequestID, frame.ThreadID, "interaction_not_active", err.Error())
		return
	}
	c.sendResult(frame.RequestID, frame.ThreadID, map[string]any{"formId": payload.FormID})
}

func (c *connection) sendResult(requestID, threadID string, payload any) {
	c.send(ServerFrame{Version: ProtocolVersion, Type: TypeResult, RequestID: requestID, ThreadID: threadID, Payload: payload})
}

func (c *connection) sendError(requestID, threadID, code, message string) {
	c.send(ServerFrame{Version: ProtocolVersion, Type: TypeError, RequestID: requestID, ThreadID: threadID,
		Payload: ErrorPayload{Code: code, Message: message}})
}

func (c *connection) send(frame ServerFrame) bool {
	select {
	case <-c.ctx.Done():
		return false
	case c.out <- frame:
		return true
	}
}

func (c *connection) trySend(frame ServerFrame) bool {
	select {
	case <-c.ctx.Done():
		return false
	case c.out <- frame:
		return true
	default:
		return false
	}
}
