package events

// Step (item-level) payloads.

// StepStartedData signals that an item (message / tool_call / model call
// / card / form) has started.
type StepStartedData struct {
	// Kind is the item kind: "message" | "tool_call" | "model" |
	// "card" | "form" | "custom".
	Kind string `json:"kind"`
}

// StepFinishedData signals that the previously started item finished.
type StepFinishedData struct {
	Kind string `json:"kind,omitempty"`
}
