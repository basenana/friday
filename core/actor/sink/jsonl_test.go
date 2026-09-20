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

func TestJSONLAppendAfterCloseFails(t *testing.T) {
	sink, err := NewJSONL(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sink.Append(context.Background(), deltaEvent()); err == nil {
		t.Fatal("Append after Close succeeded")
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
func eventLine(t *testing.T, evt events.Event) []byte {
	t.Helper()
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func compactFixture(t *testing.T, data []byte, maxBytes, targetBytes int64) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := compactJSONLFile(f, int64(len(data)), maxBytes, targetBytes); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCompactJSONLSkipsMalformedLineBeforeRunBoundary(t *testing.T) {
	prefix := append(bytes.Repeat([]byte("p"), 256), '\n')
	ordinary := eventLine(t, events.NewEvent(events.KindTextMessageContent, "old-run"))
	runStart := eventLine(t, events.NewEvent(events.KindRunStarted, "kept-run"))
	runEnd := eventLine(t, events.NewEvent(events.KindRunFinished, "kept-run"))
	data := bytes.Join([][]byte{prefix, []byte("not-json\n"), ordinary, runStart, runEnd}, nil)

	got := compactFixture(t, data, int64(len(data)), int64(len(data)-128))
	want := append(append([]byte{}, runStart...), runEnd...)
	if !bytes.Equal(got, want) {
		t.Fatalf("compacted tail = %q, want run boundary suffix %q", got, want)
	}
}

func TestCompactJSONLFallsBackAcrossMalformedLinesWithoutRunBoundary(t *testing.T) {
	prefix := append(bytes.Repeat([]byte("p"), 256), '\n')
	valid := eventLine(t, events.NewEvent(events.KindRunFinished, "old-run"))
	want := append([]byte("not-json\n"), valid...)
	data := append(append([]byte{}, prefix...), want...)

	got := compactFixture(t, data, int64(len(data)), int64(len(data)-128))
	if !bytes.Equal(got, want) {
		t.Fatalf("compacted tail = %q, want complete fallback suffix %q", got, want)
	}
}

func TestCompactJSONLMalformedFallbackRemainsBoundedAtLineBoundary(t *testing.T) {
	prefix := append(bytes.Repeat([]byte("p"), 256), '\n')
	var tail []byte
	tail = append(tail, []byte("not-json\n")...)
	for i := 0; i < 8; i++ {
		evt := events.NewEvent(events.KindTextMessageContent, "old-run").
			WithPayload(events.TextMessageContentData{Content: strings.Repeat(string(rune('a'+i)), 96)})
		tail = append(tail, eventLine(t, evt)...)
	}
	data := append(append([]byte{}, prefix...), tail...)
	const maxBytes, targetBytes = int64(600), int64(400)

	got := compactFixture(t, data, maxBytes, targetBytes)
	if int64(len(got)) > maxBytes {
		t.Fatalf("compacted size = %d, want <= %d", len(got), maxBytes)
	}
	if len(got) == 0 || got[len(got)-1] != '\n' {
		t.Fatalf("compacted tail is not newline terminated: %q", got)
	}
	if !bytes.HasSuffix(data, got) {
		t.Fatal("compacted output does not begin at a complete physical-line boundary")
	}
}

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
