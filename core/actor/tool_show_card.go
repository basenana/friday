package actor

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	coretools "github.com/basenana/friday/core/tools"
)

func makeShowCardTools(a *Actor) []*coretools.Tool {
	return []*coretools.Tool{
		coretools.NewTool("show_image",
			coretools.WithDescription("Display an existing workdir-relative image as a managed card. Use the image tool instead when the image must be analyzed. Returns the emitted card ID."),
			coretools.WithString("path", coretools.Required(), coretools.Description("Existing clean workdir-relative image path.")),
			coretools.WithString("alt", coretools.Description("Optional accessible image description.")),
			coretools.WithExample(map[string]any{"path": "plots/result.png", "alt": "Benchmark result"}),
			coretools.WithToolHandler(showImageHandler(a))),
		coretools.NewTool("show_mermaid",
			coretools.WithDescription("Render Mermaid source as a diagram card and return the emitted card ID. Provide source directly without a Markdown fence."),
			coretools.WithString("source", coretools.Required(), coretools.MinLength(1), coretools.Description("Mermaid source without a Markdown fence.")),
			coretools.WithExample(map[string]any{"source": "flowchart LR\n  A[Request] --> B[Validation]\n  B --> C[Handler]"}),
			coretools.WithToolHandler(showMermaidHandler(a))),
		coretools.NewTool("show_html",
			coretools.WithDescription("Display an existing workdir-relative HTML file in the managed preview and return the emitted card ID. Write the file before calling this tool."),
			coretools.WithString("path", coretools.Required(), coretools.Description("Existing clean workdir-relative HTML path.")),
			coretools.WithExample(map[string]any{"path": "reports/tool-audit.html"}),
			coretools.WithToolHandler(showHTMLHandler(a))),
		coretools.NewTool("show_diff",
			coretools.WithDescription("Display a unified diff and return the emitted card ID. Provide exactly one of diff for inline content up to 64 KiB or path for an existing workdir-relative diff file."),
			coretools.WithString("diff", coretools.Description("Inline unified diff. Mutually exclusive with path.")),
			coretools.WithString("path", coretools.Description("Existing clean workdir-relative diff path. Mutually exclusive with diff.")),
			coretools.WithExample(map[string]any{"diff": "--- a/app.go\n+++ b/app.go\n@@ -1 +1 @@\n-old\n+new"}),
			coretools.WithExample(map[string]any{"path": "changes/tool-contract.diff"}),
			coretools.WithToolHandler(showDiffHandler(a))),
		coretools.NewTool("show_table",
			coretools.WithDescription("Display an existing JSON, CSV, or TSV file as a table card and return the emitted card ID. Write the file first; its format is inferred from the extension."),
			coretools.WithString("path", coretools.Required(), coretools.Description("Existing clean workdir-relative JSON, CSV, or TSV path.")),
			coretools.WithArray("columns", coretools.Required(), coretools.MinItems(1), coretools.UniqueItems(true), coretools.Items(map[string]any{"type": "string", "minLength": 1}), coretools.Description("Source column keys in display order.")),
			coretools.WithString("title", coretools.Description("Optional display title.")),
			coretools.WithExample(map[string]any{"path": "results/tools.csv", "columns": []any{"tool", "status", "issue"}, "title": "Built-in tool audit"}),
			coretools.WithToolHandler(showTableHandler(a))),
	}
}

func cardResult(id string) *coretools.Result {
	raw, _ := json.Marshal(map[string]any{"card_id": id})
	return coretools.NewToolResultText(string(raw))
}

func emitTypedCard(a *Actor, kind string, component map[string]any) (*coretools.Result, error) {
	id, err := a.EmitCard(kind, "", component)
	if err != nil {
		return coretools.NewToolResultError(err.Error()), nil
	}
	return cardResult(id), nil
}

func showImageHandler(a *Actor) coretools.ToolHandlerFunc {
	return func(_ context.Context, req *coretools.Request) (*coretools.Result, error) {
		component := map[string]any{"schema_version": 1, "path": req.Arguments["path"]}
		if alt := stringArgument(req.Arguments, "alt"); alt != "" {
			component["alt"] = alt
		}
		return emitTypedCard(a, "image", component)
	}
}

func showMermaidHandler(a *Actor) coretools.ToolHandlerFunc {
	return func(_ context.Context, req *coretools.Request) (*coretools.Result, error) {
		return emitTypedCard(a, "mermaid", map[string]any{"schema_version": 1, "source": req.Arguments["source"]})
	}
}

func showHTMLHandler(a *Actor) coretools.ToolHandlerFunc {
	return func(_ context.Context, req *coretools.Request) (*coretools.Result, error) {
		return emitTypedCard(a, "html-preview", map[string]any{"schema_version": 1, "path": req.Arguments["path"]})
	}
}

func showDiffHandler(a *Actor) coretools.ToolHandlerFunc {
	return func(_ context.Context, req *coretools.Request) (*coretools.Result, error) {
		diff, _ := req.Arguments["diff"].(string)
		path, _ := req.Arguments["path"].(string)
		if (strings.TrimSpace(diff) == "") == (strings.TrimSpace(path) == "") {
			return coretools.NewToolResultError("provide exactly one of diff or path"), nil
		}
		component := map[string]any{"schema_version": 1}
		if diff != "" {
			component["unified_diff"] = diff
		} else {
			component["source"] = map[string]any{"path": path, "format": "text"}
		}
		return emitTypedCard(a, "diff", component)
	}
}

func showTableHandler(a *Actor) coretools.ToolHandlerFunc {
	return func(_ context.Context, req *coretools.Request) (*coretools.Result, error) {
		path, _ := req.Arguments["path"].(string)
		format := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		if format != "json" && format != "csv" && format != "tsv" {
			return coretools.NewToolResultError("table path must end in .json, .csv, or .tsv"), nil
		}
		rawColumns := req.Arguments["columns"].([]any)
		columns := make([]any, 0, len(rawColumns))
		for _, raw := range rawColumns {
			key := strings.TrimSpace(raw.(string))
			columns = append(columns, map[string]any{"key": key, "label": key, "type": "text"})
		}
		component := map[string]any{
			"schema_version": 1,
			"columns":        columns,
			"source":         map[string]any{"path": path, "format": format},
		}
		id, err := a.EmitCard("table", stringArgument(req.Arguments, "title"), component)
		if err != nil {
			return coretools.NewToolResultError(err.Error()), nil
		}
		return cardResult(id), nil
	}
}
