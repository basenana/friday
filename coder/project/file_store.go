package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type FileStore struct {
	basePath string
	locks    sync.Map
}

func NewFileStore(basePath string) *FileStore { return &FileStore{basePath: basePath} }

func (s *FileStore) projectDir(id string) string { return filepath.Join(s.basePath, id) }
func (s *FileStore) metaPath(id string) string {
	return filepath.Join(s.projectDir(id), "project.json")
}
func (s *FileStore) currentPath(id string) string { return filepath.Join(s.projectDir(id), "current") }
func (s *FileStore) refsDir(id string) string     { return filepath.Join(s.projectDir(id), "sessions") }
func (s *FileStore) refPath(id, sessionID string) string {
	return filepath.Join(s.refsDir(id), sessionID+".json")
}

func (s *FileStore) withLock(id string, fn func() error) error {
	value, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	unlock, err := acquireProjectFileLock(filepath.Join(s.projectDir(id), ".lock"))
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

func (s *FileStore) Ensure(meta Metadata) error {
	return s.withLock(meta.ID, func() error {
		if err := os.MkdirAll(s.refsDir(meta.ID), 0o700); err != nil {
			return err
		}
		data, err := os.ReadFile(s.metaPath(meta.ID))
		if err == nil {
			var existing Metadata
			if err := json.Unmarshal(data, &existing); err != nil {
				return fmt.Errorf("decode project metadata: %w", err)
			}
			switch existing.Version {
			case 1:
				if existing.ID != meta.ID || existing.Root != meta.Root {
					return fmt.Errorf("project identity collision for %s", meta.ID)
				}
				meta.CodebaseEnabled = existing.CodebaseEnabled
				meta.CreatedAt = existing.CreatedAt
				return writeAtomicJSON(s.metaPath(meta.ID), meta, 0o600)
			case 2:
				if existing.ID == meta.ID && existing.Name == meta.Name && existing.Repository == meta.Repository {
					return nil
				}
			default:
				return fmt.Errorf("project identity collision for %s", meta.ID)
			}
			return fmt.Errorf("project identity collision for %s", meta.ID)
		}
		if !os.IsNotExist(err) {
			return err
		}
		return writeAtomicJSON(s.metaPath(meta.ID), meta, 0o600)
	})
}

func (s *FileStore) readMetadata(id string) (Metadata, error) {
	if !validID(id) {
		return Metadata{}, fmt.Errorf("invalid project id")
	}
	data, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		return Metadata{}, err
	}
	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return Metadata{}, fmt.Errorf("decode project metadata: %w", err)
	}
	if meta.ID != id || strings.TrimSpace(meta.Root) == "" {
		return Metadata{}, fmt.Errorf("project metadata identity mismatch for %s", id)
	}
	if meta.Version == 1 {
		return meta, nil
	}
	if meta.Version != 2 || strings.TrimSpace(meta.Name) == "" || strings.TrimSpace(meta.Repository) == "" {
		return Metadata{}, fmt.Errorf("project metadata identity mismatch for %s", id)
	}
	return meta, nil
}

func (s *FileStore) CodebaseEnabled(id string) (bool, error) {
	meta, err := s.readMetadata(id)
	return meta.CodebaseEnabled, err
}

func (s *FileStore) SetCodebaseEnabled(id string, enabled bool) error {
	return s.withLock(id, func() error {
		meta, err := s.readMetadata(id)
		if err != nil {
			return err
		}
		if meta.CodebaseEnabled == enabled {
			return nil
		}
		data, err := os.ReadFile(s.metaPath(id))
		if err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return fmt.Errorf("decode project metadata: %w", err)
		}
		updatedAt := time.Now()
		fields["codebase_enabled"], _ = json.Marshal(enabled)
		fields["updated_at"], _ = json.Marshal(updatedAt)
		return writeAtomicJSON(s.metaPath(id), fields, 0o600)
	})
}

func (s *FileStore) Current(id string) (string, error) {
	data, err := os.ReadFile(s.currentPath(id))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (s *FileStore) SetCurrent(id, sessionID string) error {
	return s.withLock(id, func() error {
		return writeAtomic(s.currentPath(id), []byte(strings.TrimSpace(sessionID)+"\n"), 0o600)
	})
}

func (s *FileStore) ListRefs(id string) ([]SessionRef, error) {
	entries, err := os.ReadDir(s.refsDir(id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	refs := make([]SessionRef, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.refsDir(id), entry.Name()))
		if err != nil {
			return nil, err
		}
		var ref SessionRef
		if err := json.Unmarshal(data, &ref); err != nil {
			return nil, fmt.Errorf("decode project session reference: %w", err)
		}
		if ref.Version != 1 || ref.SessionID == "" || entry.Name() != ref.SessionID+".json" {
			return nil, fmt.Errorf("project session reference identity mismatch")
		}
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].AddedAt.Before(refs[j].AddedAt) })
	return refs, nil
}

func (s *FileStore) HasRef(id, sessionID string) (bool, error) {
	if !validID(sessionID) {
		return false, nil
	}
	var ref SessionRef
	data, err := os.ReadFile(s.refPath(id, sessionID))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, &ref); err != nil {
		return false, err
	}
	return ref.Version == 1 && ref.SessionID == sessionID, nil
}

func (s *FileStore) AddRef(id string, ref SessionRef) error {
	if !validID(ref.SessionID) {
		return fmt.Errorf("invalid session id")
	}
	return s.withLock(id, func() error {
		if err := os.MkdirAll(s.refsDir(id), 0o700); err != nil {
			return err
		}
		return writeAtomicJSON(s.refPath(id, ref.SessionID), ref, 0o600)
	})
}

func (s *FileStore) RemoveRef(id, sessionID string) error {
	if !validID(sessionID) {
		return fmt.Errorf("invalid session id")
	}
	return s.withLock(id, func() error {
		err := os.Remove(s.refPath(id, sessionID))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	})
}

func validID(id string) bool {
	return id != "" && id == strings.TrimSpace(id) && id != "." && id != ".." && !strings.ContainsAny(id, `/\`)
}

func writeAtomicJSON(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'), mode)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
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
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
