package cards

// HTMLPreviewCard displays a managed HTML artifact in a safe preview.
type HTMLPreviewCard struct {
	SchemaVersion int    `json:"schema_version"`
	Path          string `json:"path"`
}

// CardKind implements Component.
func (HTMLPreviewCard) CardKind() Kind { return KindHTMLPreview }
