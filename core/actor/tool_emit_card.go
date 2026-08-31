package actor

import (
	"context"
	"encoding/json"
	"fmt"

	coretools "github.com/basenana/friday/core/tools"
)

const emitCardDescription = `Emit one declarative A2UI card to the user. Use request_form instead when user input must block the turn.

Card kind must be one of: file, table, spreadsheet, diff, mermaid, html-preview,
form, plan, image, chart, decision, igv-command, custom.

For new rich output, component must use schema_version 1. Use only documented
snake_case fields and the exact documented enum/options. Component fields are:
Mermaid schema_version, source; HTML preview schema_version, path; table/spreadsheet
schema_version, title, columns and exactly one of rows, source; image schema_version,
path, alt; diff schema_version, title, files and exactly one of unified_diff, source.
Column fields are key, label, type, align, format.
Column type must be exactly one of: text, number, currency, percent, boolean, date, badge.
Column align must be exactly one of: left, center, right.
Column format may contain only: locale, currency, minimum_fraction_digits, maximum_fraction_digits.
Data source format must be exactly one of: json, csv, tsv; diff source format must be text.
Inline cells must be strings, finite numbers, booleans, or null.
Other fields and values are unsupported; they may be rejected or fail to render.

Managed path and source.path values must be clean, workdir-relative paths: never
absolute, traversal, or /sandbox paths.
Never choose sandbox flags, HTML trust, executable callbacks, or remote privileges;
the renderer owns those capabilities.

Use table for sortable, filterable, searchable, or groupable data. Use spreadsheet
for row/column presentation or XLSX export. Use a managed image card once, without
also writing a Markdown image for the same path.

EXAMPLE mermaid {"schema_version":1,"source":"flowchart LR\n  A --> B"}
EXAMPLE html-preview {"schema_version":1,"path":"reports/result.html"}
EXAMPLE table {"schema_version":1,"columns":[{"key":"gene","label":"Gene","type":"text"}],"rows":[{"gene":"BRCA1"}]}
EXAMPLE table-source {"schema_version":1,"columns":[{"key":"gene","label":"Gene","type":"text"}],"source":{"path":"results/genes.json","format":"json"}}
EXAMPLE spreadsheet {"schema_version":1,"columns":[{"key":"amount","label":"Amount","type":"number"}],"rows":[{"amount":42}]}
EXAMPLE spreadsheet-source {"schema_version":1,"columns":[{"key":"amount","label":"Amount","type":"number"}],"source":{"path":"results/data.tsv","format":"tsv"}}
EXAMPLE image {"schema_version":1,"path":"plots/result.png","alt":"Result plot"}
EXAMPLE diff {"schema_version":1,"unified_diff":"--- a/app.go\n+++ b/app.go\n@@ -1 +1 @@\n-old\n+new"}
EXAMPLE diff-source {"schema_version":1,"source":{"path":"changes/result.diff","format":"text"}}

Exactly one of ` + "`rows`" + ` or ` + "`source`" + ` is required for table and spreadsheet. Their
source formats are JSON, CSV, or TSV (` + "`json`" + `, ` + "`csv`" + `, or ` + "`tsv`" + `).
Exactly one of ` + "`unified_diff`" + ` or ` + "`source`" + ` is required for diff; its source format is ` + "`text`" + `.
For diff payloads up to 64 KiB, use inline ` + "`unified_diff`" + `. For larger diffs,
write the unified diff with ` + "`workdir_write_file`" + ` before emitting the card, then use
` + "`source`" + `. Never emit a diff source until the write succeeds. If source validation
fails, first write the file or use inline ` + "`unified_diff`" + `.
For more than 20 rows, write JSON/CSV/TSV with the workdir tools and use source
instead of inline rows. This is usage guidance, not a protocol limit.
Protocol hard ceilings remain 200 inline rows, 256 columns, and a 256 KiB serialized component.

Other supported payloads:
  chart   : {"type":"bar"|"line"|"pie"|"doughnut"|"radar"|"polarArea"|"scatter"|"bubble","data":{"labels":["A","B"],"datasets":[{"label":"Series 1","data":[1,2]}]},"options":{}}
  plan    : {"steps":[{"title":"Step 1","description":"...","status":"done|active|blocked|pending"}]}
  form    : {"fields":[{"name":"age","label":"Age","type":"number","required":true}]}
            This is a non-blocking preview; use request_form for blocking input.
  decision: {"prompt":"Proceed?","options":["yes","no"]}
  igv-command: {"actions":[{"type":"goto","locus":"chr1:16918-17558"}]}
  custom  : any JSON object, rendered through the JSON fallback.

The compatibility-only payloads below preserve historical messages and are not preferred output:
  file        : {"path":"results/report.txt","size":12345}
  remote image: {"url":"https://example.test/plot.png","alt":"Result plot"}
  legacy diff : {"diff":"--- a\n+++ b\n@@ ..."}
`

// makeEmitCardTool builds the emit_card tool bound to actor a.
//
// emit_card publishes an A2UI declarative card to subscribers and
// returns the assigned card_id immediately (non-blocking).
func makeEmitCardTool(a *Actor) *coretools.Tool {
	handler := func(ctx context.Context, req *coretools.Request) (*coretools.Result, error) {
		kind, ok := req.Arguments["kind"].(string)
		if !ok || kind == "" {
			return coretools.NewToolResultError("emit_card requires a 'kind' field: the card type (file, table, diff, form, plan, image, chart, custom, decision, igv-command, mermaid, html-preview, spreadsheet)"), nil
		}
		title, _ := req.Arguments["title"].(string)

		componentRaw, ok := req.Arguments["component"]
		if !ok {
			return coretools.NewToolResultError("emit_card requires a 'component' field"), nil
		}
		// Normalize the component into map[string]any. The agent may
		// pass a JSON object via the providers' decoded arguments.
		componentMap, err := toStringMap(componentRaw)
		if err != nil {
			return coretools.NewToolResultError("emit_card 'component' must be an object: " + err.Error()), nil
		}

		cardID, err := a.EmitCard(kind, title, componentMap)
		if err != nil {
			return coretools.NewToolResultError(err.Error()), nil
		}

		body := map[string]any{"card_id": cardID, "kind": kind}
		raw, _ := json.Marshal(body)
		return coretools.NewToolResultText(string(raw)), nil
	}

	return coretools.NewTool("emit_card",
		coretools.WithDescription(emitCardDescription),
		coretools.WithString("kind",
			coretools.Required(),
			coretools.Enum("file", "table", "spreadsheet", "diff", "mermaid", "html-preview", "form", "plan", "image", "chart", "decision", "igv-command", "custom"),
			coretools.Description("The A2UI card kind."),
		),
		coretools.WithString("title",
			coretools.Description("Optional display title for the emitted card."),
		),
		coretools.WithObject("component",
			coretools.Required(),
			coretools.MinProperties(1),
			coretools.Description("The declarative A2UI component payload."),
		),
		coretools.WithToolHandler(handler),
	)
}

// toStringMap converts an arbitrary value into map[string]any.
func toStringMap(v any) (map[string]any, error) {
	if v == nil {
		return nil, fmt.Errorf("nil value")
	}
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	// JSON round-trip covers map[string]interface{} decoded by some
	// providers as well as structs.
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
