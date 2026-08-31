package events

// Tool call payloads (Start-Args-End + Result).
//
// Wire format targets AG-UI v1: every event carries `tool_call_id` so
// consumers (WS handlers, frontend adapters, persistent event stores)
// can correlate Args/End/Result to the originating Start without
// relying on Event.MessageID metadata, which transport layers may
// drop. The actor's translator populates ToolCallID from the same
// source as Event.MessageID (toolStartID / toolFinishID).

// ToolCallStartData marks the beginning of a tool call.
type ToolCallStartData struct {
	ToolCallID string `json:"tool_call_id"`
	// ToolName is the tool's identifier as the LLM sees it.
	// JSON tag follows AG-UI v1 (`tool_name`).
	ToolName string `json:"tool_name"`
	// Label is an optional human-readable label for the call
	// (e.g. a localized verb or the tool's display name). Frontends
	// may render this in place of ToolName. Optional.
	Label string `json:"label,omitempty"`
	// OriginSessionID marks the session the tool call actually ran in
	// when the event is relayed from a subagent (e.g. a platform
	// delegation turn) into a parent stream. Empty for native calls.
	OriginSessionID string `json:"origin_session_id,omitempty"`
}

// ToolCallArgsData carries a partial-JSON delta of the tool arguments.
type ToolCallArgsData struct {
	ToolCallID string `json:"tool_call_id"`
	// PartialJSON is a fragment of the arguments JSON being streamed.
	// JSON tag follows AG-UI v1 (`args`).
	PartialJSON string `json:"args"`
}

// ToolCallEndData marks the end of the argument streaming for a tool call.
type ToolCallEndData struct {
	ToolCallID string `json:"tool_call_id"`
}

// ToolCallResultData carries the final result of a tool invocation.
type ToolCallResultData struct {
	ToolCallID string `json:"tool_call_id"`
	Success    bool   `json:"success"`
	Output     string `json:"output"`
	// OriginSessionID mirrors ToolCallStartData.OriginSessionID so the
	// finish side of a relayed pair stays self-describing.
	OriginSessionID string `json:"origin_session_id,omitempty"`
}
