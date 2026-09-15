// Package cache provides a small, reusable, disk-backed JSON cache.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

type Status string

const (
	StatusMiss  Status = "miss"
	StatusFresh Status = "fresh"
	StatusStale Status = "stale"
)

const defaultMaxEntrySize int64 = 16 << 20

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

var ErrEntryTooLarge = errors.New("cache entry exceeds maximum size")

type Store interface {
	Get(ctx context.Context, namespace, key string, target any) (Status, error)
	Put(ctx context.Context, namespace, key string, value any, ttl time.Duration) error
	Delete(ctx context.Context, namespace, key string) error
	Clear(ctx context.Context, namespace string) error
	Prune(ctx context.Context, namespace string) error
}

type Option func(*FileStore)

func WithClock(now func() time.Time) Option { return func(s *FileStore) { s.now = now } }
func WithMaxEntrySize(n int64) Option       { return func(s *FileStore) { s.maxEntrySize = n } }

type FileStore struct {
	root         string
	now          func() time.Time
	maxEntrySize int64
	locks        sync.Map
}

type localLock struct{ token chan struct{} }

func newLocalLock() *localLock {
	lock := &localLock{token: make(chan struct{}, 1)}
	lock.token <- struct{}{}
	return lock
}

type envelope struct {
	Version   int             `json:"version"`
	CreatedAt time.Time       `json:"createdAt"`
	ExpiresAt *time.Time      `json:"expiresAt,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

func NewFileStore(root string, opts ...Option) *FileStore {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	s := &FileStore{root: filepath.Clean(root), now: time.Now, maxEntrySize: defaultMaxEntrySize}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *FileStore) Get(ctx context.Context, namespace, key string, target any) (Status, error) {
	if err := validate(namespace, key); err != nil {
		return StatusMiss, err
	}
	if target == nil {
		return StatusMiss, errors.New("cache target is nil")
	}
	unlock, err := s.lock(ctx, namespace)
	if err != nil {
		return StatusMiss, err
	}
	defer unlock()
	path, err := s.entryPath(namespace, key, false)
	if err != nil {
		return StatusMiss, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return StatusMiss, nil
	}
	if err != nil {
		return StatusMiss, fmt.Errorf("open cache entry: %w", err)
	}
	defer f.Close()
	limited := io.LimitReader(f, s.maxEntrySize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return StatusMiss, fmt.Errorf("read cache entry: %w", err)
	}
	if int64(len(data)) > s.maxEntrySize {
		return StatusMiss, ErrEntryTooLarge
	}
	var entry envelope
	if err := json.Unmarshal(data, &entry); err != nil {
		return StatusMiss, fmt.Errorf("decode cache entry: %w", err)
	}
	if entry.Version != 1 {
		return StatusMiss, fmt.Errorf("unsupported cache entry version %d", entry.Version)
	}
	if err := json.Unmarshal(entry.Payload, target); err != nil {
		return StatusMiss, fmt.Errorf("decode cache payload: %w", err)
	}
	if entry.ExpiresAt != nil && !s.now().Before(*entry.ExpiresAt) {
		return StatusStale, nil
	}
	return StatusFresh, nil
}

func (s *FileStore) Put(ctx context.Context, namespace, key string, value any, ttl time.Duration) error {
	if err := validate(namespace, key); err != nil {
		return err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode cache payload: %w", err)
	}
	now := s.now().UTC()
	entry := envelope{Version: 1, CreatedAt: now, Payload: payload}
	if ttl > 0 {
		expiry := now.Add(ttl)
		entry.ExpiresAt = &expiry
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode cache entry: %w", err)
	}
	if int64(len(data)) > s.maxEntrySize {
		return ErrEntryTooLarge
	}
	unlock, err := s.lock(ctx, namespace)
	if err != nil {
		return err
	}
	defer unlock()
	path, err := s.entryPath(namespace, key, true)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cache-*")
	if err != nil {
		return fmt.Errorf("create cache temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write cache entry: %w", err)
	}
	if err := replaceFile(tmpName, path); err != nil {
		return fmt.Errorf("replace cache entry: %w", err)
	}
	if dir, openErr := os.Open(filepath.Dir(path)); openErr == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func (s *FileStore) Delete(ctx context.Context, namespace, key string) error {
	if err := validate(namespace, key); err != nil {
		return err
	}
	unlock, err := s.lock(ctx, namespace)
	if err != nil {
		return err
	}
	defer unlock()
	path, err := s.entryPath(namespace, key, false)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *FileStore) Clear(ctx context.Context, namespace string) error {
	if err := validate(namespace, "x"); err != nil {
		return err
	}
	unlock, err := s.lock(ctx, namespace)
	if err != nil {
		return err
	}
	defer unlock()
	dir, err := s.namespacePath(namespace, false)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && filepath.Ext(entry.Name()) == ".json" {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func (s *FileStore) Prune(ctx context.Context, namespace string) error {
	if err := validate(namespace, "x"); err != nil {
		return err
	}
	unlock, err := s.lock(ctx, namespace)
	if err != nil {
		return err
	}
	defer unlock()
	dir, err := s.namespacePath(namespace, false)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, item := range entries {
		if !item.Type().IsRegular() || filepath.Ext(item.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, item.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil || int64(len(data)) > s.maxEntrySize {
			continue
		}
		var entry envelope
		if json.Unmarshal(data, &entry) == nil && entry.ExpiresAt != nil && !s.now().Before(*entry.ExpiresAt) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func validate(namespace, key string) error {
	if !safeName.MatchString(namespace) {
		return fmt.Errorf("invalid cache namespace %q", namespace)
	}
	if !safeName.MatchString(key) {
		return fmt.Errorf("invalid cache key %q", key)
	}
	return nil
}

func (s *FileStore) entryPath(namespace, key string, create bool) (string, error) {
	dir, err := s.namespacePath(namespace, create)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, key+".json")
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("cache entry is a symlink: %s", path)
	}
	return path, nil
}

func (s *FileStore) namespacePath(namespace string, create bool) (string, error) {
	if info, err := os.Lstat(s.root); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("cache root is a symlink: %s", s.root)
	}
	if create {
		if err := os.MkdirAll(s.root, 0o700); err != nil {
			return "", err
		}
		_ = os.Chmod(s.root, 0o700)
	}
	dir := filepath.Join(s.root, namespace)
	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("cache namespace is a symlink: %s", dir)
	}
	if create {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		_ = os.Chmod(dir, 0o700)
	}
	return dir, nil
}
func (s *FileStore) lock(ctx context.Context, namespace string) (func(), error) {
	value, _ := s.locks.LoadOrStore(namespace, newLocalLock())
	local := value.(*localLock)
	select {
	case <-local.token:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	dir, err := s.namespacePath(namespace, true)
	if err != nil {
		local.token <- struct{}{}
		return nil, err
	}
	file, err := lockFile(ctx, filepath.Join(dir, ".lock"))
	if err != nil {
		local.token <- struct{}{}
		return nil, err
	}
	return func() { unlockFile(file); local.token <- struct{}{} }, nil
}
