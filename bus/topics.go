package bus

import (
	"encoding/base64"
	"strings"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/core/actor/events"
)

// Input message kinds carried on the inbox topic.
const (
	InboxUserText   = "user.text"
	InboxFormSubmit = "form.submit"
	InboxFormCancel = "form.cancel"
)

// InputDelivery controls how user text is scheduled. Empty/normal preserves
// the existing FIFO behavior; steer interrupts an active run and is processed
// before queued normal input.
type InputDelivery string

const (
	DeliveryNormal InputDelivery = "normal"
	DeliverySteer  InputDelivery = "steer"
)

// Status events carried on the status topic.
const (
	StatusCreated      = "created"
	StatusEvicted      = "evicted"
	StatusStopped      = "stopped"
	StatusInboxDropped = "inbox_dropped"
)

func agentTopic(sid, rest string) string {
	return "agent." + encodeSessionID(sid) + "." + rest
}

// encodeSessionID turns an arbitrary session ID into exactly one safe topic
// section. Session IDs can come from transport or storage boundaries, so they
// must never be allowed to inject the eventbus wildcard or section delimiter.
// Raw URL base64 is injective and uses only [a-zA-Z0-9_-]. The prefix also
// gives the empty session ID a non-empty representation.
func encodeSessionID(sid string) string {
	return "s_" + base64.RawURLEncoding.EncodeToString([]byte(sid))
}

// TopicInbox is where consumers publish input messages.
func TopicInbox(sid string) string { return agentTopic(sid, "inbox") }

// TopicPreempt is where consumers publish preemption requests.
func TopicPreempt(sid string) string { return agentTopic(sid, "preempt") }

// TopicRun builds a run lifecycle topic for event ("started",
// "finished", "error").
func TopicRun(sid, event string) string { return agentTopic(sid, "run."+event) }

// TopicReplyContent carries TEXT_MESSAGE_START/CONTENT/END events.
func TopicReplyContent(sid string) string { return agentTopic(sid, "reply.content") }

// TopicReplyReasoning carries aggregated reasoning deltas.
func TopicReplyReasoning(sid string) string { return agentTopic(sid, "reply.reasoning") }

// TopicToolCall carries TOOL_CALL_START/ARGS/END for one tool.
func TopicToolCall(sid, tool string) string {
	return agentTopic(sid, "tool_call."+SanitizeToolName(tool))
}

// TopicToolUse carries TOOL_CALL_RESULT for one tool.
func TopicToolUse(sid, tool string) string {
	return agentTopic(sid, "tool_use."+SanitizeToolName(tool))
}

// TopicCard builds a card topic for event ("emitted", "updated",
// "dismissed").
func TopicCard(sid, event string) string { return agentTopic(sid, "card."+event) }

// TopicForm builds a form topic for event ("requested", "submitted",
// "cancelled").
func TopicForm(sid, event string) string { return agentTopic(sid, "form."+event) }

// TopicObs carries observability custom events (compact.*, subagent.*,
// todo.update, model.timeout, loop.start, step).
func TopicObs(sid, name string) string { return agentTopic(sid, "obs."+name) }

// TopicStatus builds a registry lifecycle topic for event ("created",
// "evicted", "stopped", "inbox_dropped").
func TopicStatus(sid, event string) string { return agentTopic(sid, "status."+event) }

// SanitizeToolName rewrites any character outside [a-zA-Z0-9_-] to '_'
// so tool names (an uncontrolled set coming from the LLM/MCP) cannot
// break the topic grammar. The original name stays in the event
// payload; topics are for routing only.
func SanitizeToolName(name string) string {
	if name == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			// collapse any non-ASCII or delimiter character
			b.WriteByte('_')
		}
	}
	return b.String()
}

// agentTopics lists the wildcard patterns covering all outbound traffic
// of one session. The eventbus "*" matches exactly one section, so
// "agent.<sid>.*" alone would miss e.g. "agent.<sid>.reply.content".
var agentTopics = []string{
	"run.*",
	"reply.content",
	"reply.reasoning",
	"tool_call.*",
	"tool_use.*",
	"card.*",
	"form.*",
	"obs.*",
	"obs.*.*",
	"status.*",
}

