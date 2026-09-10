package sink

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/basenana/friday/core/actor/events"
)

// JSONL appends events as one JSON object per line to a file. Writes
// are serialized under a mutex; fsync is not called by default.
type JSONL struct {
	path        string
	mu          sync.Mutex
	f           *os.File
	enc         *json.Encoder
	maxBytes    int64
	targetBytes int64
}

// NewJSONL opens (or creates) the JSONL file at path for append.
// The file is created with mode 0600 if it does not exist.
func NewJSONL(path string) (*JSONL, error) {
	return NewBoundedJSONL(path, 0, 0)
}

// NewBoundedJSONL opens a JSONL sink that compacts itself once maxBytes is
// exceeded. A zero maxBytes preserves the unbounded historical behavior.
func NewBoundedJSONL(path string, maxBytes, targetBytes int64) (*JSONL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("sink: open jsonl %s: %w", path, err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	if maxBytes > 0 && (targetBytes <= 0 || targetBytes >= maxBytes) {
		targetBytes = maxBytes * 3 / 4
	}
	return &JSONL{path: path, f: f, enc: enc, maxBytes: maxBytes, targetBytes: targetBytes}, nil
}

// Append writes one event line.
func (j *JSONL) Append(_ context.Context, evt events.Event) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.enc.Encode(evt); err != nil {
		return fmt.Errorf("sink: encode event: %w", err)
	}
	if j.maxBytes > 0 {
		info, err := j.f.Stat()
		if err != nil {
			return fmt.Errorf("sink: stat jsonl: %w", err)
		}
		if info.Size() > j.maxBytes {
			if err := compactJSONLFile(j.f, info.Size(), j.maxBytes, j.targetBytes); err != nil {
				return fmt.Errorf("sink: compact jsonl: %w", err)
			}
		}
	}
	return nil
}

// CompactJSONL bounds an existing log while preserving a complete recent run
// boundary where possible. It is safe to call before opening an append sink.
func CompactJSONL(path string, maxBytes, targetBytes int64) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || maxBytes <= 0 || info.Size() <= maxBytes {
		return err
	}
	if targetBytes <= 0 || targetBytes >= maxBytes {
		targetBytes = maxBytes * 3 / 4
	}
	return compactJSONLFile(f, info.Size(), maxBytes, targetBytes)
}

func compactJSONLFile(f *os.File, size, maxBytes, targetBytes int64) error {
	start := size - targetBytes
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(f)
	skipped, err := reader.ReadBytes('\n')
	if err != nil {
		return err
	}
	start += int64(len(skipped))
	decoder := json.NewDecoder(reader)
	keepFrom := start
	for {
		recordStart := decoder.InputOffset()
		var evt events.Event
		if err := decoder.Decode(&evt); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if evt.Type == events.KindRunStarted {
			keepFrom = start + recordStart
			break
		}
	}
	if _, err := f.Seek(keepFrom, io.SeekStart); err != nil {
		return err
	}
	tail, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(tail)) > maxBytes {
		cut := int64(len(tail)) - targetBytes
		for cut < int64(len(tail)) && tail[cut] != '\n' {
			cut++
		}
		if cut < int64(len(tail)) {
			tail = tail[cut+1:]
		}
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := f.Write(tail); err != nil {
		return err
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
