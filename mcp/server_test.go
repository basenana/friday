package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

func TestMCPTextContentCollectsAllTextBlocks(t *testing.T) {
	content := []mcpgo.Content{
		mcpgo.TextContent{Type: "text", Text: " first error "},
		mcpgo.ImageContent{Type: "image", Data: "ignored", MIMEType: "image/png"},
		mcpgo.TextContent{Type: "text", Text: "second error"},
	}
	if got := mcpTextContent(content); got != "first error\nsecond error" {
		t.Fatalf("mcpTextContent() = %q", got)
	}
}

func TestConvertMCPResultPreservesBusinessError(t *testing.T) {
	remote := &mcpgo.CallToolResult{
		Content: []mcpgo.Content{mcpgo.TextContent{Type: "text", Text: "query must not be empty"}},
		IsError: true,
	}
	result := convertMCPResult("search", remote)
	if result == nil || !result.IsError {
		t.Fatalf("converted result = %#v", result)
	}
	encoded, _ := json.Marshal(result)
	for _, want := range []string{"query must not be empty", "Suggestion:", "Full MCP response"} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("converted result missing %q: %s", want, encoded)
		}
	}
}

func TestConvertMCPToolPreservesRawInputSchema(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"mode":{"oneOf":[{"type":"string"},{"type":"number"}]}},"additionalProperties":true}`)
	converted := covertMCPTool(&mcpgo.Tool{Name: "remote", Description: "Remote tool", RawInputSchema: raw})
	schema := converted.JsonSchema()
	if schema["additionalProperties"] != true {
		t.Fatalf("raw schema lost additionalProperties: %#v", schema)
	}
	properties, _ := schema["properties"].(map[string]any)
	mode, _ := properties["mode"].(map[string]any)
	if _, ok := mode["oneOf"]; !ok {
		t.Fatalf("raw schema lost oneOf: %#v", schema)
	}

	// JsonSchema returns a copy so provider-side normalization cannot mutate the
	// MCP definition retained by the tool.
	schema["additionalProperties"] = false
	if converted.JsonSchema()["additionalProperties"] != true {
		t.Fatal("JsonSchema exposed mutable raw schema")
	}
}

func TestConvertMCPToolPreservesDefinitions(t *testing.T) {
	converted := covertMCPTool(&mcpgo.Tool{
		Name: "remote",
		InputSchema: mcpgo.ToolInputSchema{
			Type:       "object",
			Defs:       map[string]any{"identifier": map[string]any{"type": "string"}},
			Properties: map[string]any{"id": map[string]any{"$ref": "#/$defs/identifier"}},
		},
	})
	encoded, _ := json.Marshal(converted.JsonSchema())
	if !strings.Contains(string(encoded), `"$defs"`) {
		t.Fatalf("definitions were lost: %s", encoded)
	}
}
