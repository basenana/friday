package cards

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestRichCardCatalog(t *testing.T) {
	largeCell := strings.Repeat("x", 256*1024)
	rows201 := make([]any, 201)
	for i := range rows201 {
		rows201[i] = map[string]any{"gene": "BRCA1", "score": float64(i)}
	}
	columns257 := make([]any, 257)
	for i := range columns257 {
		columns257[i] = map[string]any{"key": "c" + string(rune('a'+i%26)) + string(rune('A'+i/26)), "label": "Column", "type": "text"}
	}

	tests := []struct {
		name    string
		kind    Kind
		fixture func() map[string]any
		wantErr string
	}{
		{"mermaid", Kind("mermaid"), func() map[string]any {
			return map[string]any{"schema_version": float64(1), "source": "graph TD; A-->B"}
		}, ""},
		{"html preview", Kind("html-preview"), func() map[string]any {
			return map[string]any{"schema_version": float64(1), "path": "reports/result.html"}
		}, ""},
		{"spreadsheet source", Kind("spreadsheet"), validSourceSheet, ""},
		{"managed image", KindImage, func() map[string]any {
			return map[string]any{"schema_version": float64(1), "path": "plots/a.png", "alt": "Plot"}
		}, ""},
		{"versioned source diff", KindDiff, validSourceDiff, ""},
		{"legacy diff", KindDiff, func() map[string]any { return map[string]any{"diff": "--- a\n+++ b"} }, ""},
		{"unsupported version", Kind("mermaid"), func() map[string]any { return map[string]any{"schema_version": float64(2), "source": "graph TD"} }, "schema_version"},
		{"mermaid unknown field", Kind("mermaid"), func() map[string]any {
			return map[string]any{"schema_version": float64(1), "source": "graph TD", "onclick": "alert(1)"}
		}, "unknown field"},
		{"duplicate table columns", KindTable, func() map[string]any {
			v := validTable()
			v["columns"] = []any{map[string]any{"key": "gene", "label": "Gene", "type": "text"}, map[string]any{"key": "gene", "label": "Again", "type": "text"}}
			return v
		}, "duplicate"},
		{"table unknown column type", KindTable, func() map[string]any {
			v := validTable()
			v["columns"].([]any)[0].(map[string]any)["type"] = "formula"
			return v
		}, "type"},
		{"spreadsheet invalid column alignment", KindSpreadsheet, func() map[string]any {
			v := validTable()
			v["columns"].([]any)[0].(map[string]any)["align"] = "diagonal"
			return v
		}, "align"},
		{"table unknown column format field", KindTable, func() map[string]any {
			v := validTable()
			v["columns"].([]any)[0].(map[string]any)["format"] = map[string]any{"unexpected": true}
			return v
		}, "unknown field"},
		{"spreadsheet invalid fraction digits", KindSpreadsheet, func() map[string]any {
			v := validTable()
			v["columns"].([]any)[0].(map[string]any)["format"] = map[string]any{"minimum_fraction_digits": float64(-1)}
			return v
		}, "fraction"},
		{"table invalid source format", KindTable, func() map[string]any {
			v := validSourceSheet()
			v["source"].(map[string]any)["format"] = "xml"
			return v
		}, "format"},
		{"diff invalid source format", KindDiff, func() map[string]any {
			v := validSourceDiff()
			v["source"].(map[string]any)["format"] = "csv"
			return v
		}, "format"},
		{"nested table cell", KindTable, func() map[string]any {
			v := validTable()
			v["rows"] = []any{map[string]any{"gene": []any{"BRCA1"}, "score": float64(1)}}
			return v
		}, "primitive"},
		{"table missing rows and source", KindTable, func() map[string]any { v := validTable(); delete(v, "rows"); return v }, "exactly one"},
		{"table has rows and source", KindTable, func() map[string]any {
			v := validTable()
			v["source"] = map[string]any{"path": "results/data.csv", "format": "csv"}
			return v
		}, "exactly one"},
		{"table too many rows", KindTable, func() map[string]any { v := validTable(); v["rows"] = rows201; return v }, "200"},
		{"table too many columns", KindTable, func() map[string]any { v := validTable(); v["columns"] = columns257; return v }, "256"},
		{"table cell too large", KindTable, func() map[string]any {
			v := validTable()
			v["rows"] = []any{map[string]any{"gene": largeCell, "score": float64(1)}}
			return v
		}, "256KB"},
		{"spreadsheet missing data", Kind("spreadsheet"), func() map[string]any { v := validSourceSheet(); delete(v, "source"); return v }, "exactly one"},
		{"spreadsheet both data", Kind("spreadsheet"), func() map[string]any {
			v := validTable()
			v["source"] = map[string]any{"path": "results/data.csv", "format": "csv"}
			return v
		}, "exactly one"},
		{"spreadsheet too many rows", Kind("spreadsheet"), func() map[string]any { v := validTable(); v["rows"] = rows201; return v }, "200"},
		{"spreadsheet too many columns", Kind("spreadsheet"), func() map[string]any { v := validTable(); v["columns"] = columns257; return v }, "256"},
		{"spreadsheet cell too large", Kind("spreadsheet"), func() map[string]any {
			v := validTable()
			v["rows"] = []any{map[string]any{"gene": largeCell, "score": float64(1)}}
			return v
		}, "256KB"},
		{"short array row", KindTable, func() map[string]any { v := validTable(); v["rows"] = []any{[]any{"BRCA1"}}; return v }, "column"},
		{"object row extra key", KindTable, func() map[string]any {
			v := validTable()
			v["rows"] = []any{map[string]any{"gene": "BRCA1", "score": float64(1), "extra": true}}
			return v
		}, "extra"},
		{"diff missing data", KindDiff, func() map[string]any { return map[string]any{"schema_version": float64(1)} }, "exactly one"},
		{"diff both inline and source", KindDiff, func() map[string]any { v := validSourceDiff(); v["unified_diff"] = "--- a\n+++ b"; return v }, "exactly one"},
		{"table traversal path", KindTable, func() map[string]any {
			v := validSourceSheet()
			v["source"] = map[string]any{"path": "../data.csv", "format": "csv"}
			return v
		}, "path"},
		{"spreadsheet absolute path", Kind("spreadsheet"), func() map[string]any {
			v := validSourceSheet()
			v["source"] = map[string]any{"path": "/data.csv", "format": "csv"}
			return v
		}, "path"},
		{"html traversal path", Kind("html-preview"), func() map[string]any { return map[string]any{"schema_version": float64(1), "path": "../result.html"} }, "path"},
		{"image absolute path", KindImage, func() map[string]any {
			return map[string]any{"schema_version": float64(1), "path": "/plots/a.png", "alt": "Plot"}
		}, "path"},
		{"diff traversal path", KindDiff, func() map[string]any {
			v := validSourceDiff()
			v["source"] = map[string]any{"path": "../result.diff", "format": "text"}
			return v
		}, "path"},
		{"legacy table", KindTable, func() map[string]any {
			return map[string]any{"columns": []any{map[string]any{"name": "Gene", "type": "string"}}, "rows": []any{[]any{"BRCA1"}}}
		}, ""},
		{"legacy remote image", KindImage, func() map[string]any { return map[string]any{"url": "https://example.test/plot.png", "alt": "Plot"} }, ""},
		{"legacy diff aliases conflict", KindDiff, func() map[string]any { return map[string]any{"diff": "a", "unified": "b"} }, "only one"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := tt.fixture()
			before := cloneTestMap(t, fixture)
			_, err := Default.NormalizeComponent(tt.kind, fixture, workdirPathValidator)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("NormalizeComponent() error = %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NormalizeComponent() error = %v, want substring %q", err, tt.wantErr)
			}
			if !reflect.DeepEqual(fixture, before) {
				t.Fatalf("NormalizeComponent mutated fixture:\n got %#v\nwant %#v", fixture, before)
			}
		})
	}
}

