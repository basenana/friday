package proposals

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoader_ProposalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	loader := NewLoader(dir)

	p := &Proposal{
		ID:        "p-rt",
		Title:     "Round Trip",
		Status:    ProposalActive,
		OwningTeam: "alpha",
		Sessions:  map[string]string{"leader": "sess-1"},
	}
	if err := loader.InitProposal(p, "# Doc\n\nBody."); err != nil {
		t.Fatalf("InitProposal: %v", err)
	}

	// Directory tree exists.
	if _, err := os.Stat(filepath.Join(loader.ProposalDir("p-rt"), "proposal.json")); err != nil {
		t.Errorf("proposal.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(loader.ProposalDir("p-rt"), "PROPOSAL.md")); err != nil {
		t.Errorf("PROPOSAL.md missing: %v", err)
	}
	if _, err := os.Stat(loader.TasksDir("p-rt")); err != nil {
		t.Errorf("tasks dir missing: %v", err)
	}

	got, err := loader.LoadProposal("p-rt")
	if err != nil {
		t.Fatalf("LoadProposal: %v", err)
	}
	if got.Title != "Round Trip" || got.OwningTeam != "alpha" {
		t.Errorf("unexpected proposal: %+v", got)
	}
	if got.Sessions["leader"] != "sess-1" {
		t.Errorf("session map not preserved: %v", got.Sessions)
	}

	doc, err := loader.LoadDesignDoc("p-rt")
	if err != nil {
		t.Fatalf("LoadDesignDoc: %v", err)
	}
	if doc == "" {
		t.Error("expected design doc content")
	}
}

func TestLoader_TasksRoundTrip(t *testing.T) {
	dir := t.TempDir()
	loader := NewLoader(dir)
	p := &Proposal{ID: "p-tasks", Title: "Tasks Test", Status: ProposalDraft, Sessions: map[string]string{}}
	if err := loader.InitProposal(p, "doc"); err != nil {
		t.Fatalf("InitProposal: %v", err)
	}

	tasks := []Task{
		{ID: "T01", Title: "first", Status: TaskApproved, Deps: []string{}},
		{ID: "T02", Title: "second", Status: TaskReady, Deps: []string{"T01"}, Assignee: "dev"},
	}
	if err := loader.SaveTasks(p.ID, tasks); err != nil {
		t.Fatalf("SaveTasks: %v", err)
	}

	got, err := loader.LoadTasks(p.ID)
	if err != nil {
		t.Fatalf("LoadTasks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(got))
	}
	// Find by ID since order isn't guaranteed.
	byID := map[string]Task{}
	for _, tk := range got {
		byID[tk.ID] = tk
	}
	if byID["T02"].Assignee != "dev" {
		t.Errorf("assignee lost: %+v", byID["T02"])
	}
	if byID["T01"].Status != TaskApproved {
		t.Errorf("status lost: %+v", byID["T01"])
	}
}

func TestLoader_DuplicateInitFails(t *testing.T) {
	dir := t.TempDir()
	loader := NewLoader(dir)
	p := &Proposal{ID: "p-dup", Title: "Dup", Status: ProposalDraft, Sessions: map[string]string{}}
	if err := loader.InitProposal(p, "doc"); err != nil {
		t.Fatalf("first InitProposal: %v", err)
	}
	if err := loader.InitProposal(p, "doc"); err == nil {
		t.Fatal("expected duplicate-init error")
	}
}
