package skills

import (
	"strings"
	"testing"
)

func TestBuiltSkillsSystemPromptSortsSkillsByName(t *testing.T) {
	registry := NewRegistry(&Loader{skillsPaths: []string{"/skills"}})
	skills := []*Skill{
		{Name: "zeta", Description: "last", BasePath: "/skills/zeta"},
		{Name: "alpha", Description: "first", BasePath: "/skills/alpha"},
		{Name: "middle", Description: "middle", BasePath: "/skills/middle"},
	}

	prompt := builtSkillsSystemPrompt(registry, skills)

	alpha := strings.Index(prompt, "<name>alpha</name>")
	middle := strings.Index(prompt, "<name>middle</name>")
	zeta := strings.Index(prompt, "<name>zeta</name>")
	if alpha == -1 || middle == -1 || zeta == -1 {
		t.Fatalf("expected all skills to appear in prompt, got %q", prompt)
	}
	if !(alpha < middle && middle < zeta) {
		t.Fatalf("expected skills to be ordered by name, got %q", prompt)
	}
}
