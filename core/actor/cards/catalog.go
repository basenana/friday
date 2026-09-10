package cards

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

const (
	maxInlineRows    = 200
	maxColumns       = 256
	maxPayloadBytes  = 256 * 1024
	schemaVersionOne = 1
)

// Catalog validates card payloads against the supported A2UI kinds.
type Catalog struct{}

// Default is the default catalog instance accepting all in-package card kinds
// and the built-in field types.
var Default = Catalog{}

// ValidateKind reports whether kind is an accepted card kind.
func (c Catalog) ValidateKind(kind Kind) error {
	if !kind.IsValid() {
		return fmt.Errorf("cards: unknown card kind %q", kind)
	}
	return nil
}

// ValidateField reports whether f passes minimal schema checks.
func (c Catalog) ValidateField(f Field) error {
	if f.Name == "" {
		return fmt.Errorf("cards: field missing name")
	}
	if !f.Type.IsValid() {
		return fmt.Errorf("cards: field %q has unknown type %q", f.Name, f.Type)
	}
	switch f.Type {
	case FieldSelect, FieldMultiSelect:
		if len(f.Options) == 0 {
			return fmt.Errorf("cards: field %q of type %s requires options", f.Name, f.Type)
		}
	}
	for _, child := range f.Fields {
		if err := c.ValidateField(child); err != nil {
			return fmt.Errorf("cards: field %q: %w", f.Name, err)
		}
	}
	return nil
}

// ValidateForm validates a full form schema.
func (c Catalog) ValidateForm(s FormSchema) error {
	if len(s.Fields) == 0 {
		return fmt.Errorf("cards: form schema has no fields")
	}
	for _, f := range s.Fields {
		if err := c.ValidateField(f); err != nil {
			return err
		}
	}
	return nil
}

// NormalizeComponent validates and normalizes a raw component payload for the
// given kind. Versioned rich cards reject unknown fields; legacy payloads keep
// their historical compatibility paths.
func (c Catalog) NormalizeComponent(kind Kind, raw map[string]any, validateFilePath func(string) error) (map[string]any, error) {
	if raw == nil {
		return nil, fmt.Errorf("cards: nil component")
	}
	if err := c.ValidateKind(kind); err != nil {
		return nil, err
	}

	switch kind {
	case KindFile:
		var card FileCard
		if err := decodeComponent(raw, &card); err != nil {
			return nil, fmt.Errorf("cards: invalid file component: %w", err)
		}
		if card.Path == "" {
			return nil, fmt.Errorf("cards: file card path is required")
		}
		if validateFilePath != nil {
			if err := validateFilePath(card.Path); err != nil {
				return nil, fmt.Errorf("cards: invalid file card path: %w", err)
			}
		}
		return MarshalComponent(card)
	case KindTable:
		return normalizeTable(raw, validateFilePath)
	case KindSpreadsheet:
		return normalizeSpreadsheet(raw, validateFilePath)
	case KindDiff:
		return normalizeDiff(raw, validateFilePath)
	case KindMermaid:
		var card MermaidCard
		if err := decodeStrictComponent(raw, &card); err != nil {
			return nil, fmt.Errorf("cards: invalid mermaid component: %w", err)
		}
		if err := validateSchemaVersion(card.SchemaVersion); err != nil {
			return nil, err
		}
		if card.Source == "" {
			return nil, fmt.Errorf("cards: mermaid source is required")
		}
		return MarshalComponent(card)
	case KindHTMLPreview:
		var card HTMLPreviewCard
		if err := decodeStrictComponent(raw, &card); err != nil {
			return nil, fmt.Errorf("cards: invalid html-preview component: %w", err)
		}
		if err := validateSchemaVersion(card.SchemaVersion); err != nil {
			return nil, err
		}
		if err := validateManagedPath("html-preview path", card.Path, validateFilePath); err != nil {
			return nil, err
		}
		return MarshalComponent(card)
	case KindImage:
		if _, versioned := raw["schema_version"]; !versioned {
			return normalizeLegacyRemoteImage(raw)
		}
		var card ManagedImageCard
		if err := decodeStrictComponent(raw, &card); err != nil {
			return nil, fmt.Errorf("cards: invalid image component: %w", err)
		}
		if err := validateSchemaVersion(card.SchemaVersion); err != nil {
			return nil, err
		}
		if err := validateManagedPath("image path", card.Path, validateFilePath); err != nil {
			return nil, err
		}
		return MarshalComponent(card)
	case KindPlan:
		var card PlanCard
		if err := decodeComponent(raw, &card); err != nil {
			return nil, fmt.Errorf("cards: invalid plan component: %w", err)
		}
		return MarshalComponent(card)
	case KindForm:
		var form FormSchema
		if err := decodeComponent(raw, &form); err != nil {
			return nil, fmt.Errorf("cards: invalid form component: %w", err)
		}
		return MarshalComponent(form)
	case KindChart, KindCustom, KindDecision, KindIGVCommand:
		return cloneComponentMap(raw)
	default:
		return nil, fmt.Errorf("cards: unknown card kind %q", kind)
	}
}

