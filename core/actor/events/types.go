// Package events defines the actor-layer event types.
//
// All event types are aligned with the AG-UI protocol wire format so they
// can be consumed directly by AG-UI-compatible frontends. The actor package
// translates internal core events/deltas into the types defined here.
package events

import (
	"encoding/json"
	"time"
)

// EventKind is the AG-UI event type discriminator.
type EventKind string

const (
	// Lifecycle
	KindRunStarted  EventKind = "RUN_STARTED"
	KindRunFinished EventKind = "RUN_FINISHED"
	KindRunError    EventKind = "RUN_ERROR"

	// Step (item-level lifecycle)
	KindStepStarted  EventKind = "STEP_STARTED"
	KindStepFinished EventKind = "STEP_FINISHED"

	// Text message streaming (Start-Content-End pattern)
	KindTextMessageStart   EventKind = "TEXT_MESSAGE_START"
	KindTextMessageContent EventKind = "TEXT_MESSAGE_CONTENT"
	KindTextMessageEnd     EventKind = "TEXT_MESSAGE_END"

	// Tool call streaming
	KindToolCallStart  EventKind = "TOOL_CALL_START"
	KindToolCallArgs   EventKind = "TOOL_CALL_ARGS"
	KindToolCallEnd    EventKind = "TOOL_CALL_END"
	KindToolCallResult EventKind = "TOOL_CALL_RESULT"

	// State (reserved for future use)
	KindStateSnapshot EventKind = "STATE_SNAPSHOT"
	KindStateDelta    EventKind = "STATE_DELTA"

	// Custom carries card / form / domain-specific events. The Name field
	// distinguishes the payload kind (e.g. "card.emitted", "form.requested").
	KindCustom EventKind = "CUSTOM"

	// Raw passthrough of the original core event.
	KindRaw EventKind = "RAW"
)

// Custom event name conventions. These ride on KindCustom and are
// differentiated by the Event.Name field.
const (
	// Card events (payload is an A2UI component or patch).
	CustomCardEmitted   = "card.emitted"
	CustomCardUpdated   = "card.updated"
	CustomCardDismissed = "card.dismissed"

	// Form events (interrupt-style structured input).
	CustomFormRequested = "form.requested"
	CustomFormSubmitted = "form.submitted"
	CustomFormCancelled = "form.cancelled"

	// Reasoning delta (no first-class AG-UI v1 reasoning event; ride Custom).
	CustomReasoningDelta = "reasoning.delta"

	// Core passthrough names (1:1 with core EventType values).
	CustomCompactStart   = "compact.start"
	CustomCompactFinish  = "compact.finish"
	CustomCompactSkip    = "compact.skip"
	CustomSubagentStart  = "subagent.start"
	CustomSubagentFinish = "subagent.finish"
	CustomTodoUpdate     = "todo.update"
	CustomModelTimeout   = "model.timeout"
	CustomLoopStart      = "loop.start"
)

// Event is the universal envelope. Fields follow AG-UI wire naming
// (camelCase JSON tags). Internal tracking fields (ActorID, Seq) are
// not serialized.
type Event struct {
	Type        EventKind       `json:"type"`
	RunID       string          `json:"runId,omitempty"`
	MessageID   string          `json:"messageId,omitempty"`
	ParentRunID string          `json:"parentRunId,omitempty"`
	Name        string          `json:"name,omitempty"` // only for CUSTOM
	Timestamp   time.Time       `json:"timestamp"`
	Payload     json.RawMessage `json:"payload,omitempty"` // structured payload

	// internal tracking (not serialized on the wire)
	ActorID string `json:"-"`
	Seq     int64  `json:"-"`
}

// NewEvent constructs an Event with the given kind and current timestamp.
func NewEvent(kind EventKind, runID string) Event {
	return Event{
		Type:      kind,
		RunID:     runID,
		Timestamp: time.Now(),
	}
}

// WithPayload attaches a JSON-marshaled payload to the event.
func (e Event) WithPayload(v any) Event {
	if v == nil {
		return e
	}
	raw, err := jsonMarshal(v)
	if err != nil {
		// fall back to an error marker; never panic on event payload
		raw = jsonRaw(`{"_payload_error":"` + err.Error() + `"}`)
	}
	e.Payload = raw
	return e
}

// WithName sets the Custom-event discriminator name.
func (e Event) WithName(name string) Event {
	e.Name = name
	return e
}

// WithMessageID attaches a message/item id used to correlate deltas.
func (e Event) WithMessageID(id string) Event {
	e.MessageID = id
	return e
}
