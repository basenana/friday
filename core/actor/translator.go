package actor

import (
	"context"

	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/types"
)

// Translator converts core deltas/events into AG-UI-shaped actor events.
// It is stateful per-turn: it remembers whether a text message block has
// been opened so it can emit the matching START / END events.
type Translator struct {
	runID string

	textItemID  string
	textOpen    bool
	reasoningID string
	toolCallIDs map[string]string
}

// NewTranslator returns a Translator bound to the given runID (turn id).
func NewTranslator(runID string) *Translator {
	return &Translator{runID: runID, toolCallIDs: make(map[string]string)}
}

// nextID generates short per-call identifiers used to correlate START
// / CONTENT / END triplets. They are not globally unique; uniqueness
// only needs to hold within the run.
func (t *Translator) nextID(prefix string) string {
	return globalIDGenerator.Next(prefix)
}

// FromDelta converts a core Delta into zero or more actor events.
//   - First Content chunk opens a TEXT_MESSAGE_START item and emits a
//     CONTENT event.
//   - Subsequent Content chunks emit CONTENT events.
//   - The caller must invoke CloseText() once the delta stream ends so
//     the matching END event is emitted.
//   - Reasoning chunks are emitted as CUSTOM{name="reasoning.delta"}.
func (t *Translator) FromDelta(d types.Delta) []events.Event {
	out := make([]events.Event, 0, 2)
	if d.Content != "" {
		if !t.textOpen {
			t.textItemID = t.nextID("msg")
			t.textOpen = true
			out = append(out, events.NewEvent(events.KindTextMessageStart, t.runID).
				WithMessageID(t.textItemID).
				WithPayload(events.TextMessageStartData{Role: "assistant"}))
		}
		out = append(out, events.NewEvent(events.KindTextMessageContent, t.runID).
			WithMessageID(t.textItemID).
			WithPayload(events.TextMessageContentData{Content: d.Content}))
	}
	if d.Reasoning != "" {
		if t.reasoningID == "" {
			t.reasoningID = t.nextID("rsn")
		}
		out = append(out, events.NewEvent(events.KindCustom, t.runID).
			WithName(events.CustomReasoningDelta).
			WithMessageID(t.reasoningID).
			WithPayload(events.ReasoningDeltaBody{Content: d.Reasoning}))
	}
	return out
}

// CloseText flushes the END event for an open text block. No-op when
// no block is open.
func (t *Translator) CloseText() []events.Event {
	if !t.textOpen {
		return nil
	}
	t.textOpen = false
	return []events.Event{
		events.NewEvent(events.KindTextMessageEnd, t.runID).
			WithMessageID(t.textItemID).
			WithPayload(events.TextMessageEndData{}),
	}
}

