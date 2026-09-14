package tui

import (
	"reflect"
	"testing"

	"github.com/basenana/friday/core/actor/events"
	corelogger "github.com/basenana/friday/core/logger"
)

type tuiLogEntry struct {
	level   string
	message string
	fields  []interface{}
}

type tuiCaptureLogger struct {
	entries []tuiLogEntry
}

func (l *tuiCaptureLogger) Named(string) corelogger.Logger { return l }
func (l *tuiCaptureLogger) With(...interface{}) corelogger.Logger {
	return l
}
func (l *tuiCaptureLogger) Info(...interface{})          {}
func (l *tuiCaptureLogger) Warn(...interface{})          {}
func (l *tuiCaptureLogger) Error(...interface{})         {}
func (l *tuiCaptureLogger) Infof(string, ...interface{}) {}
func (l *tuiCaptureLogger) Warnf(string, ...interface{}) {}
func (l *tuiCaptureLogger) Errorf(string, ...interface{}) {
}
func (l *tuiCaptureLogger) Infow(message string, fields ...interface{}) {
	l.entries = append(l.entries, tuiLogEntry{level: "info", message: message, fields: fields})
}
func (l *tuiCaptureLogger) Warnw(message string, fields ...interface{}) {
	l.entries = append(l.entries, tuiLogEntry{level: "warn", message: message, fields: fields})
}
func (l *tuiCaptureLogger) Errorw(message string, fields ...interface{}) {
	l.entries = append(l.entries, tuiLogEntry{level: "error", message: message, fields: fields})
}

func captureTUILogs(t *testing.T) *tuiCaptureLogger {
	t.Helper()
	original := corelogger.Root()
	capture := &tuiCaptureLogger{}
	corelogger.SetRoot(capture)
	t.Cleanup(func() { corelogger.SetRoot(original) })
	return capture
}

func TestAppendErrorBlockLogsDisplayedError(t *testing.T) {
	capture := captureTUILogs(t)
	m := &model{sessionID: "session-1", textBlock: -1, reasonBlock: -1}

	m.appendBlock(chatBlock{kind: blockError, content: "restore plan: broken state"})

	want := tuiLogEntry{
		level:   "warn",
		message: "error displayed in TUI",
		fields:  []interface{}{"session_id", "session-1", "error", "restore plan: broken state"},
	}
	if !reflect.DeepEqual(capture.entries, []tuiLogEntry{want}) {
		t.Fatalf("unexpected logs: %#v", capture.entries)
	}
}

func TestFailedToolCallLogsParsedErrorWithoutArguments(t *testing.T) {
	capture := captureTUILogs(t)
	m := &model{
		sessionID:   "session-1",
		toolCalls:   make(map[string]int),
		textBlock:   -1,
		reasonBlock: -1,
	}
	start := events.NewEvent(events.KindToolCallStart, "run-1").WithPayload(events.ToolCallStartData{
		ToolCallID: "call-1",
		ToolName:   "weather",
	})
	output := "Error: invalid city\nUse a city name instead."
	result := events.NewEvent(events.KindToolCallResult, "run-1").WithPayload(events.ToolCallResultData{
		ToolCallID: "call-1",
		Success:    false,
		Output:     output,
	})

	m.handleActorEvent(start)
	m.handleActorEvent(result)

	if len(capture.entries) != 2 {
		t.Fatalf("expected start and failure logs, got %#v", capture.entries)
	}
	failure := capture.entries[1]
	if failure.level != "warn" || failure.message != "tool call failed" {
		t.Fatalf("unexpected failure log: %#v", failure)
	}
	wantFields := []interface{}{
		"session_id", "session-1",
		"run_id", "run-1",
		"tool_call_id", "call-1",
		"tool_name", "weather",
		"success", false,
		"output_bytes", len(output),
		"error", "invalid city\nUse a city name instead.",
	}
	if !reflect.DeepEqual(failure.fields, wantFields) {
		t.Fatalf("unexpected failure fields: %#v", failure.fields)
	}
}
