package actor

import "time"

// EventType enumerates the AG-UI style events emitted by an Actor via its
// outcome channel. Adapters subscribe to these (through eventbus) and
// translate them into protocol-specific updates (A2A, ACP, AG-UI SSE, ...).
type EventType string

const (
	// Lifecycle — mark the start/end of a single agent run.
	EventRunStarted   EventType = "RUN_STARTED"
	EventRunFinished  EventType = "RUN_FINISHED"
	EventRunError     EventType = "RUN_ERROR"
	EventStepStarted  EventType = "STEP_STARTED"
	EventStepFinished EventType = "STEP_FINISHED"

	// Text streaming — three-phase (Start-Content-End).
	EventTextMessageStart   EventType = "TEXT_MESSAGE_START"
	EventTextMessageContent EventType = "TEXT_MESSAGE_CONTENT"
	EventTextMessageEnd     EventType = "TEXT_MESSAGE_END"

	// Tool calls.
	EventToolCallStart  EventType = "TOOL_CALL_START"
	EventToolCallEnd    EventType = "TOOL_CALL_END"
	EventToolCallResult EventType = "TOOL_CALL_RESULT"

	// Activity (cards: plan / search / progress ...).
	EventActivitySnapshot EventType = "ACTIVITY_SNAPSHOT"
	EventActivityDelta    EventType = "ACTIVITY_DELTA"

	// Reasoning stream.
	EventReasoningStart          EventType = "REASONING_START"
	EventReasoningMessageContent EventType = "REASONING_MESSAGE_CONTENT"
	EventReasoningEnd            EventType = "REASONING_END"

	// State.
	EventMessagesSnapshot EventType = "MESSAGES_SNAPSHOT"

	// Special — anything not covered above.
	EventCustom EventType = "CUSTOM"
)

// Event is the standard event an Actor pushes to its outcome channel.
// One Event per atomic happening; consumers must not assume any field beyond
// Type/SessionID/RunID/Seq/Timestamp is present.
type Event struct {
	Type      EventType      `json:"type"`
	SessionID string         `json:"session_id"`
	RunID     string         `json:"run_id"`
	MessageID string         `json:"message_id,omitempty"` // for text/reasoning streams
	Seq       int64          `json:"seq"`                  // monotonically increasing per actor
	Timestamp time.Time      `json:"timestamp"`
	Data      map[string]any `json:"data,omitempty"`       // type-specific payload
}

// Topic builds the eventbus topic for a specific event type of a session.
// Used by adapter layer when calling eventbus.Publish.
func Topic(sessionID string, evtType EventType) string {
	return "actor." + sessionID + "." + string(evtType)
}

// TopicAll is the wildcard topic matching every event for a session.
// Used by adapter layer when calling eventbus.Subscribe.
func TopicAll(sessionID string) string {
	return "actor." + sessionID + ".*"
}
