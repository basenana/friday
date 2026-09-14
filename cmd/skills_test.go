package main

import (
	"path/filepath"
	"testing"

	"github.com/basenana/friday/skills"
)

func TestEnsureSkillDeletionAllowed(t *testing.T) {
	projectSkills := filepath.Join(t.TempDir(), "workspace", "skills")
	projectSkill := &skills.Skill{Name: "shared", BasePath: filepath.Join(projectSkills, "shared")}
	if err := ensureSkillDeletionAllowed(true, projectSkills, projectSkill); err != nil {
		t.Fatalf("project skill should be deletable: %v", err)
	}

	globalSkill := &skills.Skill{Name: "inherited", BasePath: filepath.Join(t.TempDir(), "workspace", "skills", "inherited")}
	if err := ensureSkillDeletionAllowed(true, projectSkills, globalSkill); err == nil {
		t.Fatal("inherited HOME skill should not be deletable from project scope")
	}
	if err := ensureSkillDeletionAllowed(false, projectSkills, globalSkill); err != nil {
		t.Fatalf("HOME scope should allow deleting HOME skill: %v", err)
	}
}
