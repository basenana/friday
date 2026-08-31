package actor

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/basenana/friday/core/actor/cards"
)

func TestEmitCardRichOutputDescription(t *testing.T) {
	tool := makeEmitCardTool(&Actor{})
	description := tool.GetDescription()

	enum, ok := tool.InputSchema.Properties["kind"].(map[string]interface{})["enum"].([]string)
	if !ok {
		t.Fatalf("emit_card kind enum has unexpected type: %#v", tool.InputSchema.Properties["kind"])
	}
	for _, kind := range []string{"mermaid", "html-preview", "table", "spreadsheet", "image", "diff"} {
		if !slices.Contains(enum, kind) {
			t.Errorf("emit_card kind enum missing %q: %#v", kind, enum)
		}
		if !strings.Contains(description, "EXAMPLE "+kind+" ") {
			t.Errorf("emit_card description missing %q example", kind)
		}
	}

	for _, want := range []string{
		"Exactly one of `rows` or `source`",
		"Exactly one of `unified_diff` or `source`",
		"up to 64 KiB",
		"write the unified diff with `workdir_write_file` before emitting the card",
		"first write the file or use inline `unified_diff`",
		"JSON, CSV, or TSV",
		"workdir-relative",
		"more than 20 rows",
		"Protocol hard ceilings",
		"200 inline rows",
		"256 KiB",
		"compatibility-only",
		"not preferred output",
		"Column type must be exactly one of: text, number, currency, percent, boolean, date, badge.",
		"Column align must be exactly one of: left, center, right.",
		"Column format may contain only: locale, currency, minimum_fraction_digits, maximum_fraction_digits.",
		"Data source format must be exactly one of: json, csv, tsv; diff source format must be text.",
		"Never choose sandbox flags, HTML trust, executable callbacks, or remote privileges",
	} {
		if !strings.Contains(description, want) {
			t.Errorf("emit_card description missing %q", want)
		}
	}
}

func TestEmitCardDescriptionDoesNotOverpromiseRuntimeRejection(t *testing.T) {
	description := makeEmitCardTool(&Actor{}).GetDescription()
	lowerDescription := strings.ToLower(description)
	for _, overpromise := range []string{
		"unsupported or misspelled fields are rejected",
		"unsupported or misspelled fields cause a tool error",
		"unsupported fields are rejected",
	} {
		if strings.Contains(lowerDescription, overpromise) {
			t.Errorf("emit_card description overpromises runtime validation with %q", overpromise)
		}
	}
	if !strings.Contains(description, "Other fields and values are unsupported; they may be rejected or fail to render.") {
		t.Error("emit_card description must accurately qualify unsupported fields and values")
	}
}

func TestEmitCardDescriptionExamplesValidateAgainstCurrentCatalog(t *testing.T) {
	description := makeEmitCardTool(&Actor{}).GetDescription()
	for _, example := range []struct {
		name string
		kind cards.Kind
	}{
		{"mermaid", cards.KindMermaid},
		{"html-preview", cards.KindHTMLPreview},
		{"table", cards.KindTable},
		{"table-source", cards.KindTable},
		{"spreadsheet", cards.KindSpreadsheet},
		{"spreadsheet-source", cards.KindSpreadsheet},
		{"image", cards.KindImage},
		{"diff", cards.KindDiff},
		{"diff-source", cards.KindDiff},
	} {
		raw := emitCardDescriptionExample(t, description, example.name)
		var component map[string]any
		if err := json.Unmarshal([]byte(raw), &component); err != nil {
			t.Fatalf("%s example is not JSON: %v\n%s", example.name, err, raw)
		}
		if _, err := cards.Default.NormalizeComponent(example.kind, component, descriptionTestPathValidator); err != nil {
			t.Fatalf("%s example does not satisfy the card catalog: %v", example.name, err)
		}
	}
}

func emitCardDescriptionExample(t *testing.T, description, name string) string {
	t.Helper()
	prefix := "EXAMPLE " + name + " "
	for _, line := range strings.Split(description, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	t.Fatalf("missing %s example", name)
	return ""
}

func descriptionTestPathValidator(path string) error {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return &invalidDescriptionPathError{path: path}
	}
	return nil
}

type invalidDescriptionPathError struct {
	path string
}

func (e *invalidDescriptionPathError) Error() string {
	return "invalid description path: " + e.path
}
