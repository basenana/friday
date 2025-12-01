package memory

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Storage interface {
	Append(ctx context.Context, key, value string) error
	Replace(ctx context.Context, key, value string) error
	Get(ctx context.Context, key string) (Records, error)
	List(ctx context.Context, walk func(record *Records) bool) error
}

type inMemory struct {
	records map[string]*Records
	mutex   sync.RWMutex
}

func newInMemoryStorage() Storage {
	return &inMemory{
		records: make(map[string]*Records),
		mutex:   sync.RWMutex{},
	}
}

var _ Storage = &inMemory{}

func (m *inMemory) Append(ctx context.Context, key, content string) error {
	m.mutex.Lock()
	r, ok := m.records[key]
	if !ok {
		r = &Records{}
		m.records[key] = r
	}
	m.mutex.Unlock()

	r.Records = append(r.Records, Record{
		Content: content,
		Time:    time.Now(),
	})
	return nil
}

func (m *inMemory) Replace(ctx context.Context, key, content string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if content == "" {
		delete(m.records, key)
		return nil
	}

	r, ok := m.records[key]
	if !ok {
		r = &Records{}
		m.records[key] = r
	}
	r.Records = []Record{{Content: content, Time: time.Now()}}
	return nil
}

func (m *inMemory) Get(ctx context.Context, key string) (Records, error) {
	m.mutex.RLock()
	r, ok := m.records[key]
	m.mutex.RUnlock()
	if !ok {
		return Records{}, fmt.Errorf("%s not exist", key)
	}
	return *r, nil
}

func (m *inMemory) List(ctx context.Context, walk func(record *Records) bool) error {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	for _, record := range m.records {
		if walk(record) {
			return nil
		}
	}

	return nil
}

type Records struct {
	Key     string   `json:"key"`
	Records []Record `json:"records"`
}

type Record struct {
	Content string    `json:"content"`
	Time    time.Time `json:"time"`
}
