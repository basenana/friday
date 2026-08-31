package actor

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestEmitCardUsesRichManagedPathValidator(t *testing.T) {
	defaultActor := New(nil, nil)
	if _, err := defaultActor.EmitCard("html-preview", "", map[string]any{
		"schema_version": float64(1),
		"path":           "reports/a.html",
	}); err != nil {
		t.Fatalf("default Actor rich EmitCard() error = %v", err)
	}
	if _, err := defaultActor.EmitCard("file", "", map[string]any{"path": "/sandbox/a.txt"}); err != nil {
		t.Fatalf("default Actor legacy file EmitCard() error = %v", err)
	}
	if _, err := defaultActor.EmitCard("file", "", map[string]any{"path": "reports/a.txt"}); err == nil {
		t.Fatal("default Actor legacy file unexpectedly accepted relative path")
	}

	calls := []string{}
	overriddenActor := New(nil, nil, WithFilePathValidator(func(value string) error {
		calls = append(calls, value)
		if value != "reports/a.html" {
			return fmt.Errorf("unexpected path %q", value)
		}
		return nil
	}))
	if _, err := overriddenActor.EmitCard("html-preview", "", map[string]any{
		"schema_version": float64(1),
		"path":           "reports/a.html",
	}); err != nil {
		t.Fatalf("override Actor rich EmitCard() error = %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"reports/a.html"}) {
		t.Fatalf("override validator calls = %#v", calls)
	}
}

func TestEmitCardUsesDedicatedDiffSourcePathValidator(t *testing.T) {
	var managedCalls []string
	var diffSourceCalls []string
	a := New(nil, nil,
		WithFilePathValidator(func(value string) error {
			managedCalls = append(managedCalls, value)
			return nil
		}),
		WithDiffSourcePathValidator(func(value string) error {
			diffSourceCalls = append(diffSourceCalls, value)
			return nil
		}),
	)

	if _, err := a.EmitCard("html-preview", "", map[string]any{
		"schema_version": float64(1),
		"path":           "reports/a.html",
	}); err != nil {
		t.Fatalf("html-preview EmitCard() error = %v", err)
	}
	if _, err := a.EmitCard("diff", "", map[string]any{
		"schema_version": float64(1),
		"source": map[string]any{
			"path":   "changes/a.diff",
			"format": "text",
		},
	}); err != nil {
		t.Fatalf("source diff EmitCard() error = %v", err)
	}
	if _, err := a.EmitCard("diff", "", map[string]any{
		"schema_version": float64(1),
		"unified_diff":   "--- a/file\n+++ b/file\n@@ -1 +1 @@\n-old\n+new",
	}); err != nil {
		t.Fatalf("inline diff EmitCard() error = %v", err)
	}

	if !reflect.DeepEqual(managedCalls, []string{"reports/a.html"}) {
		t.Fatalf("managed validator calls = %#v", managedCalls)
	}
	if !reflect.DeepEqual(diffSourceCalls, []string{"changes/a.diff"}) {
		t.Fatalf("diff source validator calls = %#v", diffSourceCalls)
	}
}

func TestEmitCardFilePathValidatorStillAppliesToDiffSources(t *testing.T) {
	var calls []string
	a := New(nil, nil, WithFilePathValidator(func(value string) error {
		calls = append(calls, value)
		return nil
	}))

	if _, err := a.EmitCard("diff", "", map[string]any{
		"schema_version": float64(1),
		"source": map[string]any{
			"path":   "changes/a.diff",
			"format": "text",
		},
	}); err != nil {
		t.Fatalf("source diff EmitCard() error = %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"changes/a.diff"}) {
		t.Fatalf("validator calls = %#v", calls)
	}
}

func TestEmitCardRejectsInvalidInlineUnifiedDiff(t *testing.T) {
	a := New(nil, nil, WithUnifiedDiffValidator(func([]byte) error {
		return fmt.Errorf("hunk count mismatch")
	}))

	if _, err := a.EmitCard("diff", "", map[string]any{
		"schema_version": float64(1),
		"unified_diff":   "--- a/file\n+++ b/file\n@@ -1 +1 @@\n-old\n+new",
	}); err == nil || !strings.Contains(err.Error(), "hunk count mismatch") {
		t.Fatalf("EmitCard() error = %v, want validator error", err)
	}
}