func TestRichOutputDocumentationExamplesNormalize(t *testing.T) {
	tests := []struct {
		name string
		kind Kind
		body map[string]any
	}{
		{"mermaid", KindMermaid, map[string]any{
			"schema_version": float64(1),
			"source":         "flowchart LR\n  A --> B",
		}},
		{"html-preview", KindHTMLPreview, map[string]any{
			"schema_version": float64(1),
			"path":           "reports/result.html",
		}},
		{"table inline", KindTable, map[string]any{
			"schema_version": float64(1),
			"columns": []any{
				map[string]any{"key": "gene", "label": "Gene", "type": "text"},
			},
			"rows": []any{map[string]any{"gene": "BRCA1"}},
		}},
		{"table source", KindTable, map[string]any{
			"schema_version": float64(1),
			"columns": []any{
				map[string]any{"key": "gene", "label": "Gene", "type": "text"},
			},
			"source": map[string]any{"path": "results/genes.json", "format": "json"},
		}},
		{"spreadsheet inline", KindSpreadsheet, map[string]any{
			"schema_version": float64(1),
			"columns": []any{
				map[string]any{"key": "amount", "label": "Amount", "type": "number"},
			},
			"rows": []any{map[string]any{"amount": float64(42)}},
		}},
		{"spreadsheet source", KindSpreadsheet, map[string]any{
			"schema_version": float64(1),
			"columns": []any{
				map[string]any{"key": "amount", "label": "Amount", "type": "number"},
			},
			"source": map[string]any{"path": "results/data.tsv", "format": "tsv"},
		}},
		{"managed image", KindImage, map[string]any{
			"schema_version": float64(1),
			"path":           "plots/result.png",
			"alt":            "Result plot",
		}},
		{"diff inline", KindDiff, map[string]any{
			"schema_version": float64(1),
			"unified_diff":   "--- a/app.go\n+++ b/app.go\n@@ -1 +1 @@\n-old\n+new",
		}},
		{"diff source", KindDiff, map[string]any{
			"schema_version": float64(1),
			"source":         map[string]any{"path": "changes/result.diff", "format": "text"},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Default.NormalizeComponent(tt.kind, tt.body, workdirPathValidator); err != nil {
				t.Fatalf("documented %s example is invalid: %v", tt.kind, err)
			}
		})
	}
}

