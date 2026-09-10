package events

// Custom event payloads. The Event.Name field discriminates between
// these; payloads are intentionally permissive maps to keep the actor
// layer agnostic to specific card schemas (those live in the cards
// package and the frontend).

// CustomData is a generic carrier for card / form / domain events.
type CustomData struct {
	// Name repeats the Event.Name (so a single Payload decode yields
	// everything needed). Optional.
	Name string `json:"name,omitempty"`
	// Body is the arbitrary JSON body. For card.emitted it is an A2UI
	// component; for form.requested an A2UI Form schema; for
	// card.updated {card_id, patch}; etc.
	Body map[string]any `json:"body,omitempty"`
}

// CardEmittedBody is the structured body for name="card.emitted".
type CardEmittedBody struct {
	CardID    string         `json:"card_id"`
	Kind      string         `json:"kind"` // file | table | spreadsheet | diff | mermaid | html-preview | plan | image | chart | decision | igv-command | custom
	Title     string         `json:"title,omitempty"`
	Component map[string]any `json:"component"`
}

// CardUpdatedBody is the structured body for name="card.updated".
type CardUpdatedBody struct {
	CardID string        `json:"card_id"`
	Patch  []interface{} `json:"patch"` // RFC 6902 JSON Patch ops
}

// CardDismissedBody is the structured body for name="card.dismissed".
type CardDismissedBody struct {
	CardID string `json:"card_id"`
}

// FormRequestedBody is the structured body for name="form.requested".
// Schema is the full A2UI Form schema (see cards.FormSchema).
type FormRequestedBody struct {
	FormID string         `json:"form_id"`
	Schema map[string]any `json:"schema"`
}

// FormSubmittedBody is the structured body for name="form.submitted".
type FormSubmittedBody struct {
	FormID string         `json:"form_id"`
	Values map[string]any `json:"values"`
}

// FormCancelledBody is the structured body for name="form.cancelled".
type FormCancelledBody struct {
	FormID string `json:"form_id"`
}

// ReasoningDeltaBody is the structured body for name="reasoning.delta".
type ReasoningDeltaBody struct {
	Content string `json:"content"`
}

// InputAcceptedBody records the complete user input that caused a run. It is
// emitted immediately after RUN_STARTED so event logs can rebuild a transcript
// without having to infer user messages from the intentionally short preview.
type InputAcceptedBody struct {
	TurnID   string `json:"turn_id"`
	Text     string `json:"text"`
	Delivery string `json:"delivery,omitempty"`
}
