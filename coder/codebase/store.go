package codebase

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

type runtimeState string

const (
	StateIdle     runtimeState = "idle"
	StateIndexing runtimeState = "indexing"
	StateDegraded runtimeState = "degraded"
	StateError    runtimeState = "error"
)

type Status struct {
	Version             int                  `yaml:"version"`
	State               runtimeState         `yaml:"state"`
	UpdatedAt           time.Time            `yaml:"updated_at"`
	OperationStartedAt  time.Time            `yaml:"operation_started_at,omitempty"`
	OperationID         string               `yaml:"operation_id,omitempty"`
	TriggerReasons      []string             `yaml:"trigger_reasons,omitempty"`
	LastRequested       map[string]time.Time `yaml:"last_requested,omitempty"`
	LastAttempted       map[string]time.Time `yaml:"last_attempted,omitempty"`
	LastSuccessfulIndex time.Time            `yaml:"last_successful_index,omitempty"`
	ObservedHEAD        string               `yaml:"observed_head,omitempty"`
	IndexedHEAD         string               `yaml:"indexed_head,omitempty"`
	Branch              string               `yaml:"branch,omitempty"`
	Detached            bool                 `yaml:"detached,omitempty"`
	Dirty               bool                 `yaml:"dirty,omitempty"`
	DirtyHash           string               `yaml:"dirty_hash,omitempty"`
	CandidateHash       string               `yaml:"candidate_hash,omitempty"`
	AfterPath           string               `yaml:"after_path,omitempty"`
	PresentedCount      int                  `yaml:"presented_count,omitempty"`
	CandidateCount      int                  `yaml:"candidate_count,omitempty"`
	LastError           string               `yaml:"last_error,omitempty"`
	LastCancellation    string               `yaml:"last_cancellation,omitempty"`
}

type store struct{ dir string }

func newStore(dataDir, projectID string) *store {
	return &store{dir: filepath.Join(dataDir, "projects", projectID, "codebase")}
}

func (s *store) specPath() string     { return filepath.Join(s.dir, "AGENT-SPEC.md") }
func (s *store) sessionPath() string  { return filepath.Join(s.dir, "SESSION") }
func (s *store) statusPath() string   { return filepath.Join(s.dir, "STATUS.md") }
func (s *store) indexPath() string    { return filepath.Join(s.dir, "INDEX.md") }
func (s *store) knowledgeDir() string { return filepath.Join(s.dir, "knowledge") }

func (s *store) ensureLayout() error {
	if err := os.MkdirAll(s.knowledgeDir(), 0o700); err != nil {
		return err
	}
	if err := createDefaultSpec(s.specPath()); err != nil {
		return err
	}
	if err := createIfAbsent(s.indexPath(), []byte(`# Codebase Index

## Project Overview

Not indexed yet.

## Major Modules

Not indexed yet.

## Primary Interactions

Not indexed yet.

## Architecture Conventions

Not indexed yet.

## Knowledge Map

No routed knowledge documents yet.

## Known Unknowns

Initial indexing has not completed.
`)); err != nil {
		return err
	}
	if _, err := os.Stat(s.statusPath()); os.IsNotExist(err) {
		_, err = s.writeStatus(defaultStatus(StateIdle))
		return err
	} else {
		return err
	}
}

