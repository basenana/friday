package cards

// FieldType enumerates the supported A2UI Form field types.
type FieldType string

const (
	FieldText        FieldType = "text"
	FieldTextarea    FieldType = "textarea"
	FieldNumber      FieldType = "number"
	FieldBoolean     FieldType = "boolean"
	FieldSelect      FieldType = "select"
	FieldMultiSelect FieldType = "multiselect"
	FieldDate        FieldType = "date"
	FieldList        FieldType = "list"
	FieldObject      FieldType = "object"
	FieldFile        FieldType = "file"
)

// allFieldTypes is the whitelist of accepted field types.
var allFieldTypes = map[FieldType]struct{}{
	FieldText:        {},
	FieldTextarea:    {},
	FieldNumber:      {},
	FieldBoolean:     {},
	FieldSelect:      {},
	FieldMultiSelect: {},
	FieldDate:        {},
	FieldList:        {},
	FieldObject:      {},
	FieldFile:        {},
}

// IsValid reports whether t is a known field type.
func (t FieldType) IsValid() bool {
	_, ok := allFieldTypes[t]
	return ok
}

// Option is a select / multiselect option.
type Option struct {
	Label       string `json:"label"`
	Value       any    `json:"value"`
	Description string `json:"description,omitempty"`
}

// Field is a single A2UI Form field. Only a subset of constraints
// apply per FieldType; the Catalog validates compatibility.
type Field struct {
	Name     string    `json:"name"`
	Label    string    `json:"label,omitempty"`
	Type     FieldType `json:"type"`
	Required bool      `json:"required,omitempty"`
	Default  any       `json:"default,omitempty"`
	Options  []Option  `json:"options,omitempty"` // for select / multiselect
	Min      *float64  `json:"min,omitempty"`     // for number
	Max      *float64  `json:"max,omitempty"`     // for number
	Step     *float64  `json:"step,omitempty"`    // for number
	Help     string    `json:"help,omitempty"`
	// Placeholder is a UI hint only.
	Placeholder string `json:"placeholder,omitempty"`
	// Fields holds nested field definitions for FieldTypeObject / FieldList.
	Fields []Field `json:"fields,omitempty"`
}

// FormSchema is the A2UI Form payload emitted by request_form. The
// actor emits this verbatim (as map[string]any) inside the
// form.requested Custom event.
type FormSchema struct {
	Title       string  `json:"title,omitempty"`
	Description string  `json:"description,omitempty"`
	Fields      []Field `json:"fields"`
	SubmitLabel string  `json:"submit_label,omitempty"`
	CancelLabel string  `json:"cancel_label,omitempty"`
}

// CardKind implements Component.
func (FormSchema) CardKind() Kind { return KindForm }

// FormOutcome is what request_form returns to the agent when the user
// submits or cancels.
type FormOutcome struct {
	Values    map[string]any `json:"values,omitempty"`
	Cancelled bool           `json:"cancelled,omitempty"`
}