func normalizeTable(raw map[string]any, validateFilePath func(string) error) (map[string]any, error) {
	if _, versioned := raw["schema_version"]; !versioned {
		return normalizeLegacyTable(raw)
	}
	var card RichTableCard
	if err := decodeStrictComponent(raw, &card); err != nil {
		return nil, fmt.Errorf("cards: invalid table component: %w", err)
	}
	if err := validateSchemaVersion(card.SchemaVersion); err != nil {
		return nil, err
	}
	if err := validateGrid(card.Columns, card.Rows, card.Source, validateFilePath); err != nil {
		return nil, fmt.Errorf("cards: invalid table component: %w", err)
	}
	if err := validateRichPayload(card); err != nil {
		return nil, err
	}
	return MarshalComponent(card)
}

func normalizeSpreadsheet(raw map[string]any, validateFilePath func(string) error) (map[string]any, error) {
	var card SpreadsheetCard
	if err := decodeStrictComponent(raw, &card); err != nil {
		return nil, fmt.Errorf("cards: invalid spreadsheet component: %w", err)
	}
	if err := validateSchemaVersion(card.SchemaVersion); err != nil {
		return nil, err
	}
	if err := validateGrid(card.Columns, card.Rows, card.Source, validateFilePath); err != nil {
		return nil, fmt.Errorf("cards: invalid spreadsheet component: %w", err)
	}
	if err := validateRichPayload(card); err != nil {
		return nil, err
	}
	return MarshalComponent(card)
}

func normalizeLegacyTable(raw map[string]any) (map[string]any, error) {
	var legacy struct {
		Title   string   `json:"title,omitempty"`
		Columns []Column `json:"columns"`
		Rows    []any    `json:"rows"`
	}
	if err := decodeComponent(raw, &legacy); err != nil {
		return nil, fmt.Errorf("cards: invalid legacy table component: %w", err)
	}
	if len(legacy.Columns) == 0 {
		return nil, fmt.Errorf("cards: legacy table columns are required")
	}
	columns := make([]RichColumn, len(legacy.Columns))
	for i, column := range legacy.Columns {
		if column.Name == "" {
			return nil, fmt.Errorf("cards: legacy table column %d missing name", i)
		}
		columnType := column.Type
		if columnType == "" || columnType == "string" {
			columnType = "text"
		}
		columns[i] = RichColumn{Key: column.Name, Label: column.Name, Type: columnType}
	}
	if legacy.Rows == nil {
		return nil, fmt.Errorf("cards: legacy table rows are required")
	}
	card := RichTableCard{SchemaVersion: schemaVersionOne, Title: legacy.Title, Columns: columns, Rows: &legacy.Rows}
	if err := validateGrid(card.Columns, card.Rows, nil, nil); err != nil {
		return nil, fmt.Errorf("cards: invalid legacy table component: %w", err)
	}
	if err := validateRichPayload(card); err != nil {
		return nil, err
	}
	return MarshalComponent(card)
}

