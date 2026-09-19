package sink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basenana/friday/core/actor/events"
)

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

func deltaEvent() events.Event {
	return events.NewEvent(events.KindTextMessageContent, "run-buf").
		WithMessageID("message").
		WithPayload(events.TextMessageContentData{Content: strings.Repeat("a", 32)})
}

func runFinishedEvent() events.Event {
	return events.NewEvent(events.KindRunFinished, "run-buf").
		WithPayload(events.RunFinishedData{StopReason: "end_turn"})
}

// TestJSONLBufferFlushesOnTerminalEventsAndClose pins the buffering
// contract: non-terminal events stay in the write buffer, terminal events
// (RunFinished/RunError) flush, and Close drains whatever is left.
func TestJSONLBufferFlushesOnTerminalEventsAndClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	ctx := context.Background()

	sink, err := NewJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if err := sink.Append(ctx, deltaEvent()); err != nil {
			t.Fatal(err)
		}
	}
	if lines := countLines(t, path); lines >= 100 {
		t.Fatalf("non-terminal events were flushed eagerly: %d lines on disk", lines)
	}

	if err := sink.Append(ctx, runFinishedEvent()); err != nil {
		t.Fatal(err)
	}
	if lines := countLines(t, path); lines != 101 {
		t.Fatalf("terminal event did not flush the buffer: %d lines, want 101", lines)
	}

	for i := 0; i < 10; i++ {
		if err := sink.Append(ctx, deltaEvent()); err != nil {
			t.Fatal(err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	if lines := countLines(t, path); lines != 111 {
		t.Fatalf("Close did not flush the buffer: %d lines, want 111", lines)
	}
}

func TestJSONLRunErrorAlsoFlushes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	ctx := context.Background()

	sink, err := NewJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := sink.Append(ctx, deltaEvent()); err != nil {
			t.Fatal(err)
		}
	}
	if lines := countLines(t, path); lines >= 5 {
		t.Fatalf("non-terminal events were flushed eagerly: %d lines", lines)
	}
	if err := sink.Append(ctx, events.NewEvent(events.KindRunError, "run-buf").
		WithPayload(events.RunErrorData{Message: "boom"})); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	if lines := countLines(t, path); lines != 6 {
		t.Fatalf("RunError did not flush the buffer: %d lines, want 6", lines)
	}
}

// TestBoundedJSONLCompactsAfterBufferedFlush ensures the size check still
// runs on flush and compaction keeps the log bounded with valid records.
func TestBoundedJSONLCompactsAfterBufferedFlush(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	const maxBytes = 4096
	ctx := context.Background()

	sink, err := NewBoundedJSONL(path, maxBytes, maxBytes*3/4)
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 12; round++ {
		if err := sink.Append(ctx, events.NewEvent(events.KindRunStarted, "run")); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 8; i++ {
			if err := sink.Append(ctx, deltaEvent()); err != nil {
				t.Fatal(err)
			}
		}
		if err := sink.Append(ctx, runFinishedEvent()); err != nil {
			t.Fatal(err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxBytes {
		t.Fatalf("compaction did not bound the log: %d > %d", info.Size(), maxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	runs := 0
	for {
		var evt events.Event
		if err := dec.Decode(&evt); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("compacted log contains an invalid record: %v", err)
		}
		if evt.Type == events.KindRunStarted {
			runs++
		}
	}
	if runs == 0 {
		t.Fatal("compaction dropped all run boundaries")
	}
}
