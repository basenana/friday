package main

import (
	"path/filepath"
	"testing"

	"github.com/basenana/friday/skills"
)

func TestEnsureSkillDeletionAllowed(t *testing.T) {
	projectSkills := filepath.Join(t.TempDir(), "workspace", "skills")
	projectSkill := &skills.Skill{Name: "shared", BasePath: filepath.Join(projectSkills, "shared")}
	if err := ensureSkillDeletionAllowed(projectSkills, projectSkill); err != nil {
		t.Fatalf("project skill should be deletable: %v", err)
	}

	globalSkill := &skills.Skill{Name: "inherited", BasePath: filepath.Join(t.TempDir(), "workspace", "skills", "inherited")}
	if err := ensureSkillDeletionAllowed(projectSkills, globalSkill); err == nil {
		t.Fatal("inherited HOME skill should not be deletable from project scope")
	}
}
