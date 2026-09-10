package bridge

import (
	"testing"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/core/actor/events"
)

func toolStart(id, name string) events.Event {
	return events.NewEvent(events.KindToolCallStart, "run-1").
		WithMessageID(id).
		WithPayload(events.ToolCallStartData{ToolCallID: id, ToolName: name})
}

func toolEvt(kind events.EventKind, id string) events.Event {
	return events.NewEvent(kind, "run-1").WithMessageID(id).
		WithPayload(events.ToolCallResultData{ToolCallID: id, Success: true, Output: "ok"})
}

func TestRouteEvent(t *testing.T) {
	sid := "s1"
	cases := []struct {
		name string
		evt  events.Event
		want string
	}{
		{"run started", events.NewEvent(events.KindRunStarted, "r"), bus.TopicRun(sid, "started")},
		{"run finished", events.NewEvent(events.KindRunFinished, "r"), bus.TopicRun(sid, "finished")},
		{"run error", events.NewEvent(events.KindRunError, "r"), bus.TopicRun(sid, "error")},
		{"text start", events.NewEvent(events.KindTextMessageStart, "r"), bus.TopicReplyContent(sid)},
		{"text content", events.NewEvent(events.KindTextMessageContent, "r"), bus.TopicReplyContent(sid)},
		{"text end", events.NewEvent(events.KindTextMessageEnd, "r"), bus.TopicReplyContent(sid)},
		{"reasoning", events.NewEvent(events.KindCustom, "r").WithName(events.CustomReasoningDelta), bus.TopicReplyReasoning(sid)},
		{"card emitted", events.NewEvent(events.KindCustom, "r").WithName(events.CustomCardEmitted), bus.TopicCard(sid, "emitted")},
		{"card updated", events.NewEvent(events.KindCustom, "r").WithName(events.CustomCardUpdated), bus.TopicCard(sid, "updated")},
		{"card dismissed", events.NewEvent(events.KindCustom, "r").WithName(events.CustomCardDismissed), bus.TopicCard(sid, "dismissed")},
		{"form requested", events.NewEvent(events.KindCustom, "r").WithName(events.CustomFormRequested), bus.TopicForm(sid, "requested")},
		{"form submitted", events.NewEvent(events.KindCustom, "r").WithName(events.CustomFormSubmitted), bus.TopicForm(sid, "submitted")},
		{"form cancelled", events.NewEvent(events.KindCustom, "r").WithName(events.CustomFormCancelled), bus.TopicForm(sid, "cancelled")},
		{"compact start", events.NewEvent(events.KindCustom, "r").WithName(events.CustomCompactStart), bus.TopicObs(sid, "compact.start")},
		{"subagent finish", events.NewEvent(events.KindCustom, "r").WithName(events.CustomSubagentFinish), bus.TopicObs(sid, "subagent.finish")},
		{"todo update", events.NewEvent(events.KindCustom, "r").WithName(events.CustomTodoUpdate), bus.TopicObs(sid, "todo.update")},
		{"model timeout", events.NewEvent(events.KindCustom, "r").WithName(events.CustomModelTimeout), bus.TopicObs(sid, "model.timeout")},
		{"loop start", events.NewEvent(events.KindCustom, "r").WithName(events.CustomLoopStart), bus.TopicObs(sid, "loop.start")},
		{"input accepted", events.NewEvent(events.KindCustom, "r").WithName(events.CustomInputAccepted), bus.TopicObs(sid, "input.accepted")},
		{"step started", events.NewEvent(events.KindStepStarted, "r"), bus.TopicObs(sid, "step")},
		{"step finished", events.NewEvent(events.KindStepFinished, "r"), bus.TopicObs(sid, "step")},
		{"raw dropped", events.NewEvent(events.KindRaw, "r"), ""},
		{"state dropped", events.NewEvent(events.KindStateSnapshot, "r"), ""},
		{"unknown custom dropped", events.NewEvent(events.KindCustom, "r").WithName("mystery.event"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tracker := NewToolCallTracker()
			got, ok := RouteEvent(sid, tc.evt, tracker)
			if tc.want == "" {
				if ok {
					t.Fatalf("expected drop, got topic %q", got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("RouteEvent = (%q, %v), want (%q, true)", got, ok, tc.want)
			}
		})
	}
}

func TestRouteEventToolCallLifecycle(t *testing.T) {
	sid, tracker := "s1", NewToolCallTracker()

	// START records the tool name for the call id.
	if got, ok := RouteEvent(sid, toolStart("tc1", "fs.read_file"), tracker); !ok || got != bus.TopicToolCall(sid, "fs.read_file") {
		t.Fatalf("start routed to %q (ok=%v), want tool_call.fs.read_file", got, ok)
	}
	// ARGS/END resolve the name via the tracker.
	if got, ok := RouteEvent(sid, toolEvt(events.KindToolCallArgs, "tc1"), tracker); !ok || got != bus.TopicToolCall(sid, "fs.read_file") {
		t.Fatalf("args routed to %q (ok=%v), want tool_call.fs.read_file", got, ok)
	}
	if got, ok := RouteEvent(sid, toolEvt(events.KindToolCallEnd, "tc1"), tracker); !ok || got != bus.TopicToolCall(sid, "fs.read_file") {
		t.Fatalf("end routed to %q (ok=%v), want tool_call.fs.read_file", got, ok)
	}
	// RESULT lands on tool_use and evicts the id.
	if got, ok := RouteEvent(sid, toolEvt(events.KindToolCallResult, "tc1"), tracker); !ok || got != bus.TopicToolUse(sid, "fs.read_file") {
		t.Fatalf("result routed to %q (ok=%v), want tool_use.fs.read_file", got, ok)
	}
	// After eviction the name is unknown.
	if got, ok := RouteEvent(sid, toolEvt(events.KindToolCallArgs, "tc1"), tracker); !ok || got != bus.TopicToolCall(sid, "_") {
		t.Fatalf("post-eviction args routed to %q (ok=%v), want tool_call._", got, ok)
	}
	// RESULT without a preceding START falls back too.
	if got, ok := RouteEvent(sid, toolEvt(events.KindToolCallResult, "ghost"), tracker); !ok || got != bus.TopicToolUse(sid, "_") {
		t.Fatalf("orphan result routed to %q (ok=%v), want tool_use._", got, ok)
	}
	// Tool names are sanitized into the topic grammar.
	if got, ok := RouteEvent(sid, toolStart("tc2", "mcp__weird/name.v2"), tracker); !ok || got != bus.TopicToolCall(sid, "mcp__weird_name_v2") {
		t.Fatalf("sanitized start routed to %q (ok=%v)", got, ok)
	}
	// RUN_FINISHED resets the tracker.
	if got, ok := RouteEvent(sid, toolStart("tc3", "bash"), tracker); !ok || got != bus.TopicToolCall(sid, "bash") {
		t.Fatalf("setup failed: %q", got)
	}
	RouteEvent(sid, events.NewEvent(events.KindRunFinished, "run-1"), tracker)
	if got, ok := RouteEvent(sid, toolEvt(events.KindToolCallArgs, "tc3"), tracker); !ok || got != bus.TopicToolCall(sid, "_") {
		t.Fatalf("args after run finished routed to %q (ok=%v), want tool_call._", got, ok)
	}
}

func TestSanitizeToolName(t *testing.T) {
	cases := map[string]string{
		"bash":            "bash",
		"fs.read_file":    "fs_read_file",
		"weird/tool name": "weird_tool_name",
		"":                "_",
		"mcp__x-1":        "mcp__x-1",
		"中文":              "__",
	}
	for in, want := range cases {
		if got := bus.SanitizeToolName(in); got != want {
			t.Fatalf("SanitizeToolName(%q) = %q, want %q", in, got, want)
		}
	}
}
