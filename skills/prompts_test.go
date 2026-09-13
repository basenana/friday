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

func TestSkillToolsDoNotDuplicatePromptDiscovery(t *testing.T) {
	toolNames := map[string]bool{}
	for _, tool := range NewSkillTools(NewRegistry(&Loader{})) {
		toolNames[tool.Name] = true
	}
	if toolNames["list_skills"] {
		t.Fatal("list_skills should not duplicate the skills listed in the system prompt")
	}
	if !toolNames["load_skill"] {
		t.Fatal("load_skill is missing")
	}
}
