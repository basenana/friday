package bridge

import (
	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/core/actor/events"
)

// RouteEvent maps an actor event to its bus topic for session sid.
// The second return is false when the event is not routable and must
// be dropped at the bridge (RAW, STATE_*, unknown CUSTOM names).
//
// The tracker is updated as a side effect: TOOL_CALL_START records the
// tool name for the call id, TOOL_CALL_RESULT evicts it, and
// RUN_FINISHED resets the map (bounded memory across turns).
func RouteEvent(sid string, evt events.Event, tracker *ToolCallTracker) (string, bool) {
	switch evt.Type {
	case events.KindRunStarted:
		return bus.TopicRun(sid, "started"), true
	case events.KindRunFinished:
		defer tracker.Reset()
		return bus.TopicRun(sid, "finished"), true
	case events.KindRunError:
		return bus.TopicRun(sid, "error"), true

	case events.KindTextMessageStart, events.KindTextMessageContent, events.KindTextMessageEnd:
		return bus.TopicReplyContent(sid), true

	case events.KindToolCallStart:
		var d events.ToolCallStartData
		if events.DecodePayload(evt, &d) == nil && d.ToolCallID != "" {
			tracker.Record(d.ToolCallID, bus.SanitizeToolName(d.ToolName))
		}
		return bus.TopicToolCall(sid, trackerName(tracker, evt)), true
	case events.KindToolCallArgs, events.KindToolCallEnd:
		return bus.TopicToolCall(sid, trackerName(tracker, evt)), true
	case events.KindToolCallResult:
		name := trackerName(tracker, evt)
		var d events.ToolCallResultData
		if events.DecodePayload(evt, &d) == nil && d.ToolCallID != "" {
			tracker.Evict(d.ToolCallID)
		}
		return bus.TopicToolUse(sid, name), true

	case events.KindStepStarted, events.KindStepFinished:
		return bus.TopicObs(sid, "step"), true

	case events.KindCustom:
		return routeCustom(sid, evt, tracker)
	}
	return "", false
}

func routeCustom(sid string, evt events.Event, _ *ToolCallTracker) (string, bool) {
	switch evt.Name {
	case events.CustomReasoningDelta:
		return bus.TopicReplyReasoning(sid), true
	case events.CustomCardEmitted:
		return bus.TopicCard(sid, "emitted"), true
	case events.CustomCardUpdated:
		return bus.TopicCard(sid, "updated"), true
	case events.CustomCardDismissed:
		return bus.TopicCard(sid, "dismissed"), true
	case events.CustomFormRequested:
		return bus.TopicForm(sid, "requested"), true
	case events.CustomFormSubmitted:
		return bus.TopicForm(sid, "submitted"), true
	case events.CustomFormCancelled:
		return bus.TopicForm(sid, "cancelled"), true
	case events.CustomCompactStart, events.CustomCompactFinish, events.CustomCompactSkip,
		events.CustomSubagentStart, events.CustomSubagentFinish,
		events.CustomTodoUpdate, events.CustomModelTimeout, events.CustomLoopStart,
		events.CustomInputAccepted:
		return bus.TopicObs(sid, evt.Name), true
	}
	return "", false
}

// trackerName resolves the tool name for args/end/result events from
// the tracker, falling back to "_" for unknown ids (e.g. a RESULT
// without a preceding START).
func trackerName(tracker *ToolCallTracker, evt events.Event) string {
	id := evt.MessageID
	if id == "" {
		return "_"
	}
	if name, ok := tracker.Lookup(id); ok && name != "" {
		return name
	}
	return "_"
}