func TestRichCardPathValidationUsesOriginalRelativePath(t *testing.T) {
	calls := []string{}
	validator := func(value string) error {
		calls = append(calls, value)
		if value != "results/data.csv" {
			t.Fatalf("validator path = %q, want results/data.csv", value)
		}
		return nil
	}
	if _, err := Default.NormalizeComponent(KindTable, validSourceSheet(), validator); err != nil {
		t.Fatalf("NormalizeComponent() error = %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"results/data.csv"}) {
		t.Fatalf("validator calls = %#v", calls)
	}
	if _, err := Default.NormalizeComponent(KindTable, map[string]any{
		"schema_version": float64(1),
		"columns":        validTable()["columns"],
		"source":         map[string]any{"path": "../data.csv", "format": "csv"},
	}, validator); err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("unsafe path error = %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("validator called for unsafe path: %#v", calls)
	}
}

func TestRichCardsPreserveEmptyRows(t *testing.T) {
	for _, kind := range []Kind{KindTable, KindSpreadsheet} {
		t.Run(string(kind), func(t *testing.T) {
			body := validTable()
			body["rows"] = []any{}
			got, err := Default.NormalizeComponent(kind, body, workdirPathValidator)
			if err != nil {
				t.Fatalf("NormalizeComponent() error = %v", err)
			}
			rows, exists := got["rows"]
			if !exists || !reflect.DeepEqual(rows, []any{}) {
				t.Fatalf("normalized rows = %#v, want empty array", rows)
			}
			if _, exists := got["source"]; exists {
				t.Fatalf("normalized empty rows unexpectedly has source")
			}
		})
	}
	for _, kind := range []Kind{KindTable, KindSpreadsheet} {
		t.Run(string(kind)+" source", func(t *testing.T) {
			got, err := Default.NormalizeComponent(kind, validSourceSheet(), workdirPathValidator)
			if err != nil {
				t.Fatalf("NormalizeComponent() error = %v", err)
			}
			if _, exists := got["rows"]; exists {
				t.Fatalf("source normalized with rows = %#v", got["rows"])
			}
		})
	}
}

func TestManagedPathRejectsNonPortableSeparators(t *testing.T) {
	for _, value := range []string{"..\\secret", "dir\\..\\secret", "C:\\secret", "C:/secret"} {
		body := validSourceSheet()
		body["source"] = map[string]any{"path": value, "format": "csv"}
		if _, err := Default.NormalizeComponent(KindTable, body, workdirPathValidator); err == nil || !strings.Contains(err.Error(), "path") {
			t.Fatalf("NormalizeComponent(%q) error = %v", value, err)
		}
	}
}

func TestManagedPathRejectsSurroundingWhitespace(t *testing.T) {
	tests := []struct {
		name string
		kind Kind
		body func(string) map[string]any
	}{
		{"html preview", KindHTMLPreview, func(value string) map[string]any {
			return map[string]any{"schema_version": float64(1), "path": value}
		}},
		{"image", KindImage, func(value string) map[string]any {
			return map[string]any{"schema_version": float64(1), "path": value}
		}},
		{"table source", KindTable, func(value string) map[string]any {
			body := validSourceSheet()
			body["source"].(map[string]any)["path"] = value
			return body
		}},
		{"spreadsheet source", KindSpreadsheet, func(value string) map[string]any {
			body := validSourceSheet()
			body["source"].(map[string]any)["path"] = value
			return body
		}},
		{"diff source", KindDiff, func(value string) map[string]any {
			body := validSourceDiff()
			body["source"].(map[string]any)["path"] = value
			return body
		}},
	}

	for _, tt := range tests {
		for _, value := range []string{" results/data.csv", "results/data.csv "} {
			t.Run(tt.name+"/"+fmt.Sprintf("%q", value), func(t *testing.T) {
				if _, err := Default.NormalizeComponent(tt.kind, tt.body(value), workdirPathValidator); err == nil || !strings.Contains(err.Error(), "path") {
					t.Fatalf("NormalizeComponent(%q) error = %v, want path validation error", value, err)
				}
			})
		}
	}
}

func TestManagedImageWireAlwaysIncludesStringAlt(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{"missing", map[string]any{"schema_version": float64(1), "path": "plots/result.png"}},
		{"empty", map[string]any{"schema_version": float64(1), "path": "plots/result.png", "alt": ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Default.NormalizeComponent(KindImage, tt.body, workdirPathValidator)
			if err != nil {
				t.Fatalf("NormalizeComponent() error = %v", err)
			}
			raw, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			var wire struct {
				Alt *string `json:"alt"`
			}
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if wire.Alt == nil || *wire.Alt != "" {
				t.Fatalf("wire alt = %#v, want string field with empty value; JSON = %s", wire.Alt, raw)
			}
		})
	}
}

