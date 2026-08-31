package cards

// MermaidCard displays a Mermaid diagram source document.
type MermaidCard struct {
	SchemaVersion int    `json:"schema_version"`
	Source        string `json:"source"`
}

// CardKind implements Component.
func (MermaidCard) CardKind() Kind { return KindMermaid }