func normalizeDiff(raw map[string]any, validateFilePath func(string) error) (map[string]any, error) {
	if _, versioned := raw["schema_version"]; versioned {
		var card DiffCard
		if err := decodeStrictComponent(raw, &card); err != nil {
			return nil, fmt.Errorf("cards: invalid diff component: %w", err)
		}
		if err := validateSchemaVersion(card.SchemaVersion); err != nil {
			return nil, err
		}
		if err := validateInlineOrSource(card.UnifiedDiff, card.Source, validateFilePath); err != nil {
			return nil, fmt.Errorf("cards: invalid diff component: %w", err)
		}
		return MarshalComponent(card)
	}

	aliases := []string{"diff", "unified", "unified_diff"}
	var inline string
	count := 0
	for _, alias := range aliases {
		if value, exists := raw[alias]; exists {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("cards: legacy diff %s must be a string", alias)
			}
			inline = text
			count++
		}
	}
	if count != 1 {
		return nil, fmt.Errorf("cards: legacy diff requires only one of diff, unified, unified_diff")
	}
	var legacy struct {
		Title string   `json:"title,omitempty"`
		Files []string `json:"files,omitempty"`
	}
	if err := decodeComponent(raw, &legacy); err != nil {
		return nil, fmt.Errorf("cards: invalid legacy diff component: %w", err)
	}
	card := DiffCard{SchemaVersion: schemaVersionOne, Title: legacy.Title, Files: legacy.Files, UnifiedDiff: inline}
	return MarshalComponent(card)
}

func normalizeLegacyRemoteImage(raw map[string]any) (map[string]any, error) {
	if _, exists := raw["path"]; exists {
		return nil, fmt.Errorf("cards: image path requires schema_version")
	}
	url, ok := raw["url"].(string)
	if !ok || url == "" {
		return nil, fmt.Errorf("cards: legacy image requires a remote url")
	}
	if alt, exists := raw["alt"]; exists {
		if _, ok := alt.(string); !ok {
			return nil, fmt.Errorf("cards: legacy image alt must be a string")
		}
	}
	for key := range raw {
		if key != "url" && key != "alt" {
			return nil, fmt.Errorf("cards: legacy image has unsupported field %q", key)
		}
	}
	return cloneComponentMap(raw)
}

func validateGrid(columns []RichColumn, rows *[]any, source *SourceRef, validateFilePath func(string) error) error {
	if len(columns) == 0 {
		return fmt.Errorf("columns are required")
	}
	if len(columns) > maxColumns {
		return fmt.Errorf("columns may contain at most %d columns", maxColumns)
	}
	keys := make(map[string]struct{}, len(columns))
	for index, column := range columns {
		if column.Key == "" || column.Label == "" || column.Type == "" {
			return fmt.Errorf("column %d requires key, label, and type", index)
		}
		if !isRichColumnType(column.Type) {
			return fmt.Errorf("column %d has unsupported type %q", index, column.Type)
		}
		if column.Align != "" && column.Align != "left" && column.Align != "center" && column.Align != "right" {
			return fmt.Errorf("column %d has unsupported align %q", index, column.Align)
		}
		if err := validateRichColumnFormat(column.Format); err != nil {
			return fmt.Errorf("column %d format: %w", index, err)
		}
		if _, found := keys[column.Key]; found {
			return fmt.Errorf("duplicate column key %q", column.Key)
		}
		keys[column.Key] = struct{}{}
	}
	if (rows == nil) == (source == nil) {
		return fmt.Errorf("exactly one of rows or source is required")
	}
	if source != nil {
		if source.Format != "json" && source.Format != "csv" && source.Format != "tsv" {
			return fmt.Errorf("source format must be json, csv, or tsv")
		}
		return validateManagedPath("source path", source.Path, validateFilePath)
	}
	if len(*rows) > maxInlineRows {
		return fmt.Errorf("rows may contain at most %d rows", maxInlineRows)
	}
	for rowIndex, row := range *rows {
		switch value := row.(type) {
		case []any:
			if len(value) != len(columns) {
				return fmt.Errorf("row %d must contain exactly %d columns", rowIndex, len(columns))
			}
			for columnIndex, cell := range value {
				if err := validateCell(cell); err != nil {
					return fmt.Errorf("row %d column %d: %w", rowIndex, columnIndex, err)
				}
			}
		case map[string]any:
			if len(value) != len(columns) {
				return fmt.Errorf("row %d has an extra or missing column", rowIndex)
			}
			for _, column := range columns {
				cell, found := value[column.Key]
				if !found {
					return fmt.Errorf("row %d missing column %q", rowIndex, column.Key)
				}
				if err := validateCell(cell); err != nil {
					return fmt.Errorf("row %d column %q: %w", rowIndex, column.Key, err)
				}
			}
			for key := range value {
				if _, found := keys[key]; !found {
					return fmt.Errorf("row %d has extra column %q", rowIndex, key)
				}
			}
		default:
			return fmt.Errorf("row %d must be an array or object", rowIndex)
		}
	}
	return nil
}

