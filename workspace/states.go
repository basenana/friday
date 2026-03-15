package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/basenana/friday/core/state"
)

type FileState struct {
	basePath string
	mu       sync.RWMutex
}

func NewFileState(basePath string) state.State {
	return &FileState{basePath: basePath}
}

func (f *FileState) Get(_ context.Context, scope state.StateScope, key string) (string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	filePath := f.filePath(scope, "")
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", errors.New("key not found")
	}

	var store map[string]string
	if err := json.Unmarshal(data, &store); err != nil {
		return "", err
	}

	val, ok := store[key]
	if !ok {
		return "", errors.New("key not found")
	}
	return val, nil
}

func (f *FileState) Set(_ context.Context, scope state.StateScope, key string, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	filePath := f.filePath(scope, "")

	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}

	store := make(map[string]string)
	if data, err := os.ReadFile(filePath); err == nil {
		_ = json.Unmarshal(data, &store)
	}

	store[key] = value

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filePath, data, 0644)
}

func (f *FileState) Delete(_ context.Context, scope state.StateScope, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	filePath := f.filePath(scope, "")

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}

	var store map[string]string
	if err := json.Unmarshal(data, &store); err != nil {
		return err
	}

	delete(store, key)

	newData, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filePath, newData, 0644)
}

func (f *FileState) List(_ context.Context, scope state.StateScope) ([]string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	filePath := f.filePath(scope, "")

	data, err := os.ReadFile(filePath)
	if err != nil {
		return []string{}, nil
	}

	var store map[string]string
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(store))
	for k := range store {
		keys = append(keys, k)
	}
	return keys, nil
}

func (f *FileState) WithUser(userID string) state.State {
	return &userFileState{
		parent: f,
		userID: userID,
	}
}

func (f *FileState) filePath(scope state.StateScope, userID string) string {
	switch scope {
	case state.ScopeApp:
		return filepath.Join(f.basePath, "app_state.json")
	case state.ScopeUser:
		return filepath.Join(f.basePath, "user_state_"+userID+".json")
	default:
		return filepath.Join(f.basePath, "app_state.json")
	}
}

type userFileState struct {
	parent *FileState
	userID string
}

func (u *userFileState) Get(ctx context.Context, scope state.StateScope, key string) (string, error) {
	u.parent.mu.RLock()
	defer u.parent.mu.RUnlock()

	filePath := u.parent.filePath(scope, u.userID)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", errors.New("key not found")
	}

	var store map[string]string
	if err := json.Unmarshal(data, &store); err != nil {
		return "", err
	}

	val, ok := store[key]
	if !ok {
		return "", errors.New("key not found")
	}
	return val, nil
}

func (u *userFileState) Set(ctx context.Context, scope state.StateScope, key string, value string) error {
	u.parent.mu.Lock()
	defer u.parent.mu.Unlock()

	filePath := u.parent.filePath(scope, u.userID)

	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}

	store := make(map[string]string)
	if data, err := os.ReadFile(filePath); err == nil {
		_ = json.Unmarshal(data, &store)
	}

	store[key] = value

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filePath, data, 0644)
}

func (u *userFileState) Delete(ctx context.Context, scope state.StateScope, key string) error {
	u.parent.mu.Lock()
	defer u.parent.mu.Unlock()

	filePath := u.parent.filePath(scope, u.userID)

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}

	var store map[string]string
	if err := json.Unmarshal(data, &store); err != nil {
		return err
	}

	delete(store, key)

	newData, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filePath, newData, 0644)
}

func (u *userFileState) List(ctx context.Context, scope state.StateScope) ([]string, error) {
	u.parent.mu.RLock()
	defer u.parent.mu.RUnlock()

	filePath := u.parent.filePath(scope, u.userID)

	data, err := os.ReadFile(filePath)
	if err != nil {
		return []string{}, nil
	}

	var store map[string]string
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(store))
	for k := range store {
		keys = append(keys, k)
	}
	return keys, nil
}

func (u *userFileState) WithUser(userID string) state.State {
	return u.parent.WithUser(userID)
}
