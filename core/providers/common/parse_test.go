package common

import "testing"

func TestExtractJSONParsesMarkdownFencedJSON(t *testing.T) {
	var out struct {
		Name  string `json:"name"`
		Notes string `json:"notes"`
	}

	content := "Here is the result:\n```json\n{\n  \"name\": \"demo\",\n  \"notes\": \"line1\\nline2\"\n}\n```\nThanks."

	if err := ExtractJSON(content, &out); err != nil {
		t.Fatalf("ExtractJSON failed: %v", err)
	}
	if out.Name != "demo" {
		t.Fatalf("expected name demo, got %#v", out.Name)
	}
	if out.Notes != "line1\nline2" {
		t.Fatalf("unexpected notes: %#v", out.Notes)
	}
}

func TestExtractJSONPrefersValidCandidateWhenEarlierFenceIsBroken(t *testing.T) {
	var out struct {
		Name string `json:"name"`
	}

	content := "```json\n{\"name\": }\n```\nnoise\n```json\n{\"name\": \"demo\"}\n```"

	if err := ExtractJSON(content, &out); err != nil {
		t.Fatalf("ExtractJSON failed: %v", err)
	}
	if out.Name != "demo" {
		t.Fatalf("expected name demo, got %#v", out.Name)
	}
}

func TestExtractJSONFallsBackToBalancedJSONObject(t *testing.T) {
	var out struct {
		Name string `json:"name"`
	}

	content := "prefix {\"name\":\"demo\"} trailing explanation"

	if err := ExtractJSON(content, &out); err != nil {
		t.Fatalf("ExtractJSON failed: %v", err)
	}
	if out.Name != "demo" {
		t.Fatalf("expected name demo, got %#v", out.Name)
	}
}
