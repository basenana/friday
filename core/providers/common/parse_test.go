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

func TestParseToolUseArguments(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		ok   bool
	}{
		{"valid object", `{"a":1,"b":"x"}`, true},
		{"empty object", `{}`, true},
		{"array", `[1,2,3]`, false},
		{"scalar number", `42`, false},
		{"scalar string", `"hi"`, false},
		{"empty", ``, false},
		{"whitespace", `   `, false},
		{"truncated json", `{"a":`, false},
		{"invalid", `not json`, false},
		{"null", `null`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, ok := ParseToolUseArguments(tc.raw)
			if ok != tc.ok {
				t.Fatalf("ParseToolUseArguments(%q) ok=%v want %v; args=%v", tc.raw, ok, tc.ok, args)
			}
			if ok && args == nil {
				t.Fatalf("ok=true but args nil for %q", tc.raw)
			}
		})
	}
}

func TestNormalizeToolUseArguments(t *testing.T) {
	t.Run("valid passes through", func(t *testing.T) {
		raw := `{"x":1}`
		out, msg, ok := NormalizeToolUseArguments(raw, "toolA")
		if !ok || out != raw || msg != "" {
			t.Fatalf("got (%q,%q,%v) want (%q,%q,true)", out, msg, ok, raw, "")
		}
	})
	t.Run("array normalized to empty object", func(t *testing.T) {
		out, msg, ok := NormalizeToolUseArguments(`[1,2]`, "toolB")
		if ok {
			t.Fatalf("expected ok=false for array input")
		}
		if out != "{}" {
			t.Fatalf("expected %q, got %q", "{}", out)
		}
		if msg == "" {
			t.Fatalf("expected non-empty error message")
		}
	})
	t.Run("empty normalized", func(t *testing.T) {
		out, msg, ok := NormalizeToolUseArguments(``, "toolC")
		if ok || out != "{}" || msg == "" {
			t.Fatalf("expected normalization of empty input; got (%q,%q,%v)", out, msg, ok)
		}
	})
}

func TestFormatToolUseArgumentsError_Truncates(t *testing.T) {
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'x'
	}
	msg := FormatToolUseArgumentsError("bigTool", string(long))
	if len(msg) > 200 {
		t.Fatalf("error message not truncated: %d chars", len(msg))
	}
}