func TestLegacyTableGoAPIAndNativeValues(t *testing.T) {
	positional := TableCard{"Legacy", []Column{{Name: "Gene"}}, [][]any{{"BRCA1"}}}
	if _, err := MarshalComponent(positional); err != nil {
		t.Fatalf("MarshalComponent(positional TableCard) error = %v", err)
	}
	legacy := TableCard{Columns: []Column{{Name: "Gene"}}, Rows: [][]any{{"BRCA1"}}}
	marshaled, err := MarshalComponent(legacy)
	if err != nil {
		t.Fatalf("MarshalComponent(TableCard) error = %v", err)
	}
	if _, exists := marshaled["schema_version"]; exists {
		t.Fatalf("legacy TableCard unexpectedly emitted schema_version")
	}

	normalized, err := Default.NormalizeComponent(KindTable, map[string]any{
		"columns": []Column{{Name: "Gene"}},
		"rows":    [][]any{{"BRCA1"}},
	}, workdirPathValidator)
	if err != nil {
		t.Fatalf("native legacy table error = %v", err)
	}
	if normalized["columns"].([]any)[0].(map[string]any)["key"] != "Gene" {
		t.Fatalf("native legacy table normalized columns = %#v", normalized["columns"])
	}
	if _, err := Default.NormalizeComponent(KindTable, map[string]any{
		"columns": []Column{{Name: "Gene"}},
		"rows":    []map[string]any{{"Gene": "BRCA1"}},
	}, workdirPathValidator); err != nil {
		t.Fatalf("native object-row legacy table error = %v", err)
	}

	diff, err := Default.NormalizeComponent(KindDiff, map[string]any{
		"diff":  "--- a\n+++ b",
		"files": []string{"a.txt", "b.txt"},
	}, workdirPathValidator)
	if err != nil {
		t.Fatalf("native legacy diff files error = %v", err)
	}
	if !reflect.DeepEqual(diff["files"], []any{"a.txt", "b.txt"}) {
		t.Fatalf("legacy diff files = %#v", diff["files"])
	}
}

func TestLegacyCardKindsPassThrough(t *testing.T) {
	tests := []struct {
		kind Kind
		body map[string]any
	}{
		{KindDecision, map[string]any{"prompt": "Proceed?", "options": []any{"yes", "no"}}},
		{KindIGVCommand, map[string]any{"actions": []any{map[string]any{"type": "goto", "locus": "chr1:1-10"}}}},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			got, err := Default.NormalizeComponent(tt.kind, tt.body, workdirPathValidator)
			if err != nil {
				t.Fatalf("NormalizeComponent() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.body) {
				t.Fatalf("NormalizeComponent() = %#v, want %#v", got, tt.body)
			}
		})
	}
}