func validateInlineOrSource(inline string, source *SourceRef, validateFilePath func(string) error) error {
	if (inline == "") == (source == nil) {
		return fmt.Errorf("exactly one of unified_diff or source is required")
	}
	if source == nil {
		return nil
	}
	if source.Format != "text" {
		return fmt.Errorf("source format must be text")
	}
	return validateManagedPath("source path", source.Path, validateFilePath)
}

func isRichColumnType(value string) bool {
	switch value {
	case "text", "number", "currency", "percent", "boolean", "date", "badge", "bool":
		return true
	default:
		return false
	}
}

func validateRichColumnFormat(format *RichColumnFormat) error {
	if format == nil {
		return nil
	}
	if format.Locale != nil && *format.Locale == "" {
		return fmt.Errorf("locale must not be empty")
	}
	if format.Currency != nil && *format.Currency == "" {
		return fmt.Errorf("currency must not be empty")
	}
	if format.MinimumFractionDigits != nil && *format.MinimumFractionDigits < 0 {
		return fmt.Errorf("minimum fraction digits must be non-negative")
	}
	if format.MaximumFractionDigits != nil && *format.MaximumFractionDigits < 0 {
		return fmt.Errorf("maximum fraction digits must be non-negative")
	}
	return nil
}

func validateCell(value any) error {
	switch cell := value.(type) {
	case nil, bool, float64, string:
		if text, ok := cell.(string); ok && len(text) > maxPayloadBytes {
			return fmt.Errorf("string cell exceeds 256KB")
		}
		return nil
	default:
		return fmt.Errorf("cell must be a primitive")
	}
}

func validateManagedPath(name, value string, validateFilePath func(string) error) error {
	if value == "" || value != strings.TrimSpace(value) || strings.Contains(value, "\\") || hasWindowsVolume(value) || path.IsAbs(value) || path.Clean(value) != value || value == ".." || strings.HasPrefix(value, "../") {
		return fmt.Errorf("cards: %s is invalid", name)
	}
	if validateFilePath != nil {
		if err := validateFilePath(value); err != nil {
			return fmt.Errorf("cards: invalid %s: %w", name, err)
		}
	}
	return nil
}

func hasWindowsVolume(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':'
}

func validateSchemaVersion(value int) error {
	if value != schemaVersionOne {
		return fmt.Errorf("cards: schema_version must be %d", schemaVersionOne)
	}
	return nil
}

func validateRichPayload(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("cards: marshal component: %w", err)
	}
	if len(raw) > maxPayloadBytes {
		return fmt.Errorf("cards: component payload exceeds 256KB")
	}
	return nil
}

// MarshalComponent converts a Component into the map[string]any form used in
// Custom event payloads.
func MarshalComponent(c Component) (map[string]any, error) {
	if c == nil {
		return nil, fmt.Errorf("cards: nil component")
	}
	if err := Default.ValidateKind(c.CardKind()); err != nil {
		return nil, err
	}
	if fs, ok := c.(FormSchema); ok {
		if err := Default.ValidateForm(fs); err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("cards: marshal component: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("cards: remarshal component: %w", err)
	}
	return out, nil
}

func decodeComponent(raw map[string]any, target any) error {
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func decodeStrictComponent(raw map[string]any, target any) error {
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func cloneComponentMap(raw map[string]any) (map[string]any, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("cards: marshal component: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("cards: remarshal component: %w", err)
	}
	return out, nil
}