// FromCoreEvent converts a core session.Event into zero or more actor
// events. Unknown event types are emitted as CUSTOM events whose Name
// is the original EventType string, preserving passthrough.
func (t *Translator) FromCoreEvent(evt types.Event) []events.Event {
	switch evt.Type {
	case types.EventAgentStart:
		// RUN_STARTED is emitted by the actor itself around the Chat
		// call; ignore the duplicate from core.
		return nil
	case types.EventAgentFinish:
		// RUN_FINISHED is owned by the actor loop.
		return nil
	case types.EventModelStart:
		id := t.nextID("mdl")
		return []events.Event{
			events.NewEvent(events.KindStepStarted, t.runID).
				WithMessageID(id).
				WithPayload(events.StepStartedData{Kind: "model"}),
		}
	case types.EventModelFinish:
		return []events.Event{
			events.NewEvent(events.KindStepFinished, t.runID).
				WithPayload(events.StepFinishedData{Kind: "model"}),
		}
	case types.EventToolStart:
		id := t.toolStartID(evt.Data["id"])
		name, _ := evt.Data["tool"]
		out := []events.Event{
			events.NewEvent(events.KindToolCallStart, t.runID).
				WithMessageID(id).
				WithPayload(events.ToolCallStartData{ToolCallID: id, ToolName: name}),
			events.NewEvent(events.KindStepStarted, t.runID).
				WithMessageID(id).
				WithPayload(events.StepStartedData{Kind: "tool_call"}),
		}
		if input := evt.Data["input"]; input != "" {
			out = append(out,
				events.NewEvent(events.KindToolCallArgs, t.runID).
					WithMessageID(id).
					WithPayload(events.ToolCallArgsData{ToolCallID: id, PartialJSON: input}),
				events.NewEvent(events.KindToolCallEnd, t.runID).
					WithMessageID(id).
					WithPayload(events.ToolCallEndData{ToolCallID: id}),
			)
		}
		return out
	case types.EventToolFinish:
		id := t.toolFinishID(evt.Data["id"])
		success := evt.Data["success"] != "false"
		output := evt.Data["output"]
		return []events.Event{
			events.NewEvent(events.KindToolCallResult, t.runID).
				WithMessageID(id).
				WithPayload(events.ToolCallResultData{ToolCallID: id, Success: success, Output: output}),
			events.NewEvent(events.KindStepFinished, t.runID).
				WithMessageID(id).
				WithPayload(events.StepFinishedData{Kind: "tool_call"}),
		}
	case types.EventCompactStart:
		return []events.Event{t.customPassthrough(events.CustomCompactStart, evt.Data)}
	case types.EventCompactFinish:
		return []events.Event{t.customPassthrough(events.CustomCompactFinish, evt.Data)}
	case types.EventCompactSkip:
		return []events.Event{t.customPassthrough(events.CustomCompactSkip, evt.Data)}
	case types.EventSubagentStart:
		return []events.Event{t.customPassthrough(events.CustomSubagentStart, evt.Data)}
	case types.EventSubagentFinish:
		return []events.Event{t.customPassthrough(events.CustomSubagentFinish, evt.Data)}
	case types.EventTodoUpdate:
		return []events.Event{t.customPassthrough(events.CustomTodoUpdate, evt.Data)}
	case types.EventModelTimeout:
		return []events.Event{t.customPassthrough(events.CustomModelTimeout, evt.Data)}
	case types.EventLoopStart:
		return []events.Event{t.customPassthrough(events.CustomLoopStart, evt.Data)}
	default:
		// Unknown type: passthrough with raw name.
		return []events.Event{t.customPassthrough(string(evt.Type), evt.Data)}
	}
}

func (t *Translator) toolStartID(coreID string) string {
	if coreID != "" {
		t.toolCallIDs[coreID] = coreID
		return coreID
	}
	return t.nextID("tool")
}

func (t *Translator) toolFinishID(coreID string) string {
	if coreID != "" {
		if id, ok := t.toolCallIDs[coreID]; ok {
			delete(t.toolCallIDs, coreID)
			return id
		}
		return coreID
	}
	return t.nextID("tool")
}

func (t *Translator) customPassthrough(name string, data map[string]string) events.Event {
	// Convert map[string]string into map[string]any for JSON friendliness.
	body := make(map[string]any, len(data))
	for k, v := range data {
		body[k] = v
	}
	evt := events.NewEvent(events.KindCustom, t.runID).WithName(name)
	if len(body) > 0 {
		evt = evt.WithPayload(events.CustomData{Name: name, Body: body})
	}
	return evt
}

// pumpDeltas drains resp.Deltas() until it closes, translating and
// publishing each delta. It returns when the channel is closed or ctx
// is cancelled.
func (t *Translator) pumpDeltas(ctx context.Context, deltas <-chan types.Delta, publish func(events.Event)) {
	for {
		select {
		case <-ctx.Done():
			return
		case d, ok := <-deltas:
			if !ok {
				for _, e := range t.CloseText() {
					publish(e)
				}
				return
			}
			for _, e := range t.FromDelta(d) {
				publish(e)
			}
		}
	}
}

// pumpEvents drains core session events until either the channel
// closes or done is closed. done is signaled by pumpDeltas when the
// LLM response stream ends; the session itself never closes its
// subscriber channels. When done is closed, any session events that
// were already buffered are drained before exit so trailing tool.finish
// notifications are not lost to a race with response shutdown.
func (t *Translator) pumpEvents(ctx context.Context, evts <-chan types.Event, publish func(events.Event), done <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			t.drainBufferedEvents(evts, publish)
			return
		case e, ok := <-evts:
			if !ok {
				return
			}
			for _, translated := range t.FromCoreEvent(e) {
				publish(translated)
			}
		}
	}
}

func (t *Translator) drainBufferedEvents(evts <-chan types.Event, publish func(events.Event)) {
	for {
		select {
		case e, ok := <-evts:
			if !ok {
				return
			}
			for _, translated := range t.FromCoreEvent(e) {
				publish(translated)
			}
		default:
			return
		}
	}
}
