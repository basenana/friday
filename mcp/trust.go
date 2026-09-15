package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type trustStore struct {
	path    string
	project string
	mu      sync.Mutex
}
type trustDocument struct {
	Version  int                          `json:"version"`
	Projects map[string]map[string]string `json:"projects"`
}

func newTrustStore(path, project string) *trustStore {
	return &trustStore{path: path, project: project}
}

func canonicalProject(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
}

func (s *trustStore) trusted(name, digest string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.load()
	if err != nil {
		return false
	}
	return doc.Projects[s.project][name] == digest
}

func (s *trustStore) set(name, digest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.load()
	if err != nil {
		return err
	}
	if doc.Projects == nil {
		doc.Projects = make(map[string]map[string]string)
	}
	if doc.Projects[s.project] == nil {
		doc.Projects[s.project] = make(map[string]string)
	}
	doc.Projects[s.project][name] = digest
	return s.save(doc)
}

func (s *trustStore) remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.load()
	if err != nil {
		return err
	}
	delete(doc.Projects[s.project], name)
	return s.save(doc)
}

func (s *trustStore) load() (trustDocument, error) {
	doc := trustDocument{Version: 1, Projects: make(map[string]map[string]string)}
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return doc, nil
	}
	if err != nil {
		return doc, fmt.Errorf("read MCP trust store: %w", err)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return doc, fmt.Errorf("decode MCP trust store: %w", err)
	}
	if doc.Projects == nil {
		doc.Projects = make(map[string]map[string]string)
	}
	return doc, nil
}

func (s *trustStore) save(doc trustDocument) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".mcp-trust-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, s.path)
}
