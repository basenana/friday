package project

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// UserHistoryEntry is one project-scoped TUI composer submission.
type UserHistoryEntry struct {
	Version   int       `json:"version"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
	SessionID string    `json:"session_id"`
}

func (s *FileStore) userHistoryPath(id string) string {
	return filepath.Join(s.projectDir(id), "user_history.jsonl")
}

func (s *FileStore) LoadUserHistory(id string) ([]UserHistoryEntry, error) {
	if !validID(id) {
		return nil, fmt.Errorf("invalid project id")
	}
	data, err := os.ReadFile(s.userHistoryPath(id))
	if os.IsNotExist(err) {
		return []UserHistoryEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeUserHistory(data), nil
}

func (s *FileStore) AppendUserHistory(id string, entry UserHistoryEntry) (bool, error) {
	if !validID(id) {
		return false, fmt.Errorf("invalid project id")
	}
	if entry.Version != 1 || entry.Text == "" {
		return false, fmt.Errorf("invalid user history entry")
	}
	appended := false
	err := s.withLock(id, func() error {
		path := s.userHistoryPath(id)
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if len(data) > 0 && data[len(data)-1] != '\n' {
			committedBytes := int64(0)
			if lastNewline := bytes.LastIndexByte(data, '\n'); lastNewline >= 0 {
				committedBytes = int64(lastNewline + 1)
			}
			if err := os.Truncate(path, committedBytes); err != nil {
				return err
			}
			data = data[:committedBytes]
		}
		history := decodeUserHistory(data)
		if len(history) > 0 && history[len(history)-1].Text == entry.Text {
			return nil
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if err := f.Chmod(0o600); err != nil {
			_ = f.Close()
			return err
		}
		if _, err := f.Write(append(encoded, '\n')); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		appended = true
		return nil
	})
	return appended, err
}

func decodeUserHistory(data []byte) []UserHistoryEntry {
	if len(data) == 0 {
		return []UserHistoryEntry{}
	}
	lines := bytes.Split(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		lines = lines[:len(lines)-1]
	}
	entries := make([]UserHistoryEntry, 0, len(lines))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var entry UserHistoryEntry
		if err := json.Unmarshal(line, &entry); err != nil || entry.Version != 1 || entry.Text == "" {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}
