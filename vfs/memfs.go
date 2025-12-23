package vfs

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

type inMemoryFS struct {
	records map[string]*VFile
	mutex   sync.RWMutex
}

func NewInMemoryFS() VirtualFileSystem {
	return &inMemoryFS{
		records: make(map[string]*VFile),
		mutex:   sync.RWMutex{},
	}
}

var _ VirtualFileSystem = &inMemoryFS{}

func (m *inMemoryFS) ListFiles(ctx context.Context) ([]*VFile, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	result := make([]*VFile, 0, len(m.records))
	for _, f := range m.records {
		result = append(result, f)
	}
	return result, nil
}

func (m *inMemoryFS) ReadFile(ctx context.Context, fname string) (*VFile, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	f, ok := m.records[fname]
	if !ok {
		return nil, fmt.Errorf("file does not exist")
	}
	return f, nil
}

func (m *inMemoryFS) WriteFile(ctx context.Context, f *VFile) (*VFile, error) {
	if f.Filename == "" {
		f.Filename = uuid.New().String()
	}
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.records[f.Filename] = f
	return f, nil
}
