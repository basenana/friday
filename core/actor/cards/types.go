// Package cards defines declarative card and form types aligned with
// A2UI v0.9. All cards are plain data (no executable code); the actor
// layer validates payloads against a fixed catalog before emitting them.
package cards

// Kind is the card discriminator. It maps directly to the A2UI custom
// component kind used by the rich-card system.
type Kind string

const (
	KindFile        Kind = "file"
	KindTable       Kind = "table"
	KindDiff        Kind = "diff"
	KindForm        Kind = "form"
	KindPlan        Kind = "plan"
	KindImage       Kind = "image"
	KindChart       Kind = "chart"
	KindCustom      Kind = "custom"
	KindDecision    Kind = "decision"
	KindIGVCommand  Kind = "igv-command"
	KindMermaid     Kind = "mermaid"
	KindHTMLPreview Kind = "html-preview"
	KindSpreadsheet Kind = "spreadsheet"
)

// allKinds is the whitelist of accepted card kinds. Anything outside
// this set is rejected by the Catalog.
var allKinds = map[Kind]struct{}{
	KindFile:        {},
	KindTable:       {},
	KindDiff:        {},
	KindForm:        {},
	KindPlan:        {},
	KindImage:       {},
	KindChart:       {},
	KindCustom:      {},
	KindDecision:    {},
	KindIGVCommand:  {},
	KindMermaid:     {},
	KindHTMLPreview: {},
	KindSpreadsheet: {},
}

// IsValid reports whether k is a known card kind.
func (k Kind) IsValid() bool {
	_, ok := allKinds[k]
	return ok
}

// Component is implemented by all card payload types so the Catalog
// can treat them uniformly. Card types live in this package; the
// interface is intentionally narrow.
type Component interface {
	CardKind() Kind
}

// SourceRef identifies a managed, workspace-relative card artifact.
type SourceRef struct {
	Path   string `json:"path"`
	Format string `json:"format"`
}

// RichColumnFormat describes the optional wire-formatting properties for a
// versioned table or spreadsheet column.
type RichColumnFormat struct {
	Locale                *string `json:"locale,omitempty"`
	Currency              *string `json:"currency,omitempty"`
	MinimumFractionDigits *int    `json:"minimum_fraction_digits,omitempty"`
	MaximumFractionDigits *int    `json:"maximum_fraction_digits,omitempty"`
}

// RichColumn describes a column in a versioned table or spreadsheet.
type RichColumn struct {
	Key    string            `json:"key"`
	Label  string            `json:"label"`
	Type   string            `json:"type"`
	Align  string            `json:"align,omitempty"`
	Format *RichColumnFormat `json:"format,omitempty"`
}
