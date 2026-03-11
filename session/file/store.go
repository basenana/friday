package file

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/session"
)

type FileSessionStore struct {
	basePath string
}

func NewFileSessionStore(basePath string) *FileSessionStore {
	return &FileSessionStore{
		basePath: basePath,
	}
}

func (s *FileSessionStore) EnsureDir() error {
	return os.MkdirAll(s.basePath, 0755)
}

// Private methods - internal paths

func (s *FileSessionStore) sessionDir(id string) string {
	return filepath.Join(s.basePath, id)
}

func (s *FileSessionStore) metaPath(id string) string {
	return filepath.Join(s.sessionDir(id), "session.json")
}

func (s *FileSessionStore) historyPath(id string) string {
	return filepath.Join(s.sessionDir(id), "history.jsonl")
}

// Store interface implementation

func (s *FileSessionStore) Create(sessionID string, opts ...coresession.Option) (*coresession.Session, error) {
	if err := s.EnsureDir(); err != nil {
		return nil, err
	}

	// Create session directory
	sessionDir := s.sessionDir(sessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return nil, err
	}

	now := time.Now()
	meta := session.SessionMeta{
		ID:           sessionID,
		CreatedAt:    now,
		UpdatedAt:    now,
		MessageCount: 0,
	}

	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(s.metaPath(sessionID), metaData, 0644); err != nil {
		return nil, err
	}

	if err := os.WriteFile(s.historyPath(sessionID), []byte{}, 0644); err != nil {
		return nil, err
	}

	sess := coresession.New(sessionID, nil, append(opts, coresession.WithMessageWriter(s))...)
	return sess, nil
}

func (s *FileSessionStore) Load(sessionID string, opts ...coresession.Option) (*coresession.Session, error) {
	metaPath := s.metaPath(sessionID)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session not found: %s", sessionID)
		}
		return nil, err
	}

	var meta session.SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}

	messages, err := s.LoadMessages(sessionID)
	if err != nil {
		return nil, err
	}

	sess := coresession.New(sessionID, nil, append(opts,
		coresession.WithHistory(messages...),
		coresession.WithMessageWriter(s),
	)...)

	return sess, nil
}

func (s *FileSessionStore) Delete(sessionID string) error {
	sessionDir := s.sessionDir(sessionID)
	return os.RemoveAll(sessionDir)
}

func (s *FileSessionStore) List() ([]session.SessionMeta, error) {
	return s.listFiltered(false)
}

func (s *FileSessionStore) ListActive() ([]session.SessionMeta, error) {
	return s.listFiltered(true)
}

func (s *FileSessionStore) listFiltered(activeOnly bool) ([]session.SessionMeta, error) {
	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []session.SessionMeta{}, nil
		}
		return nil, err
	}

	var result []session.SessionMeta
	for _, entry := range entries {
		// Only process directories
		if !entry.IsDir() {
			continue
		}

		sessionID := entry.Name()
		metaPath := s.metaPath(sessionID)

		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}

		var meta session.SessionMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}

		// Filter archived sessions for ListActive
		if activeOnly && meta.Archived {
			continue
		}
		result = append(result, meta)
	}

	return result, nil
}

func (s *FileSessionStore) GetMeta(sessionID string) (*session.SessionMeta, error) {
	return s.loadMeta(sessionID)
}

func (s *FileSessionStore) UpdateAlias(sessionID, alias string) error {
	meta, err := s.loadMeta(sessionID)
	if err != nil {
		return err
	}
	meta.Alias = alias
	return s.saveMeta(sessionID, meta)
}

func (s *FileSessionStore) Archive(sessionID string) error {
	meta, err := s.loadMeta(sessionID)
	if err != nil {
		return err
	}
	meta.Archived = true
	return s.saveMeta(sessionID, meta)
}

func (s *FileSessionStore) Unarchive(sessionID string) error {
	meta, err := s.loadMeta(sessionID)
	if err != nil {
		return err
	}
	meta.Archived = false
	return s.saveMeta(sessionID, meta)
}

func (s *FileSessionStore) AppendMessages(sessionID string, msgs ...types.Message) error {
	historyPath := s.historyPath(sessionID)

	file, err := os.OpenFile(historyPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, msg := range msgs {
		data, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			continue
		}
	}

	s.updateMeta(sessionID, len(msgs))

	return nil
}

func (s *FileSessionStore) LoadMessages(sessionID string) ([]types.Message, error) {
	historyPath := s.historyPath(sessionID)

	data, err := os.ReadFile(historyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []types.Message{}, nil
		}
		return nil, err
	}

	var messages []types.Message
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var msg types.Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

func (s *FileSessionStore) ReplaceMessages(sessionID string, msgs ...types.Message) error {
	historyPath := s.historyPath(sessionID)

	// 1. Backup original file if exists
	if _, err := os.Stat(historyPath); err == nil {
		timestamp := time.Now().Format("20060102_150405")
		backupPath := filepath.Join(s.sessionDir(sessionID), fmt.Sprintf("history_origin_%s.jsonl", timestamp))
		if err := os.Rename(historyPath, backupPath); err != nil {
			return fmt.Errorf("failed to backup history: %w", err)
		}
	}

	// 2. Write new content
	file, err := os.OpenFile(historyPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, msg := range msgs {
		data, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			return err
		}
	}

	// 3. Update metadata
	return s.updateMetaCount(sessionID, len(msgs))
}

func (s *FileSessionStore) updateMetaCount(sessionID string, count int) error {
	meta, err := s.loadMeta(sessionID)
	if err != nil {
		return err
	}
	meta.UpdatedAt = time.Now()
	meta.MessageCount = count
	return s.saveMeta(sessionID, meta)
}

func (s *FileSessionStore) updateMeta(sessionID string, added int) error {
	metaPath := s.metaPath(sessionID)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return err
	}

	var meta session.SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return err
	}

	meta.UpdatedAt = time.Now()
	meta.MessageCount += added

	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(metaPath, metaData, 0644)
}

func (s *FileSessionStore) loadMeta(sessionID string) (*session.SessionMeta, error) {
	metaPath := s.metaPath(sessionID)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}
	var meta session.SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (s *FileSessionStore) saveMeta(sessionID string, meta *session.SessionMeta) error {
	metaPath := s.metaPath(sessionID)
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath, metaData, 0644)
}
