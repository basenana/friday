package cards

// Column describes a single column in a TableCard.
type Column struct {
	Name string `json:"name"`
	// Type is a UI hint (e.g. "string", "number", "bool"). Optional.
	Type string `json:"type,omitempty"`
}

// TableCard is a declarative table payload.
type TableCard struct {
	Title   string   `json:"title,omitempty"`
	Columns []Column `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// CardKind implements Component.
func (TableCard) CardKind() Kind { return KindTable }

// RichTableCard is the versioned table wire contract.
type RichTableCard struct {
	SchemaVersion int          `json:"schema_version"`
	Title         string       `json:"title,omitempty"`
	Columns       []RichColumn `json:"columns"`
	Rows          *[]any       `json:"rows,omitempty"`
	Source        *SourceRef   `json:"source,omitempty"`
}

// CardKind implements Component.
func (RichTableCard) CardKind() Kind { return KindTable }
