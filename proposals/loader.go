package proposals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Loader reads/writes proposal files on disk.
//
//	{root}/{proposalID}/proposal.json
//	{root}/{proposalID}/PROPOSAL.md
//	{root}/{proposalID}/tasks/{T01}.json
//	{root}/{proposalID}/tasks/{T01}.md
//	{root}/{proposalID}/tasks/{T01}.result.md
type Loader struct {
	root string
}

// NewLoader constructs a Loader rooted at the proposals data directory.
func NewLoader(root string) *Loader {
	return &Loader{root: root}
}

// Root returns the proposals root directory.
func (l *Loader) Root() string { return l.root }

// ProposalDir returns the directory path for a proposal.
func (l *Loader) ProposalDir(id string) string {
	return filepath.Join(l.root, id)
}

// TasksDir returns the directory path for a proposal's tasks.
func (l *Loader) TasksDir(id string) string {
	return filepath.Join(l.ProposalDir(id), "tasks")
}

// InitProposal creates the proposal directory tree and writes proposal.json +
// PROPOSAL.md. Returns an error if the proposal already exists.
func (l *Loader) InitProposal(p *Proposal, designDoc string) error {
	dir := l.ProposalDir(p.ID)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("proposal already exists: %s", p.ID)
	}
	if err := os.MkdirAll(l.TasksDir(p.ID), 0755); err != nil {
		return err
	}
	if err := l.SaveProposal(p); err != nil {
		return err
	}
	return l.SaveDesignDoc(p.ID, designDoc)
}

// SaveProposal writes proposal.json.
func (l *Loader) SaveProposal(p *Proposal) error {
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	p.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(l.ProposalDir(p.ID), "proposal.json"), data, 0644)
}

// LoadProposal reads proposal.json. Returns an error if missing.
func (l *Loader) LoadProposal(id string) (*Proposal, error) {
	data, err := os.ReadFile(filepath.Join(l.ProposalDir(id), "proposal.json"))
	if err != nil {
		return nil, err
	}
	var p Proposal
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// SaveDesignDoc writes PROPOSAL.md.
func (l *Loader) SaveDesignDoc(id, doc string) error {
	return os.WriteFile(filepath.Join(l.ProposalDir(id), "PROPOSAL.md"), []byte(doc), 0644)
}

// LoadDesignDoc reads PROPOSAL.md.
func (l *Loader) LoadDesignDoc(id string) (string, error) {
	data, err := os.ReadFile(filepath.Join(l.ProposalDir(id), "PROPOSAL.md"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SaveTasks writes each task as tasks/{id}.json. Task markdown is not overwritten
// here (use SaveTaskDoc to author/refresh task briefs).
func (l *Loader) SaveTasks(proposalID string, tasks []Task) error {
	for i := range tasks {
		if err := l.SaveTask(proposalID, &tasks[i]); err != nil {
			return err
		}
	}
	return nil
}

// SaveTask writes tasks/{id}.json.
func (l *Loader) SaveTask(proposalID string, t *Task) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(l.TasksDir(proposalID), t.ID+".json"), data, 0644)
}

// SaveTaskDoc writes tasks/{id}.md (the task brief handed to the executor).
func (l *Loader) SaveTaskDoc(proposalID, taskID, doc string) error {
	return os.WriteFile(filepath.Join(l.TasksDir(proposalID), taskID+".md"), []byte(doc), 0644)
}

// LoadTaskDoc reads tasks/{id}.md.
func (l *Loader) LoadTaskDoc(proposalID, taskID string) (string, error) {
	data, err := os.ReadFile(filepath.Join(l.TasksDir(proposalID), taskID+".md"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SaveTaskResult writes tasks/{id}.result.md with the full execute output.
func (l *Loader) SaveTaskResult(proposalID, taskID, result string) error {
	return os.WriteFile(filepath.Join(l.TasksDir(proposalID), taskID+".result.md"), []byte(result), 0644)
}

// LoadTaskResult reads tasks/{id}.result.md.
func (l *Loader) LoadTaskResult(proposalID, taskID string) (string, error) {
	data, err := os.ReadFile(filepath.Join(l.TasksDir(proposalID), taskID+".result.md"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// LoadTasks reads all task .json files for a proposal, sorted by ID.
func (l *Loader) LoadTasks(proposalID string) ([]Task, error) {
	entries, err := os.ReadDir(l.TasksDir(proposalID))
	if err != nil {
		return nil, err
	}
	var out []Task
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(l.TasksDir(proposalID), e.Name()))
		if err != nil {
			return nil, err
		}
		var t Task
		if err := json.Unmarshal(data, &t); err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		out = append(out, t)
	}
	return out, nil
}

// LoadTask reads a single task by ID.
func (l *Loader) LoadTask(proposalID, taskID string) (*Task, error) {
	data, err := os.ReadFile(filepath.Join(l.TasksDir(proposalID), taskID+".json"))
	if err != nil {
		return nil, err
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}
