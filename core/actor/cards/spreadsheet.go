package cards

// SpreadsheetCard displays tabular data or a managed spreadsheet source.
type SpreadsheetCard struct {
	SchemaVersion int          `json:"schema_version"`
	Title         string       `json:"title,omitempty"`
	Columns       []RichColumn `json:"columns"`
	Rows          *[]any       `json:"rows,omitempty"`
	Source        *SourceRef   `json:"source,omitempty"`
}

// CardKind implements Component.
func (SpreadsheetCard) CardKind() Kind { return KindSpreadsheet }
