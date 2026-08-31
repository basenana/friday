package cards

// ManagedImageCard displays a workspace-managed image artifact.
type ManagedImageCard struct {
	SchemaVersion int    `json:"schema_version"`
	Path          string `json:"path"`
	Alt           string `json:"alt"`
}

// CardKind implements Component.
func (ManagedImageCard) CardKind() Kind { return KindImage }
