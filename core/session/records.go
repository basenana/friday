package session

import (
	"context"
	"errors"
)

// ErrRecordNotFound is returned when a session record has not been created.
var ErrRecordNotFound = errors.New("session record not found")

// RecordStore persists namespaced auxiliary data alongside a session.
// UpdateSessionRecord must serialize concurrent updates for one record.
type RecordStore interface {
	ReadSessionRecord(ctx context.Context, sessionID, namespace string) ([]byte, error)
	UpdateSessionRecord(ctx context.Context, sessionID, namespace string, update func([]byte) ([]byte, error)) error
}

// ReadRecord reads one record bound to this session. Returned bytes are a copy.
func (s *Session) ReadRecord(ctx context.Context, namespace string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	if data, ok := s.records[namespace]; ok {
		out := append([]byte(nil), data...)
		s.mu.RUnlock()
		return out, nil
	}
	store := s.recordStore
	s.mu.RUnlock()

	if store == nil {
		return nil, ErrRecordNotFound
	}
	data, err := store.ReadSessionRecord(ctx, s.ID, namespace)
	if err != nil {
		return nil, err
	}
	data = append([]byte(nil), data...)
	s.mu.Lock()
	if s.records == nil {
		s.records = make(map[string][]byte)
	}
	s.records[namespace] = data
	s.mu.Unlock()
	return append([]byte(nil), data...), nil
}

// UpdateRecord atomically updates one record and refreshes the session-local
// snapshot used when the session is forked.
func (s *Session) UpdateRecord(ctx context.Context, namespace string, update func([]byte) ([]byte, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if update == nil {
		return errors.New("session record update is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records == nil {
		s.records = make(map[string][]byte)
	}

	var updated []byte
	if s.recordStore != nil {
		err := s.recordStore.UpdateSessionRecord(ctx, s.ID, namespace, func(current []byte) ([]byte, error) {
			if len(current) == 0 {
				current = s.records[namespace]
			}
			next, err := update(append([]byte(nil), current...))
			if err == nil {
				updated = append([]byte(nil), next...)
			}
			return next, err
		})
		if err != nil {
			return err
		}
	} else {
		next, err := update(append([]byte(nil), s.records[namespace]...))
		if err != nil {
			return err
		}
		updated = append([]byte(nil), next...)
	}

	s.records[namespace] = updated
	return nil
}

func cloneRecords(src map[string][]byte) map[string][]byte {
	dst := make(map[string][]byte, len(src))
	for key, value := range src {
		dst[key] = append([]byte(nil), value...)
	}
	return dst
}
