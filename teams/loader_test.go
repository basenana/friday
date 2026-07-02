package teams

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoader_LoadTeam(t *testing.T) {
	dir := t.TempDir()
	teamDir := filepath.Join(dir, "alpha")
	membersDir := filepath.Join(teamDir, "members")
	if err := os.MkdirAll(membersDir, 0755); err != nil {
		t.Fatal(err)
	}
	teamJSON := `{"name":"alpha","description":"Alpha team","members":[{"name":"leader","role":"leader"},{"name":"dev","role":"member","model":"gpt-4o"}]}`
	if err := os.WriteFile(filepath.Join(teamDir, "team.json"), []byte(teamJSON), 0644); err != nil {
		t.Fatal(err)
	}
	leaderMD := "---\nname: leader\nrole: leader\nskills: [planning]\ntools_allow: [team_comment]\n---\nYou lead.\n"
	if err := os.WriteFile(filepath.Join(membersDir, "leader.md"), []byte(leaderMD), 0644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(dir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := loader.Get("alpha")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(got.Members))
	}

	members, err := LoadMembers(dir, "alpha", got.Members)
	if err != nil {
		t.Fatalf("LoadMembers: %v", err)
	}
	leader := Leader(members)
	if leader == nil {
		t.Fatal("expected leader")
	}
	if leader.Instructions != "You lead." {
		t.Errorf("leader instructions = %q", leader.Instructions)
	}
	if len(leader.ToolsAllow) != 1 || leader.ToolsAllow[0] != "team_comment" {
		t.Errorf("leader tools_allow = %v", leader.ToolsAllow)
	}

	dev := FindMember(members, "dev")
	if dev == nil {
		t.Fatal("expected to find dev")
	}
	if dev.Model != "gpt-4o" {
		t.Errorf("dev model = %q", dev.Model)
	}
}

func TestRegistry_SetActive(t *testing.T) {
	dir := t.TempDir()
	teamDir := filepath.Join(dir, "beta")
	if err := os.MkdirAll(teamDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "team.json"), []byte(`{"name":"beta","members":[{"name":"lead","role":"leader"}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	loader := NewLoader(dir)
	if err := loader.Load(); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(loader)
	reg.Refresh()
	if !reg.SetActive("beta") {
		t.Fatal("expected SetActive to succeed")
	}
	if reg.ActiveName() != "beta" {
		t.Errorf("active = %q", reg.ActiveName())
	}
	if reg.SetActive("nonexistent") {
		t.Error("expected SetActive to fail for unknown team")
	}
}

func TestLoader_LoadRefreshRemovesDeletedTeams(t *testing.T) {
	dir := t.TempDir()
	alphaDir := filepath.Join(dir, "alpha")
	betaDir := filepath.Join(dir, "beta")
	if err := os.MkdirAll(alphaDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(betaDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alphaDir, "team.json"), []byte(`{"name":"alpha","members":[{"name":"lead","role":"leader"}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(betaDir, "team.json"), []byte(`{"name":"beta","members":[{"name":"lead","role":"leader"}]}`), 0644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(dir)
	if err := loader.Load(); err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if len(loader.List()) != 2 {
		t.Fatalf("expected 2 teams after first load, got %d", len(loader.List()))
	}

	if err := os.RemoveAll(betaDir); err != nil {
		t.Fatalf("remove beta: %v", err)
	}
	if err := loader.Load(); err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if len(loader.List()) != 1 {
		t.Fatalf("expected stale team to be removed, got %d teams", len(loader.List()))
	}
	if _, err := loader.Get("beta"); err == nil {
		t.Fatal("expected removed team beta to disappear from cache")
	}
}

func TestLoader_LoadRejectsInvalidTeamName(t *testing.T) {
	dir := t.TempDir()
	teamDir := filepath.Join(dir, "alpha")
	if err := os.MkdirAll(teamDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "team.json"), []byte(`{"name":"../../escape","members":[{"name":"lead","role":"leader"}]}`), 0644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(dir)
	if err := loader.Load(); err == nil {
		t.Fatal("expected invalid team name to fail load")
	}
}