func validSessionID(id string) bool {
	if id == "" || len(id) > 255 || id != strings.TrimSpace(id) || strings.ContainsAny(id, `/\`) {
		return false
	}
	return !strings.ContainsFunc(id, unicode.IsControl)
}

func (s *store) writeSession(id string) error {
	if !validSessionID(id) {
		return fmt.Errorf("invalid Codebase SESSION id")
	}
	return atomicWrite(s.sessionPath(), []byte(id+"\n"), 0o600)
}

func (s *store) readSession() (string, error) {
	data, err := os.ReadFile(s.sessionPath())
	if err != nil {
		return "", err
	}
	if len(data) > 257 || len(data) == 0 || data[len(data)-1] != '\n' || bytes.Count(data, []byte("\n")) != 1 {
		return "", fmt.Errorf("invalid Codebase SESSION")
	}
	id := string(data[:len(data)-1])
	if !validSessionID(id) {
		return "", fmt.Errorf("invalid Codebase SESSION id")
	}
	return id, nil
}

func defaultStatus(state runtimeState) Status {
	return Status{Version: 1, State: state, UpdatedAt: time.Now(), LastRequested: map[string]time.Time{}, LastAttempted: map[string]time.Time{}}
}

func cloneStatus(status Status) Status {
	status.TriggerReasons = append([]string(nil), status.TriggerReasons...)
	status.LastRequested = cloneTimes(status.LastRequested)
	status.LastAttempted = cloneTimes(status.LastAttempted)
	return status
}

func cloneTimes(src map[string]time.Time) map[string]time.Time {
	if src == nil {
		return nil
	}
	dst := make(map[string]time.Time, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func (s *store) loadStatus() (Status, error) {
	data, err := os.ReadFile(s.statusPath())
	if os.IsNotExist(err) {
		status := defaultStatus(StateDegraded)
		return s.writeStatus(status)
	}
	if err != nil {
		return Status{}, err
	}
	status, err := parseStatus(data)
	if err == nil {
		if status.State == StateIndexing {
			status.State = StateDegraded
			status.LastCancellation = "previous process ended during indexing"
			var writeErr error
			status, writeErr = s.writeStatus(status)
			if writeErr != nil {
				return Status{}, writeErr
			}
		}
		return status, nil
	}
	quarantine := filepath.Join(s.dir, "STATUS.invalid-"+time.Now().UTC().Format("20060102T150405.000000000Z")+".md")
	if renameErr := os.Rename(s.statusPath(), quarantine); renameErr != nil {
		return Status{}, fmt.Errorf("quarantine invalid STATUS: %w", renameErr)
	}
	status = defaultStatus(StateDegraded)
	status.LastError = boundText(err.Error(), 2000)
	return s.writeStatus(status)
}

func parseStatus(data []byte) (Status, error) {
	front, _, err := splitFrontmatter(data)
	if err != nil {
		return Status{}, err
	}
	var status Status
	dec := yaml.NewDecoder(bytes.NewReader(front))
	dec.KnownFields(true)
	if err := dec.Decode(&status); err != nil {
		return Status{}, err
	}
	if status.Version != 1 {
		return Status{}, fmt.Errorf("unsupported STATUS version %d", status.Version)
	}
	switch status.State {
	case StateIdle, StateIndexing, StateDegraded, StateError:
	default:
		return Status{}, fmt.Errorf("invalid STATUS state %q", status.State)
	}
	return status, nil
}

func (s *store) writeStatus(status Status) (Status, error) {
	status.Version = 1
	status.UpdatedAt = time.Now()
	status.LastError = boundText(status.LastError, 2000)
	status.LastCancellation = boundText(status.LastCancellation, 2000)
	front, err := yaml.Marshal(status)
	if err != nil {
		return Status{}, err
	}
	body := fmt.Sprintf("# Codebase Status\n\nState: %s\nUpdated: %s\n", status.State, status.UpdatedAt.UTC().Format(time.RFC3339))
	if err := atomicWrite(s.statusPath(), append(append([]byte("---\n"), front...), []byte("---\n"+body)...), 0o600); err != nil {
		return Status{}, err
	}
	return status, nil
}

func boundText(v string, n int) string {
	v = strings.TrimSpace(v)
	return truncateUTF8(v, n)
}

func truncateUTF8(v string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(v) <= maxBytes {
		return v
	}
	cut := 0
	for index := range v {
		if index > maxBytes {
			break
		}
		cut = index
	}
	return v[:cut]
}

func createIfAbsent(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
