package events

import "encoding/json"

// jsonMarshal is a thin wrapper to keep the events package dependencies
// minimal and to provide a single seam for future encoding tweaks.
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// jsonRaw returns a RawMessage from a string without copying pitfalls.
func jsonRaw(s string) json.RawMessage {
	return json.RawMessage(s)
}

// DecodePayload unmarshals the event payload into the target.
// Returns nil error when payload is empty.
func DecodePayload(e Event, target any) error {
	if len(e.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(e.Payload, target)
}
