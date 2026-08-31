package sink

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/basenana/friday/core/actor/events"
)

// JSONL appends events as one JSON object per line to a file. Writes
// are serialized under a mutex; fsync is not called by default.
type JSONL struct {
	path string
	mu   sync.Mutex
	f    *os.File
	enc  *json.Encoder
}

// NewJSONL opens (or creates) the JSONL file at path for append.
// The file is created with mode 0600 if it does not exist.
func NewJSONL(path string) (*JSONL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("sink: open jsonl %s: %w", path, err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return &JSONL{path: path, f: f, enc: enc}, nil
}

// Append writes one event line.
func (j *JSONL) Append(_ context.Context, evt events.Event) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.enc.Encode(evt); err != nil {
		return fmt.Errorf("sink: encode event: %w", err)
	}
	return nil
}

// Close closes the underlying file. Subsequent Append calls return an
// error.
func (j *JSONL) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	return err
}

// compile-time guard.
var _ EventSink = (*JSONL)(nil)
