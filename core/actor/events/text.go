package events

// Text message payloads (Start-Content-End pattern).

// TextMessageStartData marks the beginning of an assistant text block.
type TextMessageStartData struct {
	// Role is always "assistant" for actor-emitted text. Included for
	// AG-UI wire compatibility.
	Role string `json:"role,omitempty"`
}

// TextMessageContentData carries one delta chunk of assistant text.
type TextMessageContentData struct {
	Content string `json:"content"`
}

// TextMessageEndData marks the end of an assistant text block.
type TextMessageEndData struct{}
