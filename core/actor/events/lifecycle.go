package events

// Lifecycle event payloads.

// RunStartedData is the payload for RUN_STARTED events.
type RunStartedData struct {
	// ActorID is the actor instance identifier.
	ActorID string `json:"actorId,omitempty"`
	// Batch is the number of inbox messages coalesced into this run.
	Batch int `json:"batch,omitempty"`
	// Preview is a short preview of the coalesced user message.
	Preview string `json:"preview,omitempty"`
}

// RunFinishedData is the payload for RUN_FINISHED events.
type RunFinishedData struct {
	// Interrupts lists pending interrupts (e.g. open forms) at the end
	// of the run. Empty when the run completed normally.
	Interrupts []Interrupt `json:"interrupts,omitempty"`
}

// Interrupt describes a pending human-in-the-loop interrupt carried by
// RUN_FINISHED, aligned with AG-UI's interrupt outcome.
type Interrupt struct {
	Type string `json:"type"`           // e.g. "form"
	ID   string `json:"id"`             // form/card id
	Name string `json:"name,omitempty"` // custom discriminator
}

// RunErrorData is the payload for RUN_ERROR events.
type RunErrorData struct {
	Message string `json:"message"`
}