func TestLegacyCardCompatibility(t *testing.T) {
	largeTitle := strings.Repeat("x", 256*1024)
	file, err := Default.NormalizeComponent(KindFile, map[string]any{"path": "/sandbox/report.txt", "title": largeTitle}, nil)
	if err != nil {
		t.Fatalf("large legacy file error = %v", err)
	}
	if file["title"] != largeTitle {
		t.Fatalf("large legacy file title was not preserved")
	}

	table, err := Default.NormalizeComponent(KindTable, map[string]any{
		"columns": []any{map[string]any{"name": "Gene"}},
		"rows":    []any{[]any{"BRCA1"}},
	}, workdirPathValidator)
	if err != nil {
		t.Fatalf("legacy table without type error = %v", err)
	}
	columns := table["columns"].([]any)
	if columns[0].(map[string]any)["type"] != "text" {
		t.Fatalf("legacy table default type = %#v, want text", columns[0])
	}

	wantDiff := map[string]any{"schema_version": float64(1), "unified_diff": "--- a\n+++ b"}
	for _, alias := range []string{"diff", "unified", "unified_diff"} {
		t.Run("diff alias "+alias, func(t *testing.T) {
			got, err := Default.NormalizeComponent(KindDiff, map[string]any{alias: "--- a\n+++ b"}, workdirPathValidator)
			if err != nil {
				t.Fatalf("NormalizeComponent() error = %v", err)
			}
			if !reflect.DeepEqual(got, wantDiff) {
				t.Fatalf("NormalizeComponent() = %#v, want %#v", got, wantDiff)
			}
		})
	}

	for _, value := range []map[string]any{
		{"path": "../plots/a.png", "alt": "Plot"},
		{"path": "/plots/a.png", "alt": "Plot"},
	} {
		if _, err := Default.NormalizeComponent(KindImage, value, workdirPathValidator); err == nil || !strings.Contains(err.Error(), "path") {
			t.Fatalf("legacy managed image %#v error = %v", value, err)
		}
	}
	remote := map[string]any{"url": "https://example.test/plot.png", "alt": "Plot"}
	got, err := Default.NormalizeComponent(KindImage, remote, workdirPathValidator)
	if err != nil {
		t.Fatalf("legacy remote image error = %v", err)
	}
	if !reflect.DeepEqual(got, remote) {
		t.Fatalf("legacy remote image = %#v, want %#v", got, remote)
	}
}

func TestTablePayloadAggregateLimit(t *testing.T) {
	tests := []struct {
		name string
		kind Kind
		body map[string]any
	}{
		{"legacy table", KindTable, oversizedAggregateLegacyTable()},
		{"versioned table", KindTable, oversizedAggregateRichTable()},
		{"spreadsheet", KindSpreadsheet, oversizedAggregateRichTable()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Default.NormalizeComponent(tt.kind, tt.body, workdirPathValidator); err == nil || !strings.Contains(err.Error(), "256KB") {
				t.Fatalf("NormalizeComponent() error = %v, want 256KB aggregate limit", err)
			}
		})
	}
}

func oversizedAggregateLegacyTable() map[string]any {
	columns := make([]any, 256)
	for index := range columns {
		columns[index] = map[string]any{"name": fmt.Sprintf("column_%d", index)}
	}
	return map[string]any{"columns": columns, "rows": oversizedAggregateRows()}
}

func oversizedAggregateRichTable() map[string]any {
	columns := make([]any, 256)
	for index := range columns {
		key := fmt.Sprintf("column_%d", index)
		columns[index] = map[string]any{"key": key, "label": key, "type": "text"}
	}
	return map[string]any{"schema_version": float64(1), "columns": columns, "rows": oversizedAggregateRows()}
}

func oversizedAggregateRows() []any {
	rows := make([]any, 200)
	for rowIndex := range rows {
		row := make([]any, 256)
		for columnIndex := range row {
			row[columnIndex] = "value"
		}
		rows[rowIndex] = row
	}
	return rows
}

func workdirPathValidator(value string) error {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return fmt.Errorf("path must be workdir-relative")
	}
	return nil
}

func validTable() map[string]any {
	return map[string]any{
		"schema_version": float64(1),
		"columns": []any{
			map[string]any{"key": "gene", "label": "Gene", "type": "text"},
			map[string]any{"key": "score", "label": "Score", "type": "number"},
		},
		"rows": []any{map[string]any{"gene": "BRCA1", "score": float64(1.2)}},
	}
}

func validSourceSheet() map[string]any {
	value := validTable()
	value["source"] = map[string]any{"path": "results/data.csv", "format": "csv"}
	delete(value, "rows")
	return value
}

func validSourceDiff() map[string]any {
	return map[string]any{"schema_version": float64(1), "source": map[string]any{"path": "changes/result.diff", "format": "text"}}
}

func cloneTestMap(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
