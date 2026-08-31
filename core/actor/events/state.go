package events

// State payloads (reserved). StateSnapshot and StateDelta currently have
// no producers in the MVP; the kinds are defined so that subscribers can
// switch over them today.

// StateSnapshotData carries a full actor-state snapshot.
type StateSnapshotData struct {
	State map[string]any `json:"state"`
}

// StateDeltaData carries a JSON Patch (RFC 6902) delta to the previous
// state.
type StateDeltaData struct {
	Patch []interface{} `json:"delta"`
}