// SubscribeAgent subscribes fn to every outbound topic of session sid as
// ONE serial listener registered on all patterns: delivery is strictly
// ordered across the whole topic set (publish order), which streaming
// consumers merging deltas and lifecycle markers rely on. It returns the
// listener id (pass to UnsubscribeAll). cfg selects the queue overflow
// behavior (see eventbus.SerialConfig).
func SubscribeAgent(b *eventbus.Bus, sid string, fn func(Envelope), cfg eventbus.SerialConfig) []string {
	patterns := make([]string, 0, len(agentTopics))
	for _, t := range agentTopics {
		patterns = append(patterns, agentTopic(sid, t))
	}
	return []string{b.SubscribeSerial(patterns, fn, cfg)}
}

// UnsubscribeAll removes listener ids previously returned by
// SubscribeAgent (or SubscribeSerial).
func UnsubscribeAll(b *eventbus.Bus, ids ...string) {
	for _, id := range ids {
		b.Unsubscribe(id)
	}
}

// UserTextInput is the payload schema of inbox user.text envelopes.
type UserTextInput struct {
	Text     string         `json:"text"`
	TurnID   string         `json:"turn_id,omitempty"`
	Delivery InputDelivery  `json:"delivery,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// FormSubmitInput is the payload schema of inbox form.submit envelopes.
type FormSubmitInput struct {
	FormID string         `json:"form_id"`
	Values map[string]any `json:"values,omitempty"`
}

// FormCancelInput is the payload schema of inbox form.cancel envelopes.
type FormCancelInput struct {
	FormID string `json:"form_id"`
}

// PreemptInput is the payload schema of preempt envelopes.
type PreemptInput struct {
	Reason string `json:"reason,omitempty"`
	// Scope is "current" to retain queued turns. Empty means the historical
	// cancel-all behavior.
	Scope string `json:"scope,omitempty"`
}

type PreemptScope string

const (
	PreemptAll     PreemptScope = ""
	PreemptCurrent PreemptScope = "current"
)

// InboxDropped is the payload schema of status.inbox_dropped
// envelopes, echoing the identity of the rejected input so senders
// (e.g. the a2a executor matching on TurnID) can react.
type InboxDropped struct {
	From   string `json:"from,omitempty"`
	TurnID string `json:"turn_id,omitempty"`
	FormID string `json:"form_id,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func inputEnvelope(kind, session, from string, payload any) Envelope {
	evt := events.NewEvent(events.KindCustom, "").WithName(kind)
	evt = evt.WithPayload(payload)
	return Envelope{
		Event:   evt,
		Topic:   TopicInbox(session),
		Session: session,
		From:    from,
		TS:      NextTS(),
	}
}

// NewUserInput builds an inbox envelope carrying UserTextInput.
func NewUserInput(session, from string, in UserTextInput) Envelope {
	return inputEnvelope(InboxUserText, session, from, in)
}

// NewFormSubmit builds an inbox envelope carrying FormSubmitInput.
func NewFormSubmit(session, from string, in FormSubmitInput) Envelope {
	return inputEnvelope(InboxFormSubmit, session, from, in)
}

// NewFormCancel builds an inbox envelope carrying FormCancelInput.
func NewFormCancel(session, from string, in FormCancelInput) Envelope {
	return inputEnvelope(InboxFormCancel, session, from, in)
}

// NewPreempt builds a preempt envelope carrying PreemptInput.
func NewPreempt(session, from, reason string) Envelope {
	return NewScopedPreempt(session, from, reason, PreemptAll)
}

// NewScopedPreempt builds a preemption request with an explicit queue scope.
func NewScopedPreempt(session, from, reason string, scope PreemptScope) Envelope {
	evt := events.NewEvent(events.KindCustom, "").WithName("preempt")
	evt = evt.WithPayload(PreemptInput{Reason: reason, Scope: string(scope)})
	return Envelope{
		Event:   evt,
		Topic:   TopicPreempt(session),
		Session: session,
		From:    from,
		TS:      NextTS(),
	}
}

// NewStatus builds a status lifecycle envelope.
func NewStatus(session, actorID, event string) Envelope {
	evt := events.NewEvent(events.KindCustom, "").WithName("status." + event)
	evt = evt.WithPayload(map[string]string{"actor_id": actorID})
	return Envelope{
		Event:   evt,
		Topic:   TopicStatus(session, event),
		Session: session,
		ActorID: actorID,
		TS:      NextTS(),
	}
}

// PublishStatus publishes a status lifecycle envelope on the bus.
func PublishStatus(b *eventbus.Bus, session, actorID, event string) {
	b.Publish(TopicStatus(session, event), NewStatus(session, actorID, event))
}
