package codebase

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/providers/fallback"
)

func TestDefaultSpecRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENT-SPEC.md")
	if err := createDefaultSpec(path); err != nil {
		t.Fatal(err)
	}
	spec, err := loadSpec(path, fallback.NewModelPool(nil))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Version != 1 || spec.Index.MaxLoopTimes != 100 || spec.Schedule.IdleDelay.Duration != time.Hour {
		t.Fatalf("unexpected default spec: %+v", spec)
	}
	if spec.Context.Timeout.Duration != 3*time.Minute {
		t.Fatalf("default Context timeout=%v, want 3m", spec.Context.Timeout.Duration)
	}
	if spec.Context.MaxLoopTimes != 10 {
		t.Fatalf("default Context max_loop_times=%d, want 10", spec.Context.MaxLoopTimes)
	}
	if spec.Context.MaxOutputTokens != 10000 {
		t.Fatalf("default Context max_output_tokens=%d, want 10000", spec.Context.MaxOutputTokens)
	}
	if spec.Context.Effort != "none" {
		t.Fatalf("default Context effort=%q, want none", spec.Context.Effort)
	}
	for _, advisory := range []string{
		"Prefer the maintained knowledge base",
		"Avoid broad exploration",
		"stop as soon as the available evidence is sufficient",
	} {
		if !strings.Contains(spec.Body, advisory) {
			t.Fatalf("default editable policy missing %q:\n%s", advisory, spec.Body)
		}
	}
	before, _ := os.ReadFile(path)
	if err := createDefaultSpec(path); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("default spec overwrote existing file")
	}
}

func TestSpecRejectsUnknownFieldsAndInvalidValues(t *testing.T) {
	base := defaultSpecFile
	tests := map[string]string{
		"unknown field":     strings.Replace(base, "version: 1", "version: 1\nextra: true", 1),
		"bad version":       strings.Replace(base, "version: 1", "version: 2", 1),
		"empty body":        strings.Split(base, "---\n")[0] + "---\n",
		"bad timeout":       strings.Replace(base, "timeout: 180s", "timeout: 0s", 1),
		"bad loop":          strings.Replace(base, "max_loop_times: 10\n", "max_loop_times: 0\n", 1),
		"bad effort":        strings.Replace(base, "effort: default", "effort: impossible", 1),
		"unknown model":     strings.Replace(base, "model: \"\"", "model: missing", 1),
		"trailing document": strings.Replace(base, "\n---\nPrefer", "\n...\nversion: 1\n---\nPrefer", 1),
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "AGENT-SPEC.md")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadSpec(path, fallback.NewModelPool(nil)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
